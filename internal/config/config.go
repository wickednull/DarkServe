package config

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Config struct {
	Addr       string
	DataDir    string
	CacheDir   string
	USBPaths   []string
	LocalPaths []string
	MediaExts  []string
	MinSizeMB  int

	TVGrouping      bool
	IPTVUserAgent   string
	IPTVTimeoutSec  int
	IPTVMaxChannels int

	ArtworkOnline bool
	TMDBKey       string

	RescanMinutes    int
	IPTVRefreshHours int
	YTDLPPath        string
	CastEnabled      bool
	CastURL          string
	CompatPreset     string
	CompatMaxHeight  string
	YTDLPConcurrency int
	ScanExcludes     []string
	OSSubKey         string
	OSSubLang        string
	OSSubEnabled     bool
}

func Default() *Config {
	home, _ := os.UserHomeDir()
	return &Config{
		Addr:       ":7676",
		DataDir:    filepath.Join(home, ".local/share/darkserve"),
		CacheDir:   filepath.Join(home, ".cache/darkserve"),
		USBPaths:   []string{"/media", "/mnt", "/run/media"},
		LocalPaths: []string{"$HOME/Videos", "$HOME/Music"},
		MediaExts: []string{".mkv", ".mp4", ".avi", ".mov", ".webm", ".m4v", ".mpg", ".mpeg",
			".flv", ".wmv", ".ts",
			".mp3", ".flac", ".wav", ".ogg", ".m4a", ".aac", ".opus", ".wma"},
		MinSizeMB: 1,

		TVGrouping:      true,
		IPTVUserAgent:   "VLC/3.0.21 LibVLC/3.0.21",
		IPTVTimeoutSec:  30,
		IPTVMaxChannels: 0,

		CompatPreset:     "veryfast",
		CompatMaxHeight:  "0",
		YTDLPConcurrency: 2,
		OSSubLang:        "en",
		RescanMinutes:    30,
		IPTVRefreshHours: 12,
	}
}

func Load(path string) (*Config, error) {
	cfg := Default()
	if path == "" {
		return cfg, nil
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(parts[0]))
		val := strings.Trim(strings.TrimSpace(parts[1]), "\"'")
		switch key {
		case "addr":
			cfg.Addr = val
		case "data_dir":
			cfg.DataDir = val
		case "cache_dir":
			cfg.CacheDir = val
		case "usb_paths":
			cfg.USBPaths = SplitList(val)
		case "local_paths":
			cfg.LocalPaths = SplitList(val)
		case "media_exts":
			cfg.MediaExts = SplitList(val)
		case "min_size_mb":
			if n, err := strconv.Atoi(val); err == nil && n > 0 {
				cfg.MinSizeMB = n
			}
		}
	}
	return cfg, nil
}

// AudioExts lists the audio formats DarkServe can index. They are kept in the
// same accepted-formats list as video so uploads and scans share one setting.
var AudioExts = []string{".mp3", ".flac", ".wav", ".ogg", ".m4a", ".aac", ".opus", ".wma"}

// IsAudioExt reports whether an extension is an audio format.
func IsAudioExt(ext string) bool {
	ext = strings.ToLower(ext)
	for _, e := range AudioExts {
		if e == ext {
			return true
		}
	}
	return false
}

// EnsureAudioExts appends any missing audio format, so libraries configured
// before audio support existed keep working. Returns the list unchanged when
// every audio format is already present.
func EnsureAudioExts(exts []string) ([]string, bool) {
	have := map[string]bool{}
	for _, e := range exts {
		have[strings.ToLower(strings.TrimSpace(e))] = true
	}
	out := exts
	changed := false
	for _, e := range AudioExts {
		if !have[e] {
			out = append(out, e)
			changed = true
		}
	}
	return out, changed
}

// SplitList parses a comma separated list into non-empty, trimmed parts.
func SplitList(s string) []string {
	s = strings.Trim(s, "[]")
	parts := strings.Split(s, ",")
	var out []string
	for _, p := range parts {
		p = strings.Trim(strings.TrimSpace(p), "\"'")
		if p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ApplySetting lets the in-app settings menu reconfigure the running server
// without touching any config file.
func (c *Config) ApplySetting(key, value string) {
	switch strings.ToLower(key) {
	case "usb_paths":
		if v := SplitList(value); v != nil {
			c.USBPaths = v
		}
	case "local_paths":
		c.LocalPaths = SplitList(value) // may be empty = no local libs
	case "media_exts":
		if v := SplitList(value); v != nil {
			c.MediaExts = v
		}
	case "min_size_mb":
		if n, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && n > 0 {
			c.MinSizeMB = n
		}
	case "tv_grouping":
		c.TVGrouping = strings.TrimSpace(value) != "0"
	case "iptv_user_agent":
		if v := strings.TrimSpace(value); v != "" {
			c.IPTVUserAgent = v
		}
	case "iptv_timeout_sec":
		if n, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && n >= 5 && n <= 300 {
			c.IPTVTimeoutSec = n
		}
	case "iptv_max_channels":
		if n, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && n >= 0 {
			c.IPTVMaxChannels = n
		}
	case "artwork_online":
		c.ArtworkOnline = strings.TrimSpace(value) == "1"
	case "artwork_tmdb_key":
		c.TMDBKey = strings.TrimSpace(value)
	case "rescan_minutes":
		if n, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && n >= 0 && n <= 1440 {
			c.RescanMinutes = n
		}
	case "iptv_refresh_hours":
		if n, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && n >= 0 && n <= 168 {
			c.IPTVRefreshHours = n
		}
	case "ytdlp_path":
		c.YTDLPPath = strings.TrimSpace(value)
	case "cast_enabled":
		c.CastEnabled = strings.TrimSpace(value) == "1"
	case "cast_url":
		c.CastURL = strings.TrimRight(strings.TrimSpace(value), "/")
	case "compat_preset":
		switch strings.TrimSpace(value) {
		case "ultrafast", "superfast", "veryfast", "faster", "fast", "medium", "slow", "slower":
			c.CompatPreset = strings.TrimSpace(value)
		}
	case "compat_max_height":
		switch strings.TrimSpace(value) {
		case "480", "720", "1080", "0":
			c.CompatMaxHeight = strings.TrimSpace(value)
		}
	case "ytdlp_concurrency":
		if n, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && n >= 1 && n <= 5 {
			c.YTDLPConcurrency = n
		}
	case "subs_key":
		c.OSSubKey = strings.TrimSpace(value)
	case "subs_lang":
		c.OSSubLang = strings.TrimSpace(value)
	case "scan_excludes":
		if v := SplitList(value); v != nil {
			c.ScanExcludes = v
		} else {
			c.ScanExcludes = nil
		}
	case "subs_search_enabled":
		c.OSSubEnabled = strings.TrimSpace(value) == "1"
	}
}
