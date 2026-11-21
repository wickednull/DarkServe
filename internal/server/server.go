package server

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"darkserve/internal/auth"
	"darkserve/internal/config"
	"darkserve/internal/db"
	"darkserve/internal/scanner"
	"darkserve/internal/usb"
	"darkserve/internal/ytdlp"
	"darkserve/web"
)

type Server struct {
	cfg       *config.Config
	db        *db.DB
	scanner   *scanner.Scanner
	downloads *ytdlp.Downloader
	detector  *usb.Detector
	auth      *auth.Manager
	tmpl      *template.Template
	staticFS  fs.FS

	// shutdownHook asks the process to stop. It is set by main and used by
	// the "Shut down server" button in the settings page.
	shutdownHook func()
}

// SetShutdownHook registers the callback that stops the server.
func (s *Server) SetShutdownHook(fn func()) { s.shutdownHook = fn }

func New(cfg *config.Config, database *db.DB, scanner *scanner.Scanner, detector *usb.Detector, auth *auth.Manager) *Server {
	tplFS, err := fs.Sub(web.FS, "templates")
	if err != nil {
		log.Fatalf("templates: %v", err)
	}
	tmpl := template.Must(template.ParseFS(tplFS, "*.html"))
	staticFS, err := fs.Sub(web.FS, "static")
	if err != nil {
		log.Fatalf("static: %v", err)
	}
	return &Server{cfg: cfg, db: database, scanner: scanner, detector: detector, auth: auth, tmpl: tmpl, staticFS: staticFS,
		downloads: ytdlp.New(ytdlp.Finder{Path: cfg.YTDLPPath})}
}

func (s *Server) Router() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/login", s.handleLogin)
	mux.HandleFunc("/api/login", s.handleAPILogin)
	mux.HandleFunc("/api/register", s.handleAPIRegister)
	mux.HandleFunc("/api/logout", s.handleAPILogout)
	mux.HandleFunc("/api/drives", s.handleDrives)
	mux.HandleFunc("/api/drives/unmounted", s.handleUnmountedDrives)
	mux.HandleFunc("/api/drives/mount", s.handleMountDrive)
	mux.HandleFunc("/api/libraries", s.handleLibraries)
	mux.HandleFunc("/api/libraries/", s.handleLibrary)
	mux.HandleFunc("/api/media", s.handleMedia)
	mux.HandleFunc("/api/media/", s.handleMediaItem)
	mux.HandleFunc("/api/upload", s.handleUpload)
	mux.HandleFunc("/api/iptv/lists", s.handleIPTVLists)
	mux.HandleFunc("/api/iptv/lists/", s.handleIPTVListByID)
	mux.HandleFunc("/api/iptv/channels", s.handleIPTVChannels)
	mux.HandleFunc("/api/me", s.handleMe)
	mux.HandleFunc("/api/continue", s.handleContinueWatching)
	mux.HandleFunc("/api/media-item/", s.handleMediaItemDelete)
	mux.HandleFunc("/api/search", s.handleSearch)
	mux.HandleFunc("/api/scan", s.handleScan)
	mux.HandleFunc("/api/stream/", s.handleStream)
	mux.HandleFunc("/api/progress/", s.handleProgress)
	mux.HandleFunc("/api/settings", s.handleSettings)
	mux.HandleFunc("/api/poster/", s.handlePoster)
	mux.HandleFunc("/api/artwork", s.handleArtwork)
	mux.HandleFunc("/api/network", s.handleNetwork)
	mux.HandleFunc("/api/cast-token/", s.handleCastToken)
	mux.HandleFunc("/api/ytdlp", s.handleYTDLP)
	mux.HandleFunc("/api/ytdlp/jobs", s.handleYTDLPJobs)
	mux.HandleFunc("/api/ytdlp/cancel/", s.handleYTDLPCancel)
	mux.HandleFunc("/api/favorites", s.handleFavorites)
	mux.HandleFunc("/api/favorites/", s.handleFavorites)
	mux.HandleFunc("/api/details/", s.handleDetails)
	mux.HandleFunc("/api/next/", s.handleNext)
	mux.HandleFunc("/api/subtitle/", s.handleSubtitle)
	mux.HandleFunc("/api/recent", s.handleRecent)
	mux.HandleFunc("/api/ondeck", s.handleOnDeck)
	mux.HandleFunc("/api/duplicates", s.handleDuplicates)
	mux.HandleFunc("/api/scan-status", s.handleScanStatus)
	mux.HandleFunc("/api/metadata/search", s.handleMetadataSearch)
	mux.HandleFunc("/api/metadata/apply", s.handleMetadataApply)
	mux.HandleFunc("/api/subtitles/search", s.handleSubtitleSearch)
	mux.HandleFunc("/api/subtitles/download", s.handleSubtitleDownload)
	mux.HandleFunc("/api/compat/", func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(strings.TrimPrefix(r.URL.Path, "/api/compat/"), "/") {
			s.handleCompatFile(w, r)
			return
		}
		s.handleCompat(w, r)
	})
	mux.HandleFunc("/api/users", s.handleUsers)
	mux.HandleFunc("/api/users/password", s.handleUserPassword)
	mux.HandleFunc("/api/user-count", s.handleUserCount)
	mux.HandleFunc("/api/stats", s.handleStats)
	mux.HandleFunc("/api/shutdown", s.handleShutdown)
	mux.HandleFunc("/static/", s.routeStatic)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// No caching anywhere: the UI and assets ship inside the binary and
		// change on every release. Browser caches made the UI look frozen.
		w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		s.allowCastToken(mux).ServeHTTP(w, r)
	})
}

