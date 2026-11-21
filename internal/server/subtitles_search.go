package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"darkserve/internal/auth"
)

const osSubAPI = "https://api.opensubtitles.com/api/v1"

// The API nests the interesting bits; these structs mirror the real shape.
type osSubFile struct {
	FileID   int    `json:"file_id"`
	FileName string `json:"file_name"`
}

type osSubItem struct {
	Attributes struct {
		Language       string `json:"language"`
		Release        string `json:"release"`
		Uploader       string `json:"uploader"`
		FeatureDetails struct {
			MovieName string `json:"movie_name"`
			Year      int    `json:"year"`
		} `json:"feature_details"`
		Files []osSubFile `json:"files"`
	} `json:"attributes"`
}

type osSubResp struct {
	Data []osSubItem `json:"data"`
}

// GET /api/subtitles/search?media_id=1&q=optional — looks for subtitles on
// OpenSubtitles for one indexed item.
func (s *Server) handleSubtitleSearch(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		jsonError(w, "unauthorized", 401)
		return
	}
	if !s.cfg.OSSubEnabled || s.cfg.OSSubKey == "" {
		jsonError(w, "turn on subtitle search and add an OpenSubtitles API key in Settings", 400)
		return
	}
	mediaID, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("media_id")))
	if mediaID <= 0 {
		jsonError(w, "media_id required", 400)
		return
	}
	var path, title, ext string
	if err := s.db.QueryRow("SELECT path, title, ext FROM media_items WHERE id = ?", mediaID).
		Scan(&path, &title, &ext); err != nil {
		jsonError(w, "not found", 404)
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		// Fall back to the filename minus extension, which usually carries
		// release info that helps matching.
		q = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	lang := strings.TrimSpace(s.cfg.OSSubLang)
	if lang == "" {
		lang = "en"
	}

	req, err := http.NewRequest("GET", osSubAPI+"/subtitles?"+subQuery(q, lang), nil)
	if err != nil {
		jsonError(w, err.Error(), 500)
		return
	}
	req.Header.Set("Api-Key", s.cfg.OSSubKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "DarkServe v1.0")
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		jsonError(w, "opensubtitles.com unreachable: "+err.Error(), 502)
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		jsonError(w, fmt.Sprintf("OpenSubtitles %d — check the API key in Settings", resp.StatusCode), 502)
		return
	}
	var out osSubResp
	if err := json.Unmarshal(body, &out); err != nil {
		jsonError(w, err.Error(), 500)
		return
	}
	items := make([]map[string]interface{}, 0, len(out.Data))
	for _, d := range out.Data {
		if len(d.Attributes.Files) == 0 {
			continue
		}
		items = append(items, map[string]interface{}{
			"file_id":   d.Attributes.Files[0].FileID,
			"file_name": d.Attributes.Files[0].FileName,
			"language":  d.Attributes.Language,
			"release":   d.Attributes.Release,
			"movie":     d.Attributes.FeatureDetails.MovieName,
			"year":      d.Attributes.FeatureDetails.Year,
		})
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"media_id": mediaID, "target": path, "results": items,
	})
}

func subQuery(q, lang string) string {
	v := url.Values{}
	v.Set("query", q)
	v.Set("languages", lang)
	return v.Encode()
}

// POST /api/subtitles/download {media_id, file_id, file_name} — downloads a
// subtitle beside the video so the player picks it up on the next scan.
func (s *Server) handleSubtitleDownload(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		jsonError(w, "unauthorized", 401)
		return
	}
	if !s.cfg.OSSubEnabled || s.cfg.OSSubKey == "" {
		jsonError(w, "add an OpenSubtitles API key in Settings first", 400)
		return
	}
	var req struct {
		MediaID  int    `json:"media_id"`
		FileID   int    `json:"file_id"`
		FileName string `json:"file_name"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	if req.MediaID <= 0 || req.FileID <= 0 {
		jsonError(w, "media_id and file_id required", 400)
		return
	}
	var videoPath string
	if err := s.db.QueryRow("SELECT path FROM media_items WHERE id = ?", req.MediaID).Scan(&videoPath); err != nil {
		jsonError(w, "not found", 404)
		return
	}
	link, err := s.osDownloadLink(req.FileID)
	if err != nil {
		jsonError(w, err.Error(), 502)
		return
	}
	if err := downloadTo(link, srtPathFor(videoPath)); err != nil {
		jsonError(w, "download failed: "+err.Error(), 502)
		return
	}
	json.NewEncoder(w).Encode(map[string]string{"status": "downloaded", "file": srtPathFor(videoPath)})
}

// osDownloadLink asks OpenSubtitles for a temporary download URL.
func (s *Server) osDownloadLink(fileID int) (string, error) {
	payload, _ := json.Marshal(map[string]int{"file_id": fileID})
	req, err := http.NewRequest("POST", osSubAPI+"/download", strings.NewReader(string(payload)))
	if err != nil {
		return "", err
	}
	req.Header.Set("Api-Key", s.cfg.OSSubKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "DarkServe v1.0")
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("OpenSubtitles %d on download", resp.StatusCode)
	}
	var out struct {
		Link string `json:"link"`
	}
	if err := json.Unmarshal(body, &out); err != nil || out.Link == "" {
		return "", fmt.Errorf("no download link returned")
	}
	return out.Link, nil
}

// srtPathFor names the sidecar after the video, e.g. Movie.mp4 → Movie.en.srt.
func srtPathFor(videoPath string) string {
	base := strings.TrimSuffix(videoPath, filepath.Ext(videoPath))
	lang := "en"
	return base + "." + lang + ".srt"
}

func downloadTo(url, dest string) error {
	resp, err := (&http.Client{Timeout: 60 * time.Second}).Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, io.LimitReader(resp.Body, 8<<20))
	return err
}
