package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"darkserve/internal/auth"
	"darkserve/internal/config"
)

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func dirWritable(dir string) bool {
	f, err := os.CreateTemp(dir, ".darkserve-probe-*")
	if err != nil {
		return false
	}
	name := f.Name()
	f.Close()
	os.Remove(name)
	return true
}

func ensureDir(dir string) error {
	return os.MkdirAll(dir, 0o755)
}

// sanitizePath turns a user-entered folder name into a safe relative path:
// no absolute paths, no "..", no leading dots.
func sanitizePath(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, `\`, "/"))
	s = strings.Trim(s, "/")
	parts := []string{}
	for _, p := range strings.Split(s, "/") {
		p = strings.TrimSpace(p)
		if p == "" || p == "." || p == ".." || strings.HasPrefix(p, ".") {
			continue
		}
		parts = append(parts, p)
	}
	return strings.Join(parts, "/")
}

func lastLine(s string) string {
	parts := strings.FieldsFunc(s, func(r rune) bool { return r == '\n' || r == '\r' })
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}

/* ---------- login protection ---------- */

// Five wrong passwords from one address lock that address out for 10 minutes.
const (
	maxLoginFails = 5
	loginWindow   = 10 * time.Minute
)

var loginGuard = struct {
	sync.Mutex
	fails map[string][]time.Time
}{fails: map[string][]time.Time{}}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// loginLockout returns how long an address must wait, or 0 when it may try.
func loginLockout(key string) time.Duration {
	loginGuard.Lock()
	defer loginGuard.Unlock()
	recent := pruneFails(loginGuard.fails[key])
	loginGuard.fails[key] = recent
	if len(recent) < maxLoginFails {
		return 0
	}
	return loginWindow - time.Since(recent[0])
}

func recordLoginFail(key string) {
	loginGuard.Lock()
	defer loginGuard.Unlock()
	loginGuard.fails[key] = append(pruneFails(loginGuard.fails[key]), time.Now())
}

func clearLoginFails(key string) {
	loginGuard.Lock()
	defer loginGuard.Unlock()
	delete(loginGuard.fails, key)
}

// loginKey names the account that is being tried. The lockout belongs to
// the account, not to the machine: one person typing the wrong password
// must not lock everybody else out of the server.
func loginKey(ip, username string) string {
	return ip + "|" + strings.ToLower(strings.TrimSpace(username))
}

// clearAccountLockouts lifts the lockout of one account, whatever address
// it came from. It is used after the admin sets a new password, so the
// person can get in right away.
func clearAccountLockouts(username string) {
	loginGuard.Lock()
	defer loginGuard.Unlock()
	suffix := "|" + strings.ToLower(strings.TrimSpace(username))
	for key := range loginGuard.fails {
		if strings.HasSuffix(key, suffix) {
			delete(loginGuard.fails, key)
		}
	}
}

func pruneFails(ts []time.Time) []time.Time {
	var out []time.Time
	for _, t := range ts {
		if time.Since(t) < loginWindow {
			out = append(out, t)
		}
	}
	return out
}

// checkPassword enforces a minimum length. Returns an empty string when fine.
func checkPassword(p string) string {
	if len(p) < 8 {
		return "password must be at least 8 characters"
	}
	return ""
}

/* ---------- ffmpeg / ffprobe ---------- */

func hasFFmpeg() bool {
	_, err := exec.LookPath("ffmpeg")
	return err == nil
}

type probeInfo struct {
	VideoCodec, AudioCodec string
	Duration               float64
}

var probeCache sync.Map // "id:size" -> probeInfo

func probeFile(id int, path string, size int64) probeInfo {
	key := fmt.Sprintf("%d:%d", id, size)
	if v, ok := probeCache.Load(key); ok {
		return v.(probeInfo)
	}
	var info probeInfo
	if _, err := exec.LookPath("ffprobe"); err != nil {
		return info
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-print_format", "json",
		"-show_entries", "stream=codec_type,codec_name:format=duration", path).Output()
	if err != nil {
		return info
	}
	var p struct {
		Streams []struct {
			CodecType string `json:"codec_type"`
			CodecName string `json:"codec_name"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if json.Unmarshal(out, &p) != nil {
		return info
	}
	for _, st := range p.Streams {
		if st.CodecType == "video" && info.VideoCodec == "" {
			info.VideoCodec = st.CodecName
		}
		if st.CodecType == "audio" && info.AudioCodec == "" {
			info.AudioCodec = st.CodecName
		}
	}
	info.Duration, _ = strconv.ParseFloat(p.Format.Duration, 64)
	probeCache.Store(key, info)
	return info
}

