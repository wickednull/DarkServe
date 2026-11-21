package scanner

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"darkserve/internal/config"
	"darkserve/internal/db"
	"darkserve/internal/usb"
)

type Scanner struct {
	db       *db.DB
	cfg      *config.Config
	detector *usb.Detector

	// ignoreMinSize lets a caller index files regardless of the minimum-size
	// setting — used for files the user downloaded on purpose.
	ignoreMinSize atomic.Bool
}

func New(database *db.DB, cfg *config.Config, detector *usb.Detector) *Scanner {
	return &Scanner{db: database, cfg: cfg, detector: detector}
}

var nonVideoExts = map[string]bool{
	".nfo": true, ".jpg": true, ".jpeg": true, ".png": true, ".gif": true,
	".bmp": true, ".srt": true, ".sub": true, ".idx": true, ".txt": true,
	".md": true, ".json": true, ".xml": true, ".html": true, ".htm": true,
	".pdf": true, ".zip": true, ".rar": true, ".7z": true, ".tar": true,
	".gz": true, ".bz2": true, ".xz": true, ".iso": true, ".img": true,
	".exe": true, ".dll": true, ".so": true, ".dylib": true, ".app": true,
	".deb": true, ".rpm": true, ".pkg": true, ".dmg": true, ".vmdk": true,
	".qcow2": true, ".vdi": true, ".vhdx": true, ".cue": true, ".m3u": true,
	".m3u8": true, ".pls": true, ".wpl": true, ".xspf": true, ".rtf": true,
	".doc": true, ".docx": true, ".xls": true, ".xlsx": true, ".ppt": true,
	".pptx": true, ".odt": true, ".ods": true, ".odp": true, ".csv": true,
	".tsv": true, ".log": true, ".conf": true, ".cfg": true, ".ini": true,
	".yaml": true, ".yml": true, ".toml": true, ".env": true, ".sh": true,
	".bash": true, ".zsh": true, ".fish": true, ".ps1": true, ".bat": true,
	".cmd": true, ".vbs": true, ".js": true, ".jsx": true,
	".tsx": true, ".css": true, ".scss": true, ".sass": true, ".less": true,
	".py": true, ".rb": true, ".pl": true, ".pm": true, ".lua": true,
	".r": true, ".jl": true, ".go": true, ".rs": true, ".c": true, ".cpp": true,
	".cc": true, ".cxx": true, ".h": true, ".hpp": true, ".java": true,
	".kt": true, ".kts": true, ".swift": true, ".m": true, ".mm": true,
	".cs": true, ".vb": true, ".fs": true, ".fsx": true, ".erl": true,
	".ex": true, ".exs": true, ".hs": true, ".lhs": true, ".ml": true,
	".mli": true, ".nim": true, ".d": true, ".zig": true, ".v": true,
	".sv": true, ".vhd": true, ".vhdl": true, ".asm": true, ".s": true,
	".dart": true, ".groovy": true, ".gradle": true, ".clj": true,
	".cljs": true, ".edn": true, ".scala": true, ".sc": true, ".sbt": true,
	".coffee": true, ".litcoffee": true, ".elm": true, ".purs": true,
	".idr": true, ".lidr": true, ".agda": true, ".lean": true,
	".tla": true, ".pluscal": true, ".candid": true, ".did": true,
	".vh": true, ".vapi": true, ".vala": true, ".gs": true, ".gst": true,
	".gsx": true, ".gsp": true, ".nut": true, ".cl": true, ".lisp": true,
	".lsp": true, ".asd": true, ".fnl": true, ".scm": true, ".ss": true,
	".rkt": true, ".scrbl": true, ".sch": true, ".sld": true, ".sxml": true,
	".sls": true, ".sps": true, ".f": true, ".f90": true, ".f95": true,
	".f03": true, ".f08": true, ".for": true, ".ftn": true, ".fpp": true,
	".cob": true, ".cbl": true, ".cpy": true, ".cobol": true, ".adb": true,
	".ads": true, ".ada": true, ".a": true, ".i": true, ".ii": true,
	".inl": true, ".ipp": true, ".tpp": true, ".txx": true, ".cppm": true,
	".cxxm": true, ".hppm": true, ".ixx": true, ".pyw": true, ".pyx": true,
	".pxd": true, ".pxi": true, ".pyi": true, ".pyc": true, ".pyo": true,
	".pyd": true, ".rmd": true, ".rnw": true, ".rprofile": true, ".rproj": true,
	".jmd": true, ".jldoctest": true, ".juliarc": true, ".jsm": true,
	".mjs": true, ".cjs": true, ".json5": true, ".jsonc": true,
	".jsonl": true, ".ndjson": true, ".map": true, ".d.ts": true,
	".js.map": true, ".css.map": true, ".styl": true, ".stylus": true,
	".pcss": true, ".postcss": true, ".sss": true, ".sugarss": true,
}