// routeStatic serves the embedded UI assets (no disk involvement).
func (s *Server) routeStatic(w http.ResponseWriter, r *http.Request) {
	// Go's file server guesses the wrong type for .webmanifest, and browsers
	// reject a PWA manifest served as text/plain.
	if strings.HasSuffix(r.URL.Path, ".webmanifest") {
		w.Header().Set("Content-Type", "application/manifest+json")
	}
	http.StripPrefix("/static/", http.FileServer(http.FS(s.staticFS))).ServeHTTP(w, r)
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" && r.URL.Path != "/login" {
		http.NotFound(w, r)
		return
	}
	s.tmpl.ExecuteTemplate(w, "index.html", nil)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	s.tmpl.ExecuteTemplate(w, "index.html", nil)
}

func (s *Server) handleAPILogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method not allowed", 405)
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	req.Username = strings.TrimSpace(req.Username)
	ip := clientIP(r)
	// The lockout belongs to the account being tried, so a wrong password
	// for one user does not lock the others out.
	key := loginKey(ip, req.Username)
	if wait := loginLockout(key); wait > 0 {
		jsonError(w, fmt.Sprintf("too many failed attempts — wait %d min and try again", int(wait.Minutes())+1), 429)
		return
	}
	user, err := s.auth.Login(req.Username, req.Password)
	if err != nil {
		recordLoginFail(key)
		jsonError(w, "ACCESS DENIED — invalid username or password", 401)
		return
	}
	clearLoginFails(key)
	token, _ := s.auth.CreateSession(user.ID)
	http.SetCookie(w, &http.Cookie{
		Name:     "darkserve_session",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		MaxAge:   86400,
	})
	json.NewEncoder(w).Encode(user)
}

func (s *Server) handleAPIRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method not allowed", 405)
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	var count int
	s.db.QueryRow("SELECT COUNT(*) FROM users").Scan(&count)
	if count > 0 {
		// Someone already signed up. Ask an admin to create
		// additional accounts from the Users page instead.
		jsonError(w, "registration closed — ask the admin to create your account", 403)
		return
	}

	if msg := checkPassword(req.Password); msg != "" {
		jsonError(w, msg, 400)
		return
	}
	user, err := s.auth.Register(req.Username, req.Password, "admin")
	if err != nil {
		jsonError(w, "username already exists", 409)
		return
	}
	token, _ := s.auth.CreateSession(user.ID)
	http.SetCookie(w, &http.Cookie{
		Name:     "darkserve_session",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		MaxAge:   86400,
	})
	json.NewEncoder(w).Encode(user)
}

func (s *Server) handleAPILogout(w http.ResponseWriter, r *http.Request) {
	token := extractToken(r)
	if token != "" {
		s.auth.DestroySession(token)
	}
	http.SetCookie(w, &http.Cookie{
		Name:   "darkserve_session",
		Value:  "",
		Path:   "/",
		MaxAge: -1,
	})
	w.WriteHeader(204)
}

func (s *Server) handleDrives(w http.ResponseWriter, r *http.Request) {
	drives := s.detector.Scan()
	json.NewEncoder(w).Encode(drives)
}

