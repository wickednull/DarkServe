package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"darkserve/internal/auth"
)

const (
	tmdbAPI       = "https://api.themoviedb.org/3/search/"
	tmdbPosterURL = "https://image.tmdb.org/t/p/w500"
)

// artRun tracks the one background poster lookup allowed at a time.
var artRun struct {
	sync.Mutex
	running bool
	matched int
	checked int
	lastErr string
}

var (
	junkTitleRe = regexp.MustCompile(`(?i)\s*full\s+(hd\s+)?(movie\s+)?online.*$|\s*myflix.*$`)
	dotRe       = regexp.MustCompile(`[._]+`)
	camelRe     = regexp.MustCompile(`([a-z])([A-Z])`)
	yearTagRe   = regexp.MustCompile(`\s*\((19|20)\d{2}\)`)
	yearInRe    = regexp.MustCompile(`\((19|20\d{2})\)`)
)

// posterID returns the item id when the item has real artwork, else 0 so
// the UI draws its gradient placeholder instead.
func posterID(id int, poster string) int {
	if poster == "" || poster == "-" {
		return 0
	}
	return id
}

// cleanTitle strips release-site noise and separators so the online lookup
// sees a real title: "BladeRunner.2049 Full HD online" → "Blade Runner 2049".
func cleanTitle(s string) string {
	s = junkTitleRe.ReplaceAllString(s, "")
	s = dotRe.ReplaceAllString(s, " ")
	s = camelRe.ReplaceAllString(s, "$1 $2")
	s = yearTagRe.ReplaceAllString(s, " ")
	return strings.Join(strings.Fields(s), " ")
}

func maskKey(k string) string {
	if k == "" {
		return ""
	}
	if len(k) <= 4 {
		return "••••"
	}
	return "••••••••" + k[len(k)-4:]
}

// /api/artwork — GET returns lookup status and counts (admin only),
// POST starts a background lookup for every item still missing art.
func (s *Server) handleArtwork(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil || user.Role != "admin" {
		jsonError(w, "admin required", 403)
		return
	}
	if r.Method == "GET" {
		var withArt, missing, unmatched int
		s.db.QueryRow("SELECT COUNT(*) FROM media_items WHERE poster LIKE 'file:%' OR poster LIKE 'https://%'").Scan(&withArt)
		s.db.QueryRow("SELECT COUNT(*) FROM media_items WHERE poster = ''").Scan(&missing)
		s.db.QueryRow("SELECT COUNT(*) FROM media_items WHERE poster = '-'").Scan(&unmatched)
		artRun.Lock()
		out := map[string]interface{}{
			"enabled": s.cfg.ArtworkOnline, "key_set": s.cfg.TMDBKey != "",
			"running": artRun.running, "matched": artRun.matched, "checked": artRun.checked,
			"with_art": withArt, "missing": missing, "unmatched": unmatched, "error": artRun.lastErr,
		}
		artRun.Unlock()
		json.NewEncoder(w).Encode(out)
		return
	}
	if r.Method != "POST" {
		jsonError(w, "method not allowed", 405)
		return
	}
	if !s.cfg.ArtworkOnline || s.cfg.TMDBKey == "" {
		jsonError(w, "turn on online artwork and add a TMDB key in settings first", 400)
		return
	}
	artRun.Lock()
	if artRun.running {
		artRun.Unlock()
		jsonError(w, "a lookup is already running", 409)
		return
	}
	artRun.running, artRun.matched, artRun.checked, artRun.lastErr = true, 0, 0, ""
	artRun.Unlock()
	go s.fetchPosters(s.cfg.TMDBKey)
	w.WriteHeader(202)
	json.NewEncoder(w).Encode(map[string]string{"status": "fetching"})
}

type lookupJob struct {
	id, year, season, episode int
	title, path               string
}