func (s *Scanner) AddUSBDrive(drive usb.Drive) {
	ctx := context.Background()
	name := drive.Label
	if name == "" {
		name = filepath.Base(drive.Mount)
	}

	var count int
	s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM libraries WHERE path = ?", drive.Mount).Scan(&count)
	if count == 0 {
		s.db.ExecContext(ctx,
			"INSERT INTO libraries(name, path, drive_label, is_usb, created_at) VALUES(?,?,?,?,?)",
			name, drive.Mount, drive.Label, 1, time.Now().UTC().Format(time.RFC3339))
	}

	var libID int
	s.db.QueryRowContext(ctx, "SELECT id FROM libraries WHERE path = ?", drive.Mount).Scan(&libID)
	s.scanPath(ctx, libID, drive.Mount)
}

// ScanLibraryForce indexes a library ignoring the minimum file size, so a
// deliberately downloaded clip is never filtered out.
func (s *Scanner) ScanLibraryForce(ctx context.Context, libraryID int) error {
	s.ignoreMinSize.Store(true)
	defer s.ignoreMinSize.Store(false)
	return s.ScanLibrary(ctx, libraryID)
}

func (s *Scanner) ScanLibrary(ctx context.Context, libraryID int) error {
	var path string
	err := s.db.QueryRowContext(ctx, "SELECT path FROM libraries WHERE id = ?", libraryID).Scan(&path)
	if err != nil {
		return err
	}
	s.scanPath(ctx, libraryID, path)
	return nil
}

// scanState tracks the running scan so the UI can show progress.
type scanState struct {
	mu       sync.Mutex
	Active   bool
	Library  string
	Files    int
	Started  time.Time
	Finished time.Time
}

var scan scanState

// ScanStatus reports what the background scan is doing.
func (s *Scanner) ScanStatus() (bool, string, int, time.Time, time.Time) {
	scan.mu.Lock()
	defer scan.mu.Unlock()
	return scan.Active, scan.Library, scan.Files, scan.Started, scan.Finished
}

