package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"darkserve/internal/auth"
)

// handleContinueWatching returns the user's in-progress (uncompleted) items,
// newest activity first, so Home can render a resume rail.
func (s *Server) handleContinueWatching(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		jsonError(w, "unauthorized", 401)
		return
	}
	rows, err := s.db.Query(`
		SELECT m.id, m.title, m.path, m.ext, m.size,
		       COALESCE(m.year, 0), COALESCE(m.season, 0), COALESCE(m.episode, 0),
		       l.name, l.is_usb, m.library_id,
		       wp.position_seconds, wp.duration_seconds, m.poster
		FROM watch_progress wp
		JOIN media_items m ON m.id = wp.media_item_id
		JOIN libraries l ON l.id = m.library_id
		WHERE wp.user_id = ? AND wp.completed = 0 AND wp.position_seconds > 15
		ORDER BY wp.updated_at DESC
		LIMIT 16`, user.ID)
	if err != nil {
		jsonError(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	out := []map[string]interface{}{}
	for rows.Next() {
		var id int
		var title, path, ext, libName string
		var year, season, episode int
		var size int64
		var isUSB, libID int
		var pos, dur float64
		var poster string
		if rows.Scan(&id, &title, &path, &ext, &size, &year, &season, &episode,
			&libName, &isUSB, &libID, &pos, &dur, &poster) != nil {
			continue
		}
		pct := 0
		if dur > 0 {
			pct = int((pos / dur) * 100)
			if pct > 99 {
				pct = 99
			}
		}
		out = append(out, map[string]interface{}{
			"id":           id,
			"title":        title,
			"path":         path,
			"ext":          ext,
			"size":         size,
			"year":         year,
			"season":       season,
			"episode":      episode,
			"library":      libName,
			"is_usb":       isUSB == 1,
			"position":     pos,
			"duration":     dur,
			"progress_pct": pct,
			"poster_id":    posterID(id, poster),
		})
	}
	json.NewEncoder(w).Encode(out)
}

// handleMediaItemDelete removes an entry from the index (admin only).
// The underlying file on disk is left untouched.
func (s *Server) handleMediaItemDelete(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		jsonError(w, "unauthorized", 401)
		return
	}
	if r.Method != http.MethodDelete {
		jsonError(w, "method not allowed", 405)
		return
	}
	if user.Role != "admin" {
		jsonError(w, "admin required", 403)
		return
	}
	idStr := strings.TrimPrefix(r.URL.Path, "/api/media-item/")
	id, _ := strconv.Atoi(idStr)
	if id <= 0 {
		jsonError(w, "invalid id", 400)
		return
	}
	if _, err := s.db.Exec(`
		DELETE FROM watch_progress WHERE media_item_id = ?`, id); err != nil {
		jsonError(w, err.Error(), 500)
		return
	}
	if _, err := s.db.Exec(`DELETE FROM media_items WHERE id = ?`, id); err != nil {
		jsonError(w, err.Error(), 500)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
