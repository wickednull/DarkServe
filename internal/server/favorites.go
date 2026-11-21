package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"darkserve/internal/auth"
	"darkserve/internal/config"
)

// GET /api/favorites — the signed-in user's starred items.
// POST /api/favorites/{id} — star one.
// DELETE /api/favorites/{id} — unstar one.
func (s *Server) handleFavorites(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		jsonError(w, "unauthorized", 401)
		return
	}
	switch r.Method {
	case "GET":
		rows, err := s.db.Query(`
			SELECT m.id, m.title, m.path, m.ext, m.size, COALESCE(m.year,0),
			       COALESCE(m.season,0), COALESCE(m.episode,0),
			       m.poster, l.name, l.is_usb, m.library_id, m.added_at
			FROM favorites f
			JOIN media_items m ON m.id = f.media_item_id
			JOIN libraries l ON l.id = m.library_id
			WHERE f.user_id = ?
			ORDER BY f.created_at DESC, m.id DESC LIMIT 60`, user.ID)
		if err != nil {
			jsonError(w, err.Error(), 500)
			return
		}
		defer rows.Close()
		out := []map[string]interface{}{}
		for rows.Next() {
			var id, libID, year, season, episode, isUSB int
			var title, path, ext, poster, lib, added string
			var size int64
			if rows.Scan(&id, &title, &path, &ext, &size, &year, &season, &episode,
				&poster, &lib, &isUSB, &libID, &added) != nil {
				continue
			}
			out = append(out, map[string]interface{}{
				"type": "movie", "id": id, "title": title, "path": path, "ext": ext,
				"size": size, "year": year, "season": season, "episode": episode,
				"library": lib, "is_usb": isUSB == 1, "library_id": libID,
				"added_at": added, "poster_id": posterID(id, poster),
				"audio": config.IsAudioExt(ext), "progress_pct": 0,
			})
		}
		json.NewEncoder(w).Encode(out)
		return

	case "POST", "DELETE":
		id, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/api/favorites/"))
		if id <= 0 {
			jsonError(w, "invalid id", 400)
			return
		}
		// Only index items that exist, so a stale favourite cannot dangle.
		var count int
		s.db.QueryRow("SELECT COUNT(*) FROM media_items WHERE id = ?", id).Scan(&count)
		if count == 0 {
			jsonError(w, "not found", 404)
			return
		}
		if r.Method == "POST" {
			if _, err := s.db.Exec(
				"INSERT INTO favorites(user_id, media_item_id, created_at) VALUES(?,?,?)",
				user.ID, id, now()); err != nil {
				// A repeat star is a no-op, not an error.
				w.WriteHeader(200)
				json.NewEncoder(w).Encode(map[string]string{"status": "already"})
				return
			}
			w.WriteHeader(201)
			json.NewEncoder(w).Encode(map[string]string{"status": "added"})
			return
		}
		s.db.Exec("DELETE FROM favorites WHERE user_id = ? AND media_item_id = ?", user.ID, id)
		w.WriteHeader(204)
		return
	}
	jsonError(w, "method not allowed", 405)
}
