package server

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"sort"

	"darkserve/internal/auth"
	"darkserve/internal/config"
)

// GET /api/ondeck — for each series, the first episode that is not finished,
// newest-touched series first. This is what makes a library feel like a TV.
func (s *Server) handleOnDeck(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		jsonError(w, "unauthorized", 401)
		return
	}
	rows, err := s.db.Query(`
		SELECT m.id, m.title, m.path, m.ext, m.poster,
		       COALESCE(m.season,0), COALESCE(m.episode,0),
		       l.name, l.is_usb, m.library_id,
		       COALESCE((SELECT wp.completed FROM watch_progress wp
		                  WHERE wp.media_item_id = m.id AND wp.user_id = ?), 0),
		       COALESCE((SELECT MAX(wp.updated_at) FROM watch_progress wp
		                  WHERE wp.media_item_id = m.id AND wp.user_id = ?), '')
		FROM media_items m JOIN libraries l ON l.id = m.library_id
		WHERE COALESCE(m.season,0) > 0 OR COALESCE(m.episode,0) > 0`, user.ID, user.ID)
	if err != nil {
		jsonError(w, err.Error(), 500)
		return
	}

	type ep struct {
		id, season, episode, libID        int
		title, path, ext, libName, poster string
		isUSB                             int
		completed                         int
		lastWatch                         string
	}
	shows := map[string][]ep{}
	lastTouch := map[string]string{}
	for rows.Next() {
		var e ep
		if rows.Scan(&e.id, &e.title, &e.path, &e.ext, &e.poster,
			&e.season, &e.episode, &e.libName, &e.isUSB, &e.libID,
			&e.completed, &e.lastWatch) != nil {
			continue
		}
		// The series is the show folder; season folders are lifted up to it.
		dir := showFolderOf(e.path)
		shows[dir] = append(shows[dir], e)
		if e.lastWatch > lastTouch[dir] {
			lastTouch[dir] = e.lastWatch
		}
	}
	rows.Close()

	type showOut struct {
		ep        ep
		dir       string
		remaining int
	}
	var list []showOut
	for dir, eps := range shows {
		sort.Slice(eps, func(i, j int) bool {
			if eps[i].season != eps[j].season {
				return eps[i].season < eps[j].season
			}
			if eps[i].episode != eps[j].episode {
				return eps[i].episode < eps[j].episode
			}
			return eps[i].path < eps[j].path
		})
		remaining := 0
		for _, e := range eps {
			if e.completed == 0 {
				remaining++
			}
		}
		if remaining == 0 {
			continue // fully caught up
		}
		var next ep
		for _, e := range eps {
			if e.completed == 0 {
				next = e
				break
			}
		}
		list = append(list, showOut{ep: next, dir: dir, remaining: remaining})
	}
	// Most recently touched series first, then alphabetical.
	sort.Slice(list, func(i, j int) bool {
		if lastTouch[list[i].dir] != lastTouch[list[j].dir] {
			return lastTouch[list[i].dir] > lastTouch[list[j].dir]
		}
		return list[i].dir < list[j].dir
	})

	out := make([]map[string]interface{}, 0, len(list))
	for _, s := range list {
		e := s.ep
		showName := filepath.Base(s.dir)
		out = append(out, map[string]interface{}{
			"id": e.id, "title": e.title, "path": e.path, "ext": e.ext,
			"poster_id": posterID(e.id, e.poster), "audio": config.IsAudioExt(e.ext),
			"season": e.season, "episode": e.episode,
			"library": e.libName, "is_usb": e.isUSB == 1, "library_id": e.libID,
			"show": showName, "remaining": s.remaining,
		})
	}
	json.NewEncoder(w).Encode(out)
}
