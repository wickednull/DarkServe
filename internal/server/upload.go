package server

import (
	"darkserve/internal/config"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"darkserve/internal/auth"
	"darkserve/internal/iptv"
	"darkserve/internal/scanner"
)

// ---------------------------------------------------------------------------
// Upload — streams multipart file bodies into the selected library folder.
// ---------------------------------------------------------------------------

const maxUploadBytes = 512 << 30 // 512 GiB safety ceiling

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		jsonError(w, "method not allowed", 405)
		return
	}
	mr, err := r.MultipartReader()
	if err != nil {
		jsonError(w, "multipart form required", 400)
		return
	}

	// Fields must precede the file part (the UI sends them in this order).
	libraryID := 0
	var dstPath string
	var isUSB int
	for {
		part, perr := mr.NextPart()
		if perr == io.EOF {
			break
		}
		if perr != nil {
			jsonError(w, perr.Error(), 400)
			return
		}
		if part.FormName() == "library" {
			b := make([]byte, 0, 64)
			tmp := make([]byte, 64)
			n, _ := part.Read(tmp)
			b = append(b, tmp[:n]...)
			libraryID, _ = strconv.Atoi(strings.TrimSpace(string(b)))
		}
		part.Close()
		if libraryID > 0 {
			err = s.db.QueryRow("SELECT path, is_usb FROM libraries WHERE id = ?", libraryID).
				Scan(&dstPath, &isUSB)
			if err != nil {
				jsonError(w, "library not found", 404)
				return
			}
			break
		}
	}
	if libraryID <= 0 {
		jsonError(w, "choose a library to upload into", 400)
		return
	}

	var lastItem map[string]interface{}
	uploaded := 0
	for {
		part, perr := mr.NextPart()
		if perr == io.EOF {
			break
		}
		if perr != nil {
			jsonError(w, perr.Error(), 400)
			return
		}
		defer part.Close()
		if part.FormName() != "file" || part.FileName() == "" {
			io.Copy(io.Discard, part)
			part.Close()
			continue
		}
		item, uerr := s.saveUpload(dstPath, libraryID, part)
		part.Close()
		if uerr != nil {
			jsonError(w, uerr.Error(), 400)
			return
		}
		lastItem = item
		uploaded++
	}
	if uploaded == 0 {
		jsonError(w, "no file part found", 400)
		return
	}
	dest := "saved to server folder"
	if isUSB == 1 {
		dest = "saved to USB drive"
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "uploaded", "dest": dest, "files": uploaded, "item": lastItem,
	})
}

