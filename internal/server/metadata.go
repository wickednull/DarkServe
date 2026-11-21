package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"darkserve/internal/auth"
)

// GET /api/metadata/search?media_id=1&q=Cyberpunk — searches The Movie
// Database for a better title or poster for one indexed item.
func (s *Server) handleMetadataSearch(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil || user.Role != "admin" {
		jsonError(w, "admin required", 403)
		return
	}
	if s.cfg.TMDBKey == "" {
		jsonError(w, "add a TMDB key in Settings → Artwork first", 400)
		return
	}
	mediaID, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("media_id")))
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if mediaID <= 0 || q == "" {
		jsonError(w, "media_id and q are required", 400)
		return
	}
	var season, episode *int
	if err := s.db.QueryRow("SELECT season, episode FROM media_items WHERE id = ?", mediaID).
		Scan(&season, &episode); err != nil {
		jsonError(w, "not found", 404)
		return
	}
	kind := "movie"
	if (season != nil && *season > 0) || (episode != nil && *episode > 0) {
		kind = "tv"
	}

	query := url.Values{}
	query.Set("query", cleanTitle(q))
	req, err := http.NewRequest("GET", tmdbAPI+kind+"?"+query.Encode(), nil)
	if err != nil {
		jsonError(w, err.Error(), 500)
		return
	}
	if strings.HasPrefix(s.cfg.TMDBKey, "eyJ") {
		req.Header.Set("Authorization", "Bearer "+s.cfg.TMDBKey)
	} else {
		query.Set("api_key", s.cfg.TMDBKey)
		req.URL.RawQuery = query.Encode()
	}
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		jsonError(w, "themovedb.org unreachable: "+err.Error(), 502)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		jsonError(w, fmt.Sprintf("TMDB %d — check the API key in Settings", resp.StatusCode), 502)
		return
	}
	var out struct {
		Results []struct {
			ID          int     `json:"id"`
			Title       string  `json:"title"`
			Name        string  `json:"name"`
			Year        string  `json:"release_date"`
			FirstAir    string  `json:"first_air_date"`
			PosterPath  string  `json:"poster_path"`
			Overview    string  `json:"overview"`
			VoteAverage float64 `json:"vote_average"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		jsonError(w, err.Error(), 500)
		return
	}
	items := make([]map[string]interface{}, 0, 12)
	for i, r := range out.Results {
		if i >= 12 {
			break
		}
		title := r.Title
		if title == "" {
			title = r.Name
		}
		date := r.Year
		if date == "" {
			date = r.FirstAir
		}
		year, _ := strconv.Atoi(strings.SplitN(date, "-", 2)[0])
		posterURL := ""
		if r.PosterPath != "" {
			posterURL = tmdbPosterURL + r.PosterPath
		}
		items = append(items, map[string]interface{}{
			"tmdb_id": r.ID, "title": title, "year": year,
			"poster": r.PosterPath, "overview": r.Overview, "rating": r.VoteAverage,
			"poster_url": posterURL,
		})
	}
	// A preview URL keeps the UI from needing the TMDB key.
	json.NewEncoder(w).Encode(map[string]interface{}{"kind": kind, "results": items})
}

// POST /api/metadata/apply {media_id, title, overview, rating, poster_url} —
// stores a chosen match for one item, so a badly named file still gets good
// art and a synopsis.
func (s *Server) handleMetadataApply(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil || user.Role != "admin" {
		jsonError(w, "admin required", 403)
		return
	}
	var req struct {
		MediaID  int     `json:"media_id"`
		Title    string  `json:"title"`
		Overview string  `json:"overview"`
		Rating   float64 `json:"rating"`
		Poster   string  `json:"poster_url"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	if req.MediaID <= 0 {
		jsonError(w, "media_id required", 400)
		return
	}
	// A bare TMDB path is expanded; anything else is stored as given.
	poster := ""
	if p := strings.TrimSpace(req.Poster); p != "" && p != "null" {
		if strings.HasPrefix(p, "/") {
			p = tmdbPosterURL + p
		}
		if strings.HasPrefix(p, "https://") {
			poster = p
		}
	}
	var year *int
	s.db.QueryRow("SELECT year FROM media_items WHERE id = ?", req.MediaID).Scan(&year)
	if _, err := s.db.Exec(`UPDATE media_items SET poster = ?, overview = ?, rating = ? WHERE id = ?`,
		poster, strings.TrimSpace(req.Overview), req.Rating, req.MediaID); err != nil {
		jsonError(w, err.Error(), 500)
		return
	}
	// Only rename when the admin asked for it and there is a title to use.
	if t := strings.TrimSpace(req.Title); t != "" {
		s.db.Exec("UPDATE media_items SET title = ? WHERE id = ?", t, req.MediaID)
	}
	w.WriteHeader(204)
}
