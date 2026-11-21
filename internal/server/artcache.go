package server

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Local cache of online posters. Caching them keeps the UI fast, stops every
// card from reaching out to themoviedb.org, and means a Cast device can fetch
// art even when the metadata provider is slow or unreachable.
const artCacheTTL = 30 * 24 * time.Hour

var artFetch sync.Mutex

func (s *Server) artDir() string {
	return filepath.Join(s.cfg.CacheDir, "art")
}

func artCachePath(dir, url string) string {
	sum := sha256.Sum256([]byte(url))
	// .jpg so http.ServeFile reports image/jpeg without guessing from the data.
	return filepath.Join(dir, hex.EncodeToString(sum[:])+".jpg")
}

// GET /api/poster/{id} serves local sidecar art, the cached online poster, or
// (as a last resort) redirects to the provider. 404 means "draw a placeholder".
func (s *Server) handlePoster(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/api/poster/"))
	var poster string
	if err := s.db.QueryRow("SELECT poster FROM media_items WHERE id = ?", id).Scan(&poster); err != nil {
		http.NotFound(w, r)
		return
	}
	switch {
	case strings.HasPrefix(poster, "file:"):
		http.ServeFile(w, r, strings.TrimPrefix(poster, "file:"))
		return
	case strings.HasPrefix(poster, "https://"):
		s.serveCachedPoster(w, r, poster)
		return
	default:
		http.NotFound(w, r)
	}
}

// serveCachedPoster serves the poster from disk when it is there, downloads it
// once otherwise, and falls back to a redirect if anything goes wrong.
func (s *Server) serveCachedPoster(w http.ResponseWriter, r *http.Request, url string) {
	dir := s.artDir()
	path := artCachePath(dir, url)
	if data, err := os.ReadFile(path); err == nil {
		if time.Since(fileModTime(path)) < artCacheTTL {
			w.Header().Set("Content-Type", "image/jpeg")
			w.Header().Set("Cache-Control", "private, max-age=86400")
			w.Write(data)
			return
		}
	}
	if !s.downloadPoster(url, path) {
		http.Redirect(w, r, url, http.StatusFound)
		return
	}
	http.ServeFile(w, r, path)
}

// downloadPoster fetches a poster into the cache. Only one fetch per URL runs
// at a time so a grid of posters cannot stampede the provider.
func (s *Server) downloadPoster(url, path string) bool {
	artFetch.Lock()
	defer artFetch.Unlock()
	if _, err := os.Stat(path); err == nil {
		return true // another request fetched it already
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false
	}
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(url)
	if err != nil || resp.StatusCode != http.StatusOK {
		if resp != nil {
			resp.Body.Close()
		}
		return false
	}
	defer resp.Body.Close()
	tmp := path + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return false
	}
	_, err = io.Copy(f, io.LimitReader(resp.Body, 8<<20))
	f.Close()
	if err != nil {
		os.Remove(tmp)
		return false
	}
	return os.Rename(tmp, path) == nil
}

func fileModTime(path string) time.Time {
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}