func (s *Server) handleLibraries(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		var req struct {
			Name string `json:"name"`
			Path string `json:"path"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		_, err := s.db.Exec(
			"INSERT INTO libraries(name, path, is_usb, created_at) VALUES(?,?,?,?)",
			req.Name, req.Path, 0, now())
		if err != nil {
			jsonError(w, err.Error(), 500)
			return
		}
		w.WriteHeader(201)
		json.NewEncoder(w).Encode(map[string]string{"status": "created"})
		return
	}
	rows, err := s.db.Query("SELECT id, name, path, drive_label, is_usb FROM libraries ORDER BY name")
	if err != nil {
		jsonError(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	var libs []map[string]interface{}
	for rows.Next() {
		var id, isUSB int
		var name, path, label string
		rows.Scan(&id, &name, &path, &label, &isUSB)
		libs = append(libs, map[string]interface{}{
			"id": id, "name": name, "path": path, "label": label, "is_usb": isUSB == 1,
		})
	}
	json.NewEncoder(w).Encode(libs)
}

func (s *Server) handleLibrary(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimPrefix(r.URL.Path, "/api/libraries/")
	id, _ := strconv.Atoi(idStr)
	if r.Method == "DELETE" {
		// Foreign-key cascades are not enabled, so clear the index explicitly.
		s.db.Exec("DELETE FROM media_items WHERE library_id = ?", id)
		s.db.Exec("DELETE FROM libraries WHERE id = ?", id)
		w.WriteHeader(204)
		return
	}
	if r.Method == "POST" {
		go s.scanner.ScanLibrary(context.Background(), id)
		w.WriteHeader(202)
		json.NewEncoder(w).Encode(map[string]string{"status": "scanning"})
		return
	}
	var name, path, label string
	var isUSB int
	err := s.db.QueryRow("SELECT name, path, drive_label, is_usb FROM libraries WHERE id = ?", id).Scan(&name, &path, &label, &isUSB)
	if err != nil {
		jsonError(w, "not found", 404)
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"id": id, "name": name, "path": path, "label": label, "is_usb": isUSB == 1})
}

func (s *Server) handleMedia(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "method not allowed", 405)
		return
	}
	rows, err := s.db.Query("SELECT m.id, m.title, m.year, m.season, m.episode, m.path, m.ext, m.size, l.name, l.is_usb, m.library_id FROM media_items m JOIN libraries l ON l.id = m.library_id ORDER BY m.title")
	if err != nil {
		jsonError(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	var items []map[string]interface{}
	for rows.Next() {
		var id int
		var title, path, ext, libName string
		var year, season, episode *int
		var size int64
		var isUSB, libID int
		rows.Scan(&id, &title, &year, &season, &episode, &path, &ext, &size, &libName, &isUSB, &libID)
		items = append(items, map[string]interface{}{
			"id": id, "title": title, "path": path, "ext": ext, "size": size,
			"year": year, "season": season, "episode": episode,
			"library": libName, "is_usb": isUSB == 1, "library_id": libID,
		})
	}
	json.NewEncoder(w).Encode(items)
}

// rowT is one indexed media item joined with its library name.
type rowT struct {
	Id                               int
	Title, Path, Ext, LibName, Added string
	Year, Season, Ep                 *int
	Size                             int64
	IsUSB, LibID                     int
	Poster                           string
	Progress                         *float64
}

// GET /api/media/{libraryID}   → movies + collapsed TV shows
// GET /api/media/{libraryID}?folder=/abs/dir → episodes of one show
func (s *Server) handleMediaItem(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimPrefix(r.URL.Path, "/api/media/")
	idStr = strings.SplitN(idStr, "?", 2)[0]
	id, _ := strconv.Atoi(idStr)
	if id <= 0 {
		jsonError(w, "invalid library", 400)
		return
	}
	folderFilter := strings.TrimSpace(r.URL.Query().Get("folder"))

	// Resume position is per user, so it is only included when signed in.
	rows, err := s.db.Query(`
		SELECT m.id, m.title, m.year, m.season, m.episode, m.path, m.ext, m.size,
		       l.name, l.is_usb, m.library_id, m.added_at, m.poster,
		       (SELECT CASE WHEN wp.duration_seconds > 0
		                     THEN wp.position_seconds * 100.0 / wp.duration_seconds
		                     ELSE NULL END
		         FROM watch_progress wp
		         WHERE wp.media_item_id = m.id AND wp.user_id = ? AND wp.completed = 0)
		FROM media_items m JOIN libraries l ON l.id = m.library_id
		WHERE m.library_id = ?`, userID(r), id)
	if err != nil {
		jsonError(w, err.Error(), 500)
		return
	}
	var all []rowT
	for rows.Next() {
		var t rowT
		if rows.Scan(&t.Id, &t.Title, &t.Year, &t.Season, &t.Ep, &t.Path, &t.Ext,
			&t.Size, &t.LibName, &t.IsUSB, &t.LibID, &t.Added, &t.Poster, &t.Progress) != nil {
			continue
		}
		all = append(all, t)
	}
	rows.Close()

	if folderFilter != "" {
		// Episode listing for one show folder (recursive prefix match).
		out := s.episodesIn(all, strings.TrimSuffix(folderFilter, "/")+"/")
		sortEpisodes(out)
		// Episodes named "01.mkv" carry no season/episode in the DB —
		// number them by their sorted position so UI shows E01, E02…
		for i := range out {
			if out[i]["season"].(int) == 0 && out[i]["episode"].(int) == 0 {
				out[i]["season"] = 1
			}
			gotEp := out[i]["episode"].(int)
			if gotEp == 0 {
				// Sequential index within its season slice.
				out[i]["episode"] = i + 1
			}
		}
		json.NewEncoder(w).Encode(out)
		return
	}

	// Grouping: a directory is a TV show when the user put that show in its own
	// folder. Generic containers ("Videos", "Movies", ...) and the library root
	// are never a show, so their files always list as individual movies.
	showDirs := map[string]bool{}
	var libPath string
	s.db.QueryRow("SELECT path FROM libraries WHERE id = ?", id).Scan(&libPath)
	libPath = strings.TrimSuffix(libPath, "/")
	if s.cfg.TVGrouping {
		isShowFolder := func(dir string) bool {
			return dir != libPath && dir != "/" && !isGenericContainer(filepath.Base(dir))
		}
		for _, t := range all {
			show := showFolderOf(t.Path)
			if t.Ep != nil && *t.Ep > 0 && isShowFolder(show) {
				showDirs[show] = true
			}
			if isSeasonFolderName(filepath.Base(filepath.Dir(t.Path))) && isShowFolder(show) {
				showDirs[show] = true
			}
		}
	}

	var movies []map[string]interface{}
	type showAgg struct {
		Dir, LibName string
		Count        int
		IsUSB        bool
		LibID        int
		LastAdded    string
		PosterID     int
	}
	shows := map[string]*showAgg{}
	for _, t := range all {
		dir := showFolderOf(t.Path)
		if showDirs[dir] {
			ag, ok := shows[dir]
			if !ok {
				ag = &showAgg{Dir: dir, LibName: t.LibName, LibID: t.LibID, IsUSB: t.IsUSB == 1}
				shows[dir] = ag
			}
			if ag.PosterID == 0 && t.Poster != "" {
				ag.PosterID = t.Id
			}
			ag.Count++
			if t.Added > ag.LastAdded {
				ag.LastAdded = t.Added
			}
			continue
		}
		year, season, episode := 0, 0, 0
		if t.Year != nil {
			year = *t.Year
		}
		if t.Season != nil {
			season = *t.Season
		}
		if t.Ep != nil {
			episode = *t.Ep
		}
		movies = append(movies, map[string]interface{}{
			"type": "movie", "id": t.Id, "title": t.Title, "path": t.Path, "ext": t.Ext,
			"size": t.Size, "year": year, "season": season, "episode": episode,
			"library": t.LibName, "is_usb": t.IsUSB == 1, "library_id": t.LibID,
			"added_at": t.Added, "poster_id": posterID(t.Id, t.Poster),
			"audio":        config.IsAudioExt(t.Ext),
			"progress_pct": progressPct(t.Progress, t.Size),
		})
	}
	showsKey := make([]string, 0, len(shows))
	for k := range shows {
		showsKey = append(showsKey, k)
	}
	sort.Strings(showsKey)
	for _, dir := range showsKey {
		ag := shows[dir]
		movies = append(movies, map[string]interface{}{
			"type": "show", "id": 0, "title": filepath.Base(dir),
			"path": dir, "ext": "", "size": int64(0),
			"year": 0, "season": 0, "episode": ag.Count,
			"library": ag.LibName, "is_usb": ag.IsUSB, "library_id": ag.LibID,
			"added_at": ag.LastAdded, "poster_id": ag.PosterID,
			"progress_pct": 0,
		})
	}
	// Movies first, then shows (they're alphabetically mixed in most grids).
	sort.Slice(movies, func(i, j int) bool {
		ti, _ := movies[i]["title"].(string)
		tj, _ := movies[j]["title"].(string)
		return ti < tj
	})
	if movies == nil {
		movies = []map[string]interface{}{}
	}
	json.NewEncoder(w).Encode(movies)
}

// userID returns the signed-in user's id, or 0 for anonymous requests.
func userID(r *http.Request) int {
	if u := auth.GetUser(r.Context()); u != nil {
		return u.ID
	}
	return 0
}

// progressPct converts a stored completion percentage into 1–99 for the bar,
// or 0 when nothing usable was stored.
func progressPct(pct *float64, _ int64) int {
	if pct == nil || *pct <= 1 || *pct >= 99 {
		return 0
	}
	return int(*pct)
}

// episodesIn returns every item under a folder prefix, in database order.
func (s *Server) episodesIn(all []rowT, prefix string) []map[string]interface{} {
	out := []map[string]interface{}{}
	for _, t := range all {
		if !strings.HasPrefix(t.Path, prefix) {
			continue
		}
		year, season, episode := 0, 0, 0
		if t.Year != nil {
			year = *t.Year
		}
		if t.Season != nil {
			season = *t.Season
		}
		if t.Ep != nil {
			episode = *t.Ep
		}
		out = append(out, map[string]interface{}{
			"id": t.Id, "title": t.Title, "path": t.Path, "ext": t.Ext,
			"poster_id": posterID(t.Id, t.Poster), "audio": config.IsAudioExt(t.Ext),
			"size": t.Size, "year": year, "season": season, "episode": episode,
			"library": t.LibName, "is_usb": t.IsUSB == 1, "library_id": t.LibID,
			"progress_pct": progressPct(t.Progress, t.Size),
		})
	}
	return out
}

// sortEpisodes orders episodes by season, then episode number, then title.
func sortEpisodes(out []map[string]interface{}) {
	sort.Slice(out, func(i, j int) bool {
		si, _ := out[i]["season"].(int)
		sj, _ := out[j]["season"].(int)
		ei, _ := out[i]["episode"].(int)
		ej, _ := out[j]["episode"].(int)
		if si != sj {
			return si < sj
		}
		if ei != ej {
			return ei < ej
		}
		oi, _ := out[i]["title"].(string)
		oj, _ := out[j]["title"].(string)
		return oi < oj
	})
}

// GET /api/next/{id} — the episode that follows this one in the same series, so
// the player can advance automatically.
func (s *Server) handleNext(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/api/next/"))
	var libID int
	var path string
	var season, episode *int
	if err := s.db.QueryRow(`SELECT library_id, path, season, episode FROM media_items WHERE id = ?`, id).
		Scan(&libID, &path, &season, &episode); err != nil {
		jsonError(w, "not found", 404)
		return
	}
	isEp := (season != nil && *season > 0) || (episode != nil && *episode > 0)
	if !isEp {
		json.NewEncoder(w).Encode(map[string]interface{}{"next": nil})
		return
	}
	rows, err := s.db.Query(`
		SELECT m.id, m.title, m.path, m.ext, m.size, m.year, m.season, m.episode,
		       l.name, l.is_usb, m.library_id, m.added_at, m.poster
		FROM media_items m JOIN libraries l ON l.id = m.library_id
		WHERE m.library_id = ?`, libID)
	if err != nil {
		jsonError(w, err.Error(), 500)
		return
	}
	var all []rowT
	for rows.Next() {
		var t rowT
		if rows.Scan(&t.Id, &t.Title, &t.Path, &t.Ext, &t.Size, &t.Year, &t.Season, &t.Ep,
			&t.LibName, &t.IsUSB, &t.LibID, &t.Added, &t.Poster) != nil {
			continue
		}
		all = append(all, t)
	}
	rows.Close()

	folder := showFolderOf(path)
	eps := s.episodesIn(all, strings.TrimSuffix(folder, "/")+"/")
	sortEpisodes(eps)
	next := map[string]interface{}{"next": nil}
	for i, e := range eps {
		if e["id"].(int) != id || i+1 >= len(eps) {
			continue
		}
		n := eps[i+1]
		next = map[string]interface{}{
			"next": map[string]interface{}{
				"id": n["id"], "title": n["title"], "path": n["path"], "ext": n["ext"],
				"poster_id": n["poster_id"], "audio": n["audio"],
				"season": n["season"], "episode": n["episode"],
				"library": n["library"], "is_usb": n["is_usb"], "library_id": n["library_id"],
			},
		}
		break
	}
	json.NewEncoder(w).Encode(next)
}

// showFolderOf maps a video path to its "show" directory: the parent folder,
// but elevated one level when the parent is a "Season N" style folder.
func showFolderOf(path string) string {
	dir := filepath.Dir(path)
	base := filepath.Base(dir)
	low := strings.ToLower(base)
	if strings.HasPrefix(low, "season") || strings.HasPrefix(low, "s0") ||
		strings.HasPrefix(low, "s1") || strings.HasPrefix(low, "s2") {
		parent := filepath.Dir(dir)
		if parent != dir && parent != "." && strings.HasPrefix(parent, "/") {
			return parent
		}
	}
	return dir
}

func boolSetting(on bool) string {
	if on {
		return "1"
	}
	return "0"
}

// isGenericContainer reports whether a folder name is a general bucket of
// movies or shows rather than one show's own folder.
func isGenericContainer(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "videos", "video", "movies", "movie", "tv", "tv shows", "shows", "show",
		"series", "films", "film", "media", "downloads", "download", "anime",
		"cartoons", "kids", "misc", "other", "all":
		return true
	}
	return false
}

// isSeasonFolderName returns true if a folder name looks like a season folder
// (e.g., "Season 1", "Season 01", "S01", "Specials", etc.)
func isSeasonFolderName(name string) bool {
	low := strings.ToLower(name)
	return strings.HasPrefix(low, "season") ||
		strings.HasPrefix(low, "s0") ||
		strings.HasPrefix(low, "s1") ||
		strings.HasPrefix(low, "s2") ||
		strings.HasPrefix(low, "s3") ||
		strings.HasPrefix(low, "s4") ||
		strings.HasPrefix(low, "s5") ||
		strings.HasPrefix(low, "s6") ||
		strings.HasPrefix(low, "s7") ||
		strings.HasPrefix(low, "s8") ||
		strings.HasPrefix(low, "s9") ||
		low == "specials"
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		jsonError(w, "unauthorized", 401)
		return
	}
	json.NewEncoder(w).Encode(user)
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if q == "" {
		json.NewEncoder(w).Encode([]interface{}{})
		return
	}
	rows, err := s.db.Query("SELECT m.id, m.title, m.year, m.season, m.episode, m.path, m.ext, m.size, l.name, l.is_usb, m.library_id FROM media_items m JOIN libraries l ON l.id = m.library_id WHERE m.title LIKE ? ORDER BY m.title", "%"+q+"%")
	if err != nil {
		jsonError(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	var items []map[string]interface{}
	for rows.Next() {
		var id int
		var title, path, ext, libName string
		var year, season, episode *int
		var size int64
		var isUSB, libID int
		rows.Scan(&id, &title, &year, &season, &episode, &path, &ext, &size, &libName, &isUSB, &libID)
		items = append(items, map[string]interface{}{
			"id": id, "title": title, "path": path, "ext": ext, "size": size,
			"year": year, "season": season, "episode": episode,
			"library": libName, "is_usb": isUSB == 1, "library_id": libID,
		})
	}
	json.NewEncoder(w).Encode(items)
}

func (s *Server) handleScan(w http.ResponseWriter, r *http.Request) {
	rows, _ := s.db.Query("SELECT id FROM libraries")
	var ids []int
	for rows.Next() {
		var id int
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		go s.scanner.ScanLibrary(context.Background(), id)
	}
	w.WriteHeader(202)
	json.NewEncoder(w).Encode(map[string]string{"status": "scanning"})
}

func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimPrefix(r.URL.Path, "/api/stream/")
	id, _ := strconv.Atoi(idStr)
	var filePath string
	err := s.db.QueryRow("SELECT path FROM media_items WHERE id = ?", id).Scan(&filePath)
	if err != nil {
		jsonError(w, "not found", 404)
		return
	}
	if _, err := os.Stat(filePath); err != nil {
		jsonError(w, "file not found", 404)
		return
	}
	ext := strings.ToLower(filepath.Ext(filePath))
	contentType := "video/mp4"
	switch ext {
	case ".mkv":
		contentType = "video/x-matroska"
	case ".avi":
		contentType = "video/x-msvideo"
	case ".mov":
		contentType = "video/quicktime"
	case ".webm":
		contentType = "video/webm"
	case ".ts":
		contentType = "video/mp2t"
	case ".mp3":
		contentType = "audio/mpeg"
	case ".flac":
		contentType = "audio/flac"
	case ".wav":
		contentType = "audio/wav"
	case ".ogg", ".opus":
		contentType = "audio/ogg"
	case ".m4a":
		contentType = "audio/mp4"
	case ".aac":
		contentType = "audio/aac"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Accept-Ranges", "bytes")
	http.ServeFile(w, r, filePath)
}

func (s *Server) handleProgress(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimPrefix(r.URL.Path, "/api/progress/")
	parts := strings.SplitN(idStr, "/", 2)
	mediaID, _ := strconv.Atoi(parts[0])
	user := auth.GetUser(r.Context())
	if user == nil {
		jsonError(w, "unauthorized", 401)
		return
	}
	if r.Method == "POST" {
		var req struct {
			Position  float64 `json:"position"`
			Duration  float64 `json:"duration"`
			Completed bool    `json:"completed"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		s.db.Exec("INSERT INTO watch_progress(user_id, media_item_id, position_seconds, duration_seconds, completed, updated_at) VALUES(?,?,?,?,?,?) ON CONFLICT(user_id, media_item_id) DO UPDATE SET position_seconds=excluded.position_seconds, duration_seconds=excluded.duration_seconds, completed=excluded.completed, updated_at=excluded.updated_at",
			user.ID, mediaID, req.Position, req.Duration, req.Completed, now())
		w.WriteHeader(204)
		return
	}
	if r.Method == "DELETE" {
		// Forgets this title for this user only; it drops off Continue Watching.
		s.db.Exec("DELETE FROM watch_progress WHERE user_id = ? AND media_item_id = ?",
			user.ID, mediaID)
		w.WriteHeader(204)
		return
	}
	var pos, dur float64
	var completed int
	s.db.QueryRow("SELECT position_seconds, duration_seconds, completed FROM watch_progress WHERE user_id = ? AND media_item_id = ?",
		user.ID, mediaID).Scan(&pos, &dur, &completed)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"position": pos, "duration": dur, "completed": completed == 1,
	})
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		jsonError(w, "unauthorized", 401)
		return
	}
	if r.Method == "GET" {
		// Seed defaults, then overlay anything saved in the DB.
		settings := map[string]string{
			"addr":                s.cfg.Addr,
			"data_dir":            s.cfg.DataDir,
			"usb_paths":           strings.Join(s.cfg.USBPaths, ","),
			"local_paths":         strings.Join(s.cfg.LocalPaths, ","),
			"media_exts":          strings.Join(s.cfg.MediaExts, ","),
			"min_size_mb":         strconv.Itoa(s.cfg.MinSizeMB),
			"usb_autodetect":      "1",
			"tv_grouping":         boolSetting(s.cfg.TVGrouping),
			"iptv_user_agent":     s.cfg.IPTVUserAgent,
			"iptv_timeout_sec":    strconv.Itoa(s.cfg.IPTVTimeoutSec),
			"iptv_max_channels":   strconv.Itoa(s.cfg.IPTVMaxChannels),
			"rescan_minutes":      strconv.Itoa(s.cfg.RescanMinutes),
			"ytdlp_path":          s.cfg.YTDLPPath,
			"cast_enabled":        boolSetting(s.cfg.CastEnabled),
			"cast_url":            s.cfg.CastURL,
			"scan_excludes":       strings.Join(s.cfg.ScanExcludes, ","),
			"compat_preset":       s.cfg.CompatPreset,
			"compat_max_height":   s.cfg.CompatMaxHeight,
			"ytdlp_concurrency":   strconv.Itoa(s.cfg.YTDLPConcurrency),
			"subs_key":            maskKey(s.cfg.OSSubKey),
			"subs_lang":           s.cfg.OSSubLang,
			"subs_search_enabled": boolSetting(s.cfg.OSSubEnabled),
			"iptv_refresh_hours":  strconv.Itoa(s.cfg.IPTVRefreshHours),
			"artwork_online":      boolSetting(s.cfg.ArtworkOnline),
			"artwork_tmdb_key":    maskKey(s.cfg.TMDBKey),
		}
		rows, _ := s.db.Query("SELECT key, value FROM settings")
		for rows.Next() {
			var k, v string
			rows.Scan(&k, &v)
			settings[k] = v
		}
		rows.Close()
		json.NewEncoder(w).Encode(settings)
		return
	}
	if r.Method == "POST" {
		if user.Role != "admin" {
			jsonError(w, "admin required", 403)
			return
		}
		var req = map[string]string{}
		json.NewDecoder(r.Body).Decode(&req)
		for key, value := range req {
			// The API key is shown masked; a masked value means "unchanged".
			if key == "artwork_tmdb_key" && strings.HasPrefix(value, "••") {
				continue
			}
			// Persist, then apply live so no restart is needed.
			s.db.Exec("INSERT INTO settings(key, value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", key, value)
			s.cfg.ApplySetting(key, value)
		}
		s.detector.SetPaths(s.cfg.USBPaths)
		s.downloads.SetPath(s.cfg.YTDLPPath)
		s.downloads.MaxConcurrent = s.cfg.YTDLPConcurrency
		go s.scanner.SyncAll(context.Background(), s.detector.Scan())
		w.WriteHeader(204)
		return
	}
}