func (s *Scanner) scanPath(ctx context.Context, libID int, root string) {
	defer s.pruneMissing(ctx, libID, root)
	scan.mu.Lock()
	scan.Active = true
	scan.Files = 0
	scan.Started = time.Now()
	scan.Finished = time.Time{}
	scan.mu.Unlock()
	defer func() {
		scan.mu.Lock()
		scan.Active = false
		scan.Finished = time.Now()
		scan.mu.Unlock()
	}()
	var libName string
	s.db.QueryRow("SELECT name FROM libraries WHERE id = ?", libID).Scan(&libName)
	scan.mu.Lock()
	scan.Library = libName
	scan.mu.Unlock()
	seen := 0
	filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() {
			seen++
			// Publish progress every 200 files so the UI can show movement.
			if seen%200 == 0 {
				scan.mu.Lock()
				scan.Files = seen
				scan.mu.Unlock()
			}
		}
		if info.IsDir() {
			// Skip hidden folders such as .Trash-1000 and .git.
			if path != root && strings.HasPrefix(info.Name(), ".") {
				return filepath.SkipDir
			}
			// Skip anything the admin excluded, e.g. node_modules.
			if s.excluded(path) {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))

		if nonVideoExts[ext] {
			return nil
		}

		if !s.isMediaExt(ext) {
			return nil
		}

		if !validateVideoFile(path, ext) {
			return nil
		}

		if strings.HasPrefix(filepath.Base(path), ".") {
			return nil
		}

		// Skip files smaller than the configured minimum. Audio tracks are
		// exempt: short clips are legitimate, unlike video fragments.
		if info.Size() < s.minBytes() && !s.ignoreMinSize.Load() && !config.IsAudioExt(ext) {
			return nil
		}

		var count int
		s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM media_items WHERE path = ?", path).Scan(&count)
		if count > 0 {
			return nil
		}

		meta := parseFilename(path)

		s.db.ExecContext(ctx,
			`INSERT INTO media_items(library_id, title, original_title, year, season, episode, path, ext, size, added_at, poster)
			 VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
			libID, meta.Title, meta.OriginalTitle, meta.Year, meta.Season, meta.Episode,
			path, ext, info.Size(), time.Now().UTC().Format(time.RFC3339), sidecarPoster(path))
		return nil
	})
}

// excluded reports whether a path matches a configured exclusion. Matching is
// by path fragment so "node_modules" and "/home/null/Downloads" both work.
func (s *Scanner) excluded(path string) bool {
	for _, ex := range s.cfg.ScanExcludes {
		ex = strings.TrimSpace(ex)
		if ex != "" && strings.Contains(path, ex) {
			return true
		}
	}
	return false
}

// sidecarPoster finds artwork saved next to a video, named after it
// ("Movie.mp4" → "Movie.jpg" / "Movie-poster.jpg"). Folder-level images are
// ignored there so one cover never decorates every file in a shared folder.
// Audio tracks also accept album art for the whole folder (cover.jpg, …).
func sidecarPoster(path string) string {
	dir := filepath.Dir(path)
	ext := filepath.Ext(path)
	base := strings.TrimSuffix(filepath.Base(path), ext)
	for _, name := range []string{base + "-poster.jpg", base + "-poster.png", base + ".jpg", base + ".png"} {
		p := filepath.Join(dir, name)
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return "file:" + p
		}
	}
	if config.IsAudioExt(ext) {
		for _, name := range []string{"cover.jpg", "cover.png", "folder.jpg", "folder.png",
			"front.jpg", "front.png", "albumart.jpg", "albumart.png", "AlbumArt.jpg"} {
			p := filepath.Join(dir, name)
			if info, err := os.Stat(p); err == nil && !info.IsDir() {
				return "file:" + p
			}
		}
	}
	return ""
}

// pruneMissing drops index rows whose files are gone or now live in hidden
// folders. It only runs while the library root is reachable, so an unplugged
// drive keeps its index until it comes back.
func (s *Scanner) pruneMissing(ctx context.Context, libID int, root string) {
	if _, err := os.Stat(root); err != nil {
		return
	}
	rows, err := s.db.QueryContext(ctx, "SELECT id, path FROM media_items WHERE library_id = ?", libID)
	if err != nil {
		return
	}
	var stale []int
	for rows.Next() {
		var id int
		var path string
		if rows.Scan(&id, &path) != nil {
			continue
		}
		hidden := strings.Contains(strings.TrimPrefix(path, root), "/.")
		if _, err := os.Stat(path); err != nil || hidden {
			stale = append(stale, id)
		}
	}
	rows.Close()
	for _, id := range stale {
		s.db.ExecContext(ctx, "DELETE FROM media_items WHERE id = ?", id)
	}
}

// validateVideoFile confirms the file header actually looks like video for
// its extension.
func validateVideoFile(path string, ext string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 16)
	n, _ := f.Read(buf)
	return HeaderOK(ext, buf[:n])
}

// HeaderOK reports whether the first bytes of a file look like a real video
// for its extension. MP4-family files are matched by the "ftyp" box at
// offset 4 (box sizes vary: 0x18, 0x20, 0x24…).
func HeaderOK(ext string, hdr []byte) bool {
	if len(hdr) < 12 {
		return false
	}
	switch ext {
	case ".mp4", ".mov", ".m4v", ".m4a":
		return string(hdr[4:8]) == "ftyp"
	case ".flac":
		return string(hdr[0:4]) == "fLaC"
	case ".wav":
		return string(hdr[0:4]) == "RIFF" && string(hdr[8:12]) == "WAVE"
	case ".ogg", ".opus":
		return string(hdr[0:4]) == "OggS"
	case ".wma":
		return hdr[0] == 0x30 && hdr[1] == 0x26 && hdr[2] == 0xB2 && hdr[3] == 0x75 &&
			hdr[4] == 0x8E && hdr[5] == 0x66 && hdr[6] == 0xCF && hdr[7] == 0x11
	case ".mp3", ".aac":
		// Either an ID3 tag upfront or a raw frame sync (0xFF Ex/Fx).
		return string(hdr[0:3]) == "ID3" || (hdr[0] == 0xFF && (hdr[1]&0xE0) == 0xE0)
	case ".mkv", ".webm":
		return hdr[0] == 0x1A && hdr[1] == 0x45 && hdr[2] == 0xDF && hdr[3] == 0xA3
	case ".avi":
		return string(hdr[0:4]) == "RIFF" && string(hdr[8:12]) == "AVI "
	case ".flv":
		return string(hdr[0:3]) == "FLV"
	case ".wmv":
		return hdr[0] == 0x30 && hdr[1] == 0x26 && hdr[2] == 0xB2 && hdr[3] == 0x75 && hdr[4] == 0x8E && hdr[5] == 0x66
	case ".mpg", ".mpeg":
		return hdr[0] == 0x00 && hdr[1] == 0x00 && hdr[2] == 0x01 && hdr[3] == 0xBA
	case ".ts":
		return hdr[0] == 0x47
	default:
		return false
	}
}

func (s *Scanner) isMediaExt(ext string) bool {
	for _, e := range s.cfg.MediaExts {
		if e == ext {
			return true
		}
	}
	return false
}

type Metadata struct {
	Title         string
	OriginalTitle string
	Year          int
	Season        int
	Episode       int
}

// MetadataFor parses title/year/season/episode info out of a file path.
func MetadataFor(path string) Metadata {
	return parseFilename(path)
}

var (
	yearRe       = regexp.MustCompile(`[\.\_\(\[ ](\d{4})[\.\_\)\] ]`)
	bareYearRe   = regexp.MustCompile(`\b(19[3-9]\d|20[0-4]\d)\b`)
	seasonRe     = regexp.MustCompile(`(?i)s(\d{1,2})[\s\.\_\-]*e(\d{1,3})`)
	seasonRe2    = regexp.MustCompile(`(?i)season[\s\.\_\-]*(\d{1,2})`)
	episodeRe    = regexp.MustCompile(`(?i)episode[\s\.\_\-]*(\d{1,3})`)
	episodeRe2   = regexp.MustCompile(`(?i)[\s\-\.]?ep(\d{1,3})`) // Matches EP1, EP2, etc.
	resolutionRe = regexp.MustCompile(`(\d{3,4})p`)
	cleanRe      = regexp.MustCompile(`[\.\_\-\[\]\(\)\{\}]`)
)

func parseFilename(path string) Metadata {
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	name := strings.TrimSuffix(base, ext)

	meta := Metadata{
		OriginalTitle: name,
	}

	// Year with separators (file|2024|x264) or free-floating ("Akira 1988 Full").
	if m := yearRe.FindStringSubmatch(name); m != nil {
		meta.Year, _ = strconv.Atoi(m[1])
	} else if m := bareYearRe.FindStringSubmatch(name); m != nil {
		meta.Year, _ = strconv.Atoi(m[1])
	}

	if m := seasonRe.FindStringSubmatch(name); m != nil {
		meta.Season, _ = strconv.Atoi(m[1])
		meta.Episode, _ = strconv.Atoi(m[2])
	} else {
		if m := seasonRe2.FindStringSubmatch(name); m != nil {
			meta.Season, _ = strconv.Atoi(m[1])
		}
		if m := episodeRe.FindStringSubmatch(name); m != nil {
			meta.Episode, _ = strconv.Atoi(m[1])
		} else if m := episodeRe2.FindStringSubmatch(name); m != nil {
			meta.Episode, _ = strconv.Atoi(m[1])
		}
	}

	title := name
	title = yearRe.ReplaceAllString(title, " ")
	title = bareYearRe.ReplaceAllString(title, " ")
	title = seasonRe.ReplaceAllString(title, " ")
	title = seasonRe2.ReplaceAllString(title, " ")
	title = episodeRe.ReplaceAllString(title, " ")
	title = episodeRe2.ReplaceAllString(title, " ")
	title = resolutionRe.ReplaceAllString(title, " ")
	title = cleanRe.ReplaceAllString(title, " ")
	title = strings.TrimSpace(title)
	title = strings.Join(strings.Fields(title), " ")
	meta.Title = title

	return meta
}

// ---- live settings helpers (no config files involved) ----

func (s *Scanner) setting(key string) string {
	var v string
	if err := s.db.QueryRow("SELECT value FROM settings WHERE key = ?", key).Scan(&v); err != nil {
		return ""
	}
	return v
}

func (s *Scanner) minBytes() int64 {
	v := s.setting("min_size_mb")
	if v == "" {
		return int64(s.cfg.MinSizeMB) * 1024 * 1024
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n <= 0 {
		return 1024 * 1024
	}
	return int64(n) * 1024 * 1024
}

// SyncAll ensures every USB volume found by the detector and every local
// media root exists as a library and is indexed. Respects the
// usb_autodetect toggle saved in settings.
func (s *Scanner) SyncAll(ctx context.Context, drives []usb.Drive) {
	if s.setting("usb_autodetect") != "0" {
		for _, drive := range drives {
			s.AddUSBDrive(drive)
		}
	}
	s.syncLocal(ctx)
}

// syncLocal indexes the user's standard media folders (Videos/Music by
// default) so locally stored media shows up alongside USB content.
func (s *Scanner) syncLocal(ctx context.Context) {
	for _, raw := range s.cfg.LocalPaths {
		p := expandPath(raw)
		if p == "" {
			continue
		}
		st, err := os.Stat(p)
		if err != nil || !st.IsDir() {
			continue
		}
		var count int
		s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM libraries WHERE path = ?", p).Scan(&count)
		if count == 0 {
			s.db.ExecContext(ctx,
				"INSERT INTO libraries(name, path, drive_label, is_usb, created_at) VALUES(?,?,?,?,?)",
				"Local · "+filepath.Base(p), p, "local", 0, time.Now().UTC().Format(time.RFC3339))
		}
		var id int
		if err := s.db.QueryRowContext(ctx, "SELECT id FROM libraries WHERE path = ?", p).Scan(&id); err == nil {
			s.scanPath(ctx, id, p)
		}
	}
}

func expandPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(p, "~"), "/"))
		}
	}
	return os.ExpandEnv(p)
}