// browserPlayable reports whether a normal browser can play the file without
// help. Unknown (no ffprobe) counts as playable so nothing is hidden.
func browserPlayable(ext string, info probeInfo) bool {
	ext = strings.ToLower(ext)
	switch ext {
	case ".avi", ".wmv", ".flv", ".mpg", ".mpeg", ".ts":
		return false
	case ".mp3", ".flac", ".wav", ".ogg", ".m4a", ".opus":
		// Audio containers are remuxed by the browser itself.
		return true
	case ".aac", ".wma":
		// ADTS/ASF audio usually needs conversion.
		return false
	}
	if info.VideoCodec == "" {
		return true
	}
	okV := map[string]bool{"h264": true, "vp8": true, "vp9": true, "av1": true}
	okA := map[string]bool{"": true, "aac": true, "mp3": true, "opus": true, "vorbis": true, "flac": true}
	return okV[info.VideoCodec] && okA[info.AudioCodec]
}

/* ---------- details ---------- */

// GET /api/details/{id}
func (s *Server) handleDetails(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/api/details/"))
	var d struct {
		id, year, season, episode, libID int
		title, path, ext, poster, over   string
		size                             int64
		rating                           float64
		libName                          string
		isUSB                            int
	}
	err := s.db.QueryRow(`SELECT m.id, m.title, COALESCE(m.year,0), COALESCE(m.season,0), COALESCE(m.episode,0),
		m.path, m.ext, m.size, m.poster, m.overview, m.rating, l.name, l.is_usb, m.library_id
		FROM media_items m JOIN libraries l ON l.id = m.library_id WHERE m.id = ?`, id).
		Scan(&d.id, &d.title, &d.year, &d.season, &d.episode, &d.path, &d.ext, &d.size,
			&d.poster, &d.over, &d.rating, &d.libName, &d.isUSB, &d.libID)
	if err != nil {
		jsonError(w, "not found", 404)
		return
	}
	info := probeFile(d.id, d.path, d.size)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id": d.id, "title": d.title, "year": d.year, "season": d.season, "episode": d.episode,
		"path": d.path, "ext": d.ext, "size": d.size, "overview": d.over, "rating": d.rating,
		"library": d.libName, "is_usb": d.isUSB == 1, "library_id": d.libID,
		"poster_id":       posterID(d.id, d.poster),
		"audio":           config.IsAudioExt(d.ext),
		"video_codec":     info.VideoCodec,
		"audio_codec":     info.AudioCodec,
		"duration":        info.Duration,
		"browser_ok":      browserPlayable(d.ext, info),
		"compat_possible": hasFFmpeg(),
		"subtitles":       subtitleList(d.id, d.path),
	})
}

/* ---------- subtitles ---------- */

type subFile struct{ Label, Path string }