// handleUserCount tells the login screen whether an admin already exists,
// so it can show "Sign In" vs "First-time Setup".
func (s *Server) handleUserCount(w http.ResponseWriter, r *http.Request) {
	var count int
	s.db.QueryRow("SELECT COUNT(*) FROM users").Scan(&count)
	json.NewEncoder(w).Encode(map[string]int{"count": count})
}

func (s *Server) handleUsers(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil || user.Role != "admin" {
		jsonError(w, "admin required", 403)
		return
	}
	if r.Method == "GET" {
		users, _ := s.auth.ListUsers()
		json.NewEncoder(w).Encode(users)
		return
	}
	if r.Method == "DELETE" {
		idStr := r.URL.Query().Get("id")
		id, _ := strconv.Atoi(idStr)
		if err := s.auth.DeleteUser(id); err != nil {
			jsonError(w, err.Error(), 400)
			return
		}
		w.WriteHeader(204)
		return
	}
	if r.Method == "POST" {
		var req struct {
			Username string `json:"username"`
			Password string `json:"password"`
			Role     string `json:"role"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		if req.Role == "" {
			req.Role = "user"
		}
		if msg := checkPassword(req.Password); msg != "" {
			jsonError(w, msg, 400)
			return
		}
		_, err := s.auth.Register(req.Username, req.Password, req.Role)
		if err != nil {
			jsonError(w, err.Error(), 409)
			return
		}
		w.WriteHeader(201)
	}
}

// POST /api/users/password — gives a user a new password.
//
// The admin uses it when somebody cannot get in — a lost password, or a
// browser that filled in the wrong one. Resetting keeps the watch progress
// and the favourites of that account, deleting the account would not.
func (s *Server) handleUserPassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user := auth.GetUser(r.Context())
	if user == nil || user.Role != "admin" {
		jsonError(w, "admin required", http.StatusForbidden)
		return
	}
	var req struct {
		ID       int    `json:"id"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "bad request", http.StatusBadRequest)
		return
	}
	if msg := checkPassword(req.Password); msg != "" {
		jsonError(w, msg, 400)
		return
	}
	username, err := s.auth.SetPassword(req.ID, req.Password)
	if err != nil {
		jsonError(w, err.Error(), 400)
		return
	}
	// A new password deserves a fresh start, even if the account was
	// locked out for too many tries.
	clearAccountLockouts(username)
	json.NewEncoder(w).Encode(map[string]string{"status": "password changed"})
}