// saveUpload streams a single part into libPath. Validation happens on the
// first 16 bytes so broken files are rejected before consuming the transfer.
func (s *Server) saveUpload(dstDir string, libraryID int, part *multipart.Part) (map[string]interface{}, error) {
	orig := filepath.Base(strings.TrimSpace(part.FileName()))
	if orig == "" || orig == "." || orig == "/" {
		return nil, fmt.Errorf("invalid filename")
	}
	ext := strings.ToLower(filepath.Ext(orig))
	if !s.isMediaExt(ext) {
		return nil, fmt.Errorf("%s is not an accepted media format", ext)
	}
	base := strings.TrimSuffix(orig, ext)

	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return nil, fmt.Errorf("destination unreachable: %v", err)
	}

	// Deduplicate: name, name (2), name (3)…
	dst := filepath.Join(dstDir, orig)
	for i := 2; ; i++ {
		if _, err := os.Stat(dst); os.IsNotExist(err) {
			break
		}
		dst = filepath.Join(dstDir, fmt.Sprintf("%s (%d)%s", base, i, ext))
	}

	tmp := dst + ".part"
	out, err := os.Create(tmp)
	if err != nil {
		return nil, fmt.Errorf("cannot create file on destination: %v", err)
	}
	defer out.Close()

	head := make([]byte, 16)
	n, _ := io.ReadFull(part, head)
	head = head[:n]
	if n < 12 {
		os.Remove(tmp)
		return nil, fmt.Errorf("file too small to be video")
	}
	if !scanner.HeaderOK(ext, head) {
		os.Remove(tmp)
		return nil, fmt.Errorf("not a recognized video file (magic mismatch)")
	}
	if _, err := out.Write(head); err != nil {
		os.Remove(tmp)
		return nil, err
	}

	r := io.TeeReader(part, out)
	var written int64 = int64(n)
	for {
		buf := make([]byte, 256<<10)
		m, rerr := r.Read(buf)
		if m > 0 {
			// migrate to written cap: refuse monsters to avoid filling disk
			if written+int64(m) > maxUploadBytes {
				out.Close()
				os.Remove(tmp)
				return nil, fmt.Errorf("upload exceeds %d GB ceiling", maxUploadBytes>>30)
			}
			if _, werr := out.Write(buf[:m]); werr != nil {
				os.Remove(tmp)
				return nil, werr
			}
			written += int64(m)
		}
		if rerr != nil {
			if rerr == io.EOF {
				break
			}
			os.Remove(tmp)
			return nil, rerr
		}
	}
	if err := out.Sync(); err != nil {
		os.Remove(tmp)
		return nil, err
	}
	if written < int64(1024*1024) && !config.IsAudioExt(ext) {
		os.Remove(tmp)
		return nil, fmt.Errorf("upload too small (<1MB)")
	}

	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return nil, err
	}

	meta := scanner.MetadataFor(dst)
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.db.Exec(`INSERT INTO media_items
		(library_id, title, original_title, year, season, episode, path, ext, size, added_at)
		VALUES(?,?,?,?,?,?,?,?,?,?)`,
		libraryID, meta.Title, meta.OriginalTitle, meta.Year, meta.Season, meta.Episode,
		dst, ext, written, now)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return map[string]interface{}{
		"id": id, "title": meta.Title, "path": dst, "ext": ext, "size": written,
		"year": meta.Year, "season": meta.Season, "episode": meta.Episode,
	}, nil
}

func (s *Server) isMediaExt(ext string) bool {
	for _, e := range s.cfg.MediaExts {
		if e == ext {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// IPTV — playlists + channels (The-Crew-style live TV, fully native)
// ---------------------------------------------------------------------------

func (s *Server) handleIPTVLists(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		jsonError(w, "unauthorized", 401)
		return
	}
	if r.Method == "GET" {
		rows, err := s.db.Query(`
			SELECT l.id, l.name, l.source_type, l.source, l.updated_at,
			       (SELECT COUNT(*) FROM iptv_channels c WHERE c.list_id = l.id)
			FROM iptv_lists l ORDER BY l.id`)
		if err != nil {
			jsonError(w, err.Error(), 500)
			return
		}
		defer rows.Close()
		var lists []map[string]interface{}
		for rows.Next() {
			var id int
			var name, stype, source, updated string
			var channels int
			rows.Scan(&id, &name, &stype, &source, &updated, &channels)
			lists = append(lists, map[string]interface{}{
				"id": id, "name": name, "type": stype,
				"source": source, "channels": channels, "updated": updated,
			})
		}
		if lists == nil {
			lists = []map[string]interface{}{}
		}
		json.NewEncoder(w).Encode(lists)
		return
	}
	if r.Method == "POST" {
		if user.Role != "admin" {
			jsonError(w, "admin required", 403)
			return
		}
		var req struct {
			Name       string `json:"name"`
			SourceType string `json:"type"`   // url | paste
			Source     string `json:"source"` // URL when url; raw text when paste
		}
		json.NewDecoder(r.Body).Decode(&req)
		if strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.Source) == "" {
			jsonError(w, "name and source required", 400)
			return
		}
		listID, err := s.upsertIPTVList(req.Name, req.SourceType, req.Source)
		if err != nil {
			jsonError(w, err.Error(), 500)
			return
		}
		count, err := s.refreshIPTVList(listID, req.SourceType, req.Source)
		if err != nil {
			jsonError(w, "list saved but fetch failed: "+err.Error(), 502)
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"id": listID, "channels": count,
		})
		return
	}
	jsonError(w, "method not allowed", 405)
}

