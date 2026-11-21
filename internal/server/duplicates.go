package server

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"darkserve/internal/auth"
)

// duplicateGroup is one normalised title that appears more than once.
type duplicateGroup struct {
	Title string
	Items []map[string]interface{}
}

// normTitle reduces a parsed title to a comparison key: lowercase, no
// punctuation, no "Full HD online MyFlixer" noise, no year.
func normTitle(t string) string {
	t = strings.ToLower(t)
	var b strings.Builder
	for _, r := range t {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ':
			b.WriteRune(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// GET /api/duplicates — titles that appear more than once in the index, largest
// first. Useful for spotting the same film saved twice under different names.
func (s *Server) handleDuplicates(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		jsonError(w, "unauthorized", 401)
		return
	}
	rows, err := s.db.Query(`
		SELECT m.id, m.title, m.path, m.ext, m.size, COALESCE(m.year,0),
		       l.name, l.is_usb, m.library_id
		FROM media_items m JOIN libraries l ON l.id = m.library_id
		WHERE m.library_id IN (SELECT id FROM libraries)
		  -- Episodes of one series share a normalised title by design.
		  AND COALESCE(m.season,0) = 0 AND COALESCE(m.episode,0) = 0`)
	if err != nil {
		jsonError(w, err.Error(), 500)
		return
	}
	groups := map[string][]map[string]interface{}{}
	for rows.Next() {
		var id, libID, year, isUSB int
		var title, path, ext, libName string
		var size int64
		if rows.Scan(&id, &title, &path, &ext, &size, &year, &libName, &isUSB, &libID) != nil {
			continue
		}
		key := normTitle(title)
		if key == "" {
			continue
		}
		groups[key] = append(groups[key], map[string]interface{}{
			"id": id, "title": title, "path": path, "ext": ext, "size": size,
			"year": year, "library": libName, "is_usb": isUSB == 1, "library_id": libID,
		})
	}
	rows.Close()

	var out []duplicateGroup
	for _, items := range groups {
		if len(items) < 2 {
			continue
		}
		sort.Slice(items, func(i, j int) bool {
			si, _ := items[i]["size"].(int64)
			sj, _ := items[j]["size"].(int64)
			return si > sj
		})
		display := items[0]["title"].(string)
		out = append(out, duplicateGroup{Title: display, Items: items})
	}
	// Most copies first.
	sort.Slice(out, func(i, j int) bool { return len(out[i].Items) > len(out[j].Items) })

	payload := make([]map[string]interface{}, 0, len(out))
	for _, g := range out {
		payload = append(payload, map[string]interface{}{
			"title": g.Title, "copies": len(g.Items), "items": g.Items,
		})
	}
	json.NewEncoder(w).Encode(payload)
}