// GET /api/scan-status — is a scan running, and how far along it is.
func (s *Server) handleScanStatus(w http.ResponseWriter, r *http.Request) {
	active, lib, files, started, finished := s.scanner.ScanStatus()
	out := map[string]interface{}{
		"scanning": active, "library": lib, "files": files,
		"started": started, "finished": finished,
	}
	if active && !started.IsZero() {
		out["elapsed"] = int(time.Since(started).Seconds())
	}
	json.NewEncoder(w).Encode(out)
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	var libCount, mediaCount, userCount int
	s.db.QueryRow("SELECT COUNT(*) FROM libraries").Scan(&libCount)
	s.db.QueryRow("SELECT COUNT(*) FROM media_items").Scan(&mediaCount)
	s.db.QueryRow("SELECT COUNT(*) FROM users").Scan(&userCount)
	drives := s.detector.Scan()
	json.NewEncoder(w).Encode(map[string]interface{}{
		"libraries":   libCount,
		"media_items": mediaCount,
		"users":       userCount,
		"drives":      len(drives),
		"ffmpeg":      hasFFmpeg(),
	})
}

func extractToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	cookie, err := r.Cookie("darkserve_session")
	if err == nil {
		return cookie.Value
	}
	return ""
}

func jsonError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func now() string {
	return time.Now().UTC().Format(time.RFC3339)
}