func (s *Server) upsertIPTVList(name, stype, source string) (int64, error) {
	if stype != "paste" {
		stype = "url"
	}
	res, err := s.db.Exec(`INSERT INTO iptv_lists(name, source_type, source, updated_at) VALUES(?,?,?,?)`,
		name, stype, source, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Server) handleIPTVListByID(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		jsonError(w, "unauthorized", 401)
		return
	}
	idStr := strings.TrimPrefix(r.URL.Path, "/api/iptv/lists/")
	id, _ := strconv.Atoi(idStr)
	switch r.Method {
	case "DELETE":
		if user.Role != "admin" {
			jsonError(w, "admin required", 403)
			return
		}
		// Foreign-key cascades are not enabled, so clear channels explicitly.
		s.db.Exec("DELETE FROM iptv_channels WHERE list_id = ?", id)
		s.db.Exec("DELETE FROM iptv_lists WHERE id = ?", id)
		w.WriteHeader(204)

	case "POST": // refresh
		var name, stype, source string
		if err := s.db.QueryRow(`SELECT name, source_type, source FROM iptv_lists WHERE id = ?`, id).
			Scan(&name, &stype, &source); err != nil {
			jsonError(w, "list not found", 404)
			return
		}
		count, err := s.refreshIPTVList(int64(id), stype, source)
		if err != nil {
			jsonError(w, err.Error(), 502)
			return
		}
		json.NewEncoder(w).Encode(map[string]int{"channels": count})

	default:
		jsonError(w, "method not allowed", 405)
	}
}

func (s *Server) refreshIPTVList(id int64, stype, source string) (int, error) {
	var content string
	var err error
	if stype == "paste" {
		content = source
	} else {
		content, err = iptv.Fetch(source, s.cfg.IPTVUserAgent, time.Duration(s.cfg.IPTVTimeoutSec)*time.Second)
		if err != nil {
			return 0, err
		}
	}
	pl := iptv.Parse(content)
	if len(pl.Channels) == 0 {
		return 0, fmt.Errorf("no channels parsed from playlist")
	}
	if max := s.cfg.IPTVMaxChannels; max > 0 && len(pl.Channels) > max {
		pl.Channels = pl.Channels[:max]
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	if _, err := tx.Exec("DELETE FROM iptv_channels WHERE list_id = ?", id); err != nil {
		tx.Rollback()
		return 0, err
	}
	stmt, err := tx.Prepare(`INSERT OR IGNORE INTO iptv_channels(list_id, name, group_name, logo, url)
		VALUES(?,?,?,?,?)`)
	if err != nil {
		tx.Rollback()
		return 0, err
	}
	defer stmt.Close()
	inserted := 0
	for _, ch := range pl.Channels {
		if strings.TrimSpace(ch.URL) == "" {
			continue
		}
		if _, err := stmt.Exec(id, ch.Name, ch.Group, ch.Logo, ch.URL); err == nil {
			inserted++
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	s.db.Exec("UPDATE iptv_lists SET updated_at = ? WHERE id = ?", time.Now().UTC().Format(time.RFC3339), id)
	return inserted, nil
}

func (s *Server) handleIPTVChannels(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		jsonError(w, "unauthorized", 401)
		return
	}
	listID, _ := strconv.Atoi(r.URL.Query().Get("list"))
	if listID <= 0 {
		json.NewEncoder(w).Encode([]map[string]interface{}{})
		return
	}
	group := strings.TrimSpace(r.URL.Query().Get("group"))
	q := strings.TrimSpace(r.URL.Query().Get("q"))

	args := []interface{}{listID}
	where := "list_id = ?"
	if group != "" {
		where += " AND group_name = ?"
		args = append(args, group)
	}
	if q != "" {
		where += " AND name LIKE ?"
		args = append(args, "%"+q+"%")
	}
	limit := 2000
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 && v <= 20000 {
		limit = v
	}

	rows, err := s.db.Query(`SELECT id, name, group_name, logo, url FROM iptv_channels WHERE `+where+`
		ORDER BY name LIMIT `+strconv.Itoa(limit), args...)
	if err != nil {
		jsonError(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	var out []map[string]interface{}
	for rows.Next() {
		var id int
		var name, gname, logo, url string
		rows.Scan(&id, &name, &gname, &logo, &url)
		out = append(out, map[string]interface{}{
			"id": id, "name": name, "group": gname, "logo": logo, "url": url,
		})
	}
	if out == nil {
		out = []map[string]interface{}{}
	}
	json.NewEncoder(w).Encode(out)
}