func (s *Server) fetchPosters(key string) {
	defer func() {
		artRun.Lock()
		artRun.running = false
		artRun.Unlock()
	}()
	rows, err := s.db.Query(`SELECT id, title, path, year, season, episode FROM media_items WHERE poster = '' ORDER BY id`)
	if err != nil {
		setArtErr(err.Error())
		return
	}
	var jobs []lookupJob
	for rows.Next() {
		var j lookupJob
		var year, season, episode *int
		if rows.Scan(&j.id, &j.title, &j.path, &year, &season, &episode) != nil {
			continue
		}
		if year != nil {
			j.year = *year
		}
		if season != nil {
			j.season = *season
		}
		if episode != nil {
			j.episode = *episode
		}
		jobs = append(jobs, j)
	}
	rows.Close()

	// Episodes are looked up by their show's folder name, once per show.
	cache := map[string]tmdbResult{}
	for _, j := range jobs {
		kind, name := "movie", cleanTitle(j.title)
		if j.season > 0 || j.episode > 0 {
			kind = "tv"
			name = cleanTitle(filepath.Base(showFolderOf(j.path)))
		}
		year := j.year
		if m := yearInRe.FindStringSubmatch(filepath.Base(showFolderOf(j.path))); kind == "tv" && m != nil {
			year, _ = strconv.Atoi(m[1])
		}
		ck := fmt.Sprintf("%s|%s|%d", kind, strings.ToLower(name), year)
		res, seen := cache[ck]
		if !seen {
			// Transport hiccups are retried; a 401 means the key is wrong.
			for attempt := 0; attempt < 3; attempt++ {
				res, err = tmdbLookup(key, kind, name, year)
				if err == nil {
					break
				}
				setArtErr(err.Error())
				if strings.Contains(err.Error(), "401") || !networkFault(err) {
					break
				}
				if attempt == 2 {
					break
				}
				time.Sleep(time.Second)
			}
			if err != nil {
				continue // leave unmarked so a later run retries
			}
			cache[ck] = res
			time.Sleep(250 * time.Millisecond)
		}
		poster := "-"
		if res.Poster != "" {
			poster = tmdbPosterURL + res.Poster
		}
		s.db.Exec("UPDATE media_items SET poster = ?, overview = ?, rating = ? WHERE id = ?",
			poster, res.Overview, res.Rating, j.id)
		artRun.Lock()
		artRun.checked++
		if res.Poster != "" {
			artRun.matched++
		}
		artRun.Unlock()
	}
}

func setArtErr(msg string) {
	artRun.Lock()
	artRun.lastErr = msg
	artRun.Unlock()
}

type tmdbResult struct {
	Poster, Overview string
	Rating           float64
}

// networkFault reports whether an error is a transport problem worth retrying.
func networkFault(err error) bool {
	if err == nil {
		return false
	}
	var e net.Error
	if errors.As(err, &e) {
		return e.Timeout()
	}
	return true
}

func timeoutHint(err error) string {
	if errors.As(err, new(net.Error)) || strings.Contains(err.Error(), "timeout") {
		return "request timed out"
	}
	return "network error"
}

// tmdbLookup searches The Movie Database. It tries the year first, then
// without it. Only the title and year leave the machine.
func tmdbLookup(key, kind, title string, year int) (tmdbResult, error) {
	if title == "" {
		return tmdbResult{}, nil
	}
	if year > 0 {
		if res, err := tmdbSearch(key, kind, title, year); err != nil || res.Poster != "" || res.Overview != "" {
			return res, err
		}
	}
	return tmdbSearch(key, kind, title, 0)
}

func tmdbSearch(key, kind, title string, year int) (tmdbResult, error) {
	q := url.Values{}
	q.Set("query", title)
	if year > 0 {
		if kind == "tv" {
			q.Set("first_air_date_year", strconv.Itoa(year))
		} else {
			q.Set("year", strconv.Itoa(year))
		}
	}
	req, err := http.NewRequest("GET", tmdbAPI+kind+"?"+q.Encode(), nil)
	if err != nil {
		return tmdbResult{}, err
	}
	// Two credential types exist: the short v3 API key goes in the query,
	// the long v4 "Read Access Token" (a JWT) goes in the Authorization header.
	if strings.HasPrefix(key, "eyJ") {
		req.Header.Set("Authorization", "Bearer "+key)
	} else {
		q.Set("api_key", key)
		req.URL.RawQuery = q.Encode()
	}
	req.Header.Set("Accept", "application/json")
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		// Never include the URL: it carries the API key.
		return tmdbResult{}, fmt.Errorf("could not reach themoviedb.org (%v)", timeoutHint(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		var e struct {
			Message string `json:"status_message"`
		}
		json.Unmarshal(body, &e)
		msg := e.Message
		if msg == "" {
			msg = resp.Status
		}
		return tmdbResult{}, fmt.Errorf("TMDB %d: %s", resp.StatusCode, msg)
	}
	var out struct {
		Results []struct {
			PosterPath  string  `json:"poster_path"`
			Overview    string  `json:"overview"`
			VoteAverage float64 `json:"vote_average"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return tmdbResult{}, err
	}
	for _, r := range out.Results {
		if r.PosterPath != "" || r.Overview != "" {
			return tmdbResult{Poster: r.PosterPath, Overview: r.Overview, Rating: r.VoteAverage}, nil
		}
	}
	return tmdbResult{}, nil
}