// subtitleFiles finds sidecar subtitles: Movie.srt, Movie.vtt, Movie.en.srt…
func subtitleFiles(videoPath string) []subFile {
	dir := filepath.Dir(videoPath)
	base := strings.TrimSuffix(filepath.Base(videoPath), filepath.Ext(videoPath))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []subFile
	for _, e := range entries {
		name := e.Name()
		low := strings.ToLower(name)
		if e.IsDir() || !(strings.HasSuffix(low, ".srt") || strings.HasSuffix(low, ".vtt")) {
			continue
		}
		stem := strings.TrimSuffix(name, filepath.Ext(name))
		if stem != base && !strings.HasPrefix(stem, base+".") {
			continue
		}
		label := strings.TrimPrefix(strings.TrimPrefix(stem, base), ".")
		if label == "" {
			label = "Subtitles"
		}
		out = append(out, subFile{Label: strings.ToUpper(label), Path: filepath.Join(dir, name)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out
}

func subtitleList(id int, videoPath string) []map[string]interface{} {
	out := []map[string]interface{}{}
	for i, f := range subtitleFiles(videoPath) {
		out = append(out, map[string]interface{}{
			"label": f.Label, "src": fmt.Sprintf("/api/subtitle/%d/%d", id, i),
		})
	}
	return out
}

var srtTimeRe = regexp.MustCompile(`(\d{2}:\d{2}:\d{2}),(\d{3})`)

// GET /api/subtitle/{id}/{index} — serves a sidecar subtitle as WebVTT.
func (s *Server) handleSubtitle(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/subtitle/"), "/")
	if len(parts) != 2 {
		http.NotFound(w, r)
		return
	}
	id, _ := strconv.Atoi(parts[0])
	idx, _ := strconv.Atoi(parts[1])
	var path string
	if err := s.db.QueryRow("SELECT path FROM media_items WHERE id = ?", id).Scan(&path); err != nil {
		http.NotFound(w, r)
		return
	}
	subs := subtitleFiles(path)
	if idx < 0 || idx >= len(subs) {
		http.NotFound(w, r)
		return
	}
	data, err := os.ReadFile(subs[idx].Path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	text := strings.TrimPrefix(string(data), "\ufeff")
	if strings.HasSuffix(strings.ToLower(subs[idx].Path), ".srt") {
		text = "WEBVTT\n\n" + srtTimeRe.ReplaceAllString(text, "$1.$2")
	}
	w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
	w.Write([]byte(text))
}

/* ---------- compatibility stream (ffmpeg → HLS) ---------- */

type compatJob struct {
	cmd   *exec.Cmd
	dir   string
	timer *time.Timer
}

var compat = struct {
	sync.Mutex
	jobs map[int]*compatJob
}{jobs: map[int]*compatJob{}}

var compatFileRe = regexp.MustCompile(`^[a-zA-Z0-9_]+\.(m3u8|ts)$`)

func (s *Server) compatDir(id int) string {
	return filepath.Join(s.cfg.CacheDir, "compat", strconv.Itoa(id))
}

func stopCompat(id int) {
	compat.Lock()
	defer compat.Unlock()
	j, ok := compat.jobs[id]
	if !ok {
		return
	}
	if j.timer != nil {
		j.timer.Stop()
	}
	if j.cmd.Process != nil {
		j.cmd.Process.Kill()
		j.cmd.Wait()
	}
	os.RemoveAll(j.dir)
	delete(compat.jobs, id)
}

// /api/compat/{id} — POST starts a transcode for browsers that cannot play the
// file directly; DELETE stops it. The job stops itself after 20 idle minutes.
func (s *Server) handleCompat(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		jsonError(w, "unauthorized", 401)
		return
	}
	id, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/api/compat/"))
	switch r.Method {
	case "DELETE":
		stopCompat(id)
		w.WriteHeader(204)
		return
	case "POST":
	default:
		jsonError(w, "method not allowed", 405)
		return
	}
	if !hasFFmpeg() {
		jsonError(w, "ffmpeg is not installed on this machine", 501)
		return
	}
	var path string
	var size int64
	if err := s.db.QueryRow("SELECT path, size FROM media_items WHERE id = ?", id).Scan(&path, &size); err != nil {
		jsonError(w, "not found", 404)
		return
	}
	stopCompat(id)
	info := probeFile(id, path, size)
	dir := s.compatDir(id)
	os.MkdirAll(dir, 0o755)

	video := []string{"-c:v", "libx264",
		"-preset", firstNonEmpty(strings.TrimSpace(s.cfg.CompatPreset), "veryfast"),
		"-crf", "23", "-pix_fmt", "yuv420p"}
	// Scaling down keeps a slow machine usable; "0" keeps the source size.
	if h := strings.TrimSpace(s.cfg.CompatMaxHeight); h != "" && h != "0" {
		video = append(video, "-vf", "scale=-2:"+h)
	}
	if info.VideoCodec == "h264" && strings.TrimSpace(s.cfg.CompatMaxHeight) == "0" {
		video = []string{"-c:v", "copy"}
	}
	var args []string
	if config.IsAudioExt(filepath.Ext(path)) {
		// Audio-only job: no video stream to map or copy.
		args = []string{"-hide_banner", "-loglevel", "error", "-y", "-i", path,
			"-vn", "-c:a", "aac", "-b:a", "160k", "-ac", "2"}
	} else {
		args = []string{"-hide_banner", "-loglevel", "error", "-y", "-i", path,
			"-map", "0:v:0", "-map", "0:a:0?"}
		args = append(args, video...)
		args = append(args, "-c:a", "aac", "-b:a", "160k", "-ac", "2")
	}
	args = append(args,
		"-f", "hls", "-hls_time", "4", "-hls_list_size", "0", "-hls_playlist_type", "event",
		"-hls_segment_filename", filepath.Join(dir, "seg%05d.ts"),
		filepath.Join(dir, "index.m3u8"))
	cmd := exec.Command("ffmpeg", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		jsonError(w, "could not start ffmpeg: "+err.Error(), 500)
		return
	}
	job := &compatJob{cmd: cmd, dir: dir}
	job.timer = time.AfterFunc(20*time.Minute, func() { stopCompat(id) })
	compat.Lock()
	compat.jobs[id] = job
	compat.Unlock()

	go func() {
		err := cmd.Wait()
		compat.Lock()
		if _, ok := compat.jobs[id]; ok && err != nil {
			msg := strings.TrimSpace(lastLine(stderr.String()))
			log.Printf("compat stream for %d stopped: %v %s", id, err, msg)
		}
		compat.Unlock()
	}()
	for i := 0; i < 40; i++ {
		if _, err := os.Stat(filepath.Join(dir, "index.m3u8")); err == nil {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	w.WriteHeader(202)
	json.NewEncoder(w).Encode(map[string]string{
		"status": "starting", "url": "/api/compat/" + strconv.Itoa(id) + "/index.m3u8",
	})
}

// GET /api/compat/{id}/{file} — playlist and segments for a running job.
func (s *Server) handleCompatFile(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		jsonError(w, "unauthorized", 401)
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/compat/"), "/")
	if len(parts) != 2 || !compatFileRe.MatchString(parts[1]) {
		http.NotFound(w, r)
		return
	}
	id, _ := strconv.Atoi(parts[0])
	compat.Lock()
	if j, ok := compat.jobs[id]; ok && j.timer != nil {
		j.timer.Reset(20 * time.Minute)
	}
	compat.Unlock()
	file := filepath.Join(s.compatDir(id), parts[1])
	if strings.HasSuffix(parts[1], ".m3u8") {
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	} else {
		w.Header().Set("Content-Type", "video/mp2t")
	}
	w.Header().Set("Cache-Control", "no-store")
	http.ServeFile(w, r, file)
}

/* ---------- recently added ---------- */

// GET /api/recent — newest indexed items across every library.
func (s *Server) handleRecent(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(`SELECT m.id, m.title, COALESCE(m.year,0), COALESCE(m.season,0), COALESCE(m.episode,0),
		m.ext, m.size, m.poster, l.name, l.is_usb, m.library_id, m.added_at
		FROM media_items m JOIN libraries l ON l.id = m.library_id
		ORDER BY m.added_at DESC, m.id DESC LIMIT 24`)
	if err != nil {
		jsonError(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	out := []map[string]interface{}{}
	for rows.Next() {
		var id, year, season, episode, isUSB, libID int
		var title, ext, poster, lib, added string
		var size int64
		if rows.Scan(&id, &title, &year, &season, &episode, &ext, &size, &poster, &lib, &isUSB, &libID, &added) != nil {
			continue
		}
		out = append(out, map[string]interface{}{
			"type": "movie", "id": id, "title": title, "year": year, "season": season, "episode": episode,
			"ext": ext, "size": size, "library": lib, "is_usb": isUSB == 1, "library_id": libID,
			"added_at": added, "poster_id": posterID(id, poster), "audio": config.IsAudioExt(ext),
		})
	}
	json.NewEncoder(w).Encode(out)
}
