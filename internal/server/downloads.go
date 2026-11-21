package server

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"darkserve/internal/auth"
	"darkserve/internal/ytdlp"
)

// /api/ytdlp — GET status (any signed-in user), POST a new download.
func (s *Server) handleYTDLP(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		jsonError(w, "unauthorized", 401)
		return
	}
	if r.Method == "GET" {
		// Report availability so the UI can prompt an install without a download attempt.
		f := s.ytdlpFinder()
		json.NewEncoder(w).Encode(map[string]interface{}{
			"available":       f.Locate() != "",
			"version":         f.Version(),
			"configured_path": strings.TrimSpace(s.cfg.YTDLPPath),
		})
		return
	}
	if r.Method != "POST" {
		jsonError(w, "method not allowed", 405)
		return
	}
	var req struct {
		URL       string `json:"url"`
		Library   int    `json:"library"`
		Kind      string `json:"kind"`      // video | audio
		Quality   string `json:"quality"`   // best | 2160 | 1440 | 1080 | 720 | 480
		AudioFmt  string `json:"audio_fmt"` // mp3 | m4a | opus | flac | wav
		Bitrate   string `json:"bitrate"`   // 0 (best) | 320 | 192 | 128
		Subtitles bool   `json:"subtitles"`
		Playlist  bool   `json:"playlist"`
		Subdir    string `json:"subdir"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	req.URL = strings.TrimSpace(req.URL)
	if req.URL == "" {
		jsonError(w, "a URL is required", 400)
		return
	}
	if !strings.HasPrefix(req.URL, "http://") && !strings.HasPrefix(req.URL, "https://") {
		jsonError(w, "URL must start with http:// or https://", 400)
		return
	}
	var libPath string
	if err := s.db.QueryRow("SELECT path FROM libraries WHERE id = ?", req.Library).Scan(&libPath); err != nil {
		jsonError(w, "pick a destination library", 400)
		return
	}
	if !dirWritable(libPath) {
		jsonError(w, "that library is not writable — is the drive still connected?", 409)
		return
	}

	// Downloads land inside the library so the scanner indexes them, either at
	// the root or in a folder the user named.
	destDir := libPath
	if sub := strings.TrimSpace(req.Subdir); sub != "" {
		destDir = filepath.Join(libPath, sanitizePath(sub))
		if err := ensureDir(destDir); err != nil {
			jsonError(w, "could not create folder: "+err.Error(), 500)
			return
		}
	}
	if !dirWritable(destDir) {
		jsonError(w, "destination folder is not writable", 409)
		return
	}

	f := ytdlp.Format{Subtitles: req.Subtitles, Playlist: req.Playlist}
	if req.Kind == "audio" {
		f.Kind = "audio"
		f.AudioContainer = firstNonEmpty(req.AudioFmt, "mp3")
		f.Quality = firstNonEmpty(req.Bitrate, "0")
	} else {
		f.Kind = "video"
		f.Quality = firstNonEmpty(req.Quality, "best")
	}

	job, err := s.downloads.Start(req.URL, destDir, f, req.Library, s.afterDownload)
	if err != nil {
		jsonError(w, err.Error(), 400)
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"id": job.ID, "status": job.Status})
}

// /api/ytdlp/jobs — the download list, also accepts ?id= for one job.
func (s *Server) handleYTDLPJobs(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		jsonError(w, "unauthorized", 401)
		return
	}
	if idStr := r.URL.Query().Get("id"); idStr != "" {
		id, _ := strconv.Atoi(idStr)
		j := s.downloads.Job(id)
		if j == nil {
			jsonError(w, "not found", 404)
			return
		}
		if r.Method == "DELETE" {
			if !s.downloads.Cancel(id) {
				jsonError(w, "that job is not running", 409)
				return
			}
			w.WriteHeader(204)
			return
		}
		json.NewEncoder(w).Encode(jobPayload(j))
		return
	}
	out := []map[string]interface{}{}
	for _, j := range s.downloads.Jobs() {
		out = append(out, jobPayload(j))
	}
	json.NewEncoder(w).Encode(out)
}

// /api/ytdlp/cancel/{id} stops a running download.
func (s *Server) handleYTDLPCancel(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		jsonError(w, "unauthorized", 401)
		return
	}
	id, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/api/ytdlp/cancel/"))
	if !s.downloads.Cancel(id) {
		jsonError(w, "that job is not running", 409)
		return
	}
	w.WriteHeader(204)
}

// afterDownload re-indexes the destination library so a finished download
// shows up in the player without waiting for the next scheduled scan.
func (s *Server) afterDownload(j *ytdlp.Job) {
	if j.Library <= 0 || j.Status == "cancelled" {
		return
	}
	go s.scanner.ScanLibraryForce(context.Background(), j.Library)
}

func (s *Server) ytdlpFinder() ytdlp.Finder {
	return ytdlp.Finder{Path: s.cfg.YTDLPPath}
}

func jobPayload(j *ytdlp.Job) map[string]interface{} {
	return j.Snapshot()
}
