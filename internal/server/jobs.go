package server

import (
	"context"
	"log"
	"time"
)

// StartJobs runs the background loop: scheduled library rescans and
// automatic IPTV playlist refreshes. Settings are read on every tick, so
// changes made in the settings menu apply without a restart.
func (s *Server) StartJobs(ctx context.Context) {
	go func() {
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		var lastScan time.Time
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if m := s.cfg.RescanMinutes; m > 0 && time.Since(lastScan) >= time.Duration(m)*time.Minute {
					lastScan = time.Now()
					s.scanner.SyncAll(ctx, s.detector.Scan())
				}
				s.refreshDueIPTV()
			}
		}
	}()
}

// refreshDueIPTV re-fetches URL playlists whose last refresh is older than
// the configured interval. Paste-only lists have no source to fetch.
func (s *Server) refreshDueIPTV() {
	h := s.cfg.IPTVRefreshHours
	if h <= 0 {
		return
	}
	rows, err := s.db.Query(`SELECT id, source_type, source, updated_at FROM iptv_lists WHERE source_type = 'url'`)
	if err != nil {
		return
	}
	type due struct {
		id         int64
		stype, src string
	}
	var todo []due
	for rows.Next() {
		var d due
		var updated string
		if rows.Scan(&d.id, &d.stype, &d.src, &updated) != nil {
			continue
		}
		if t, err := time.Parse(time.RFC3339, updated); err != nil || time.Since(t) >= time.Duration(h)*time.Hour {
			todo = append(todo, d)
		}
	}
	rows.Close()
	for _, d := range todo {
		if _, err := s.refreshIPTVList(d.id, d.stype, d.src); err != nil {
			log.Printf("iptv auto-refresh list %d: %v", d.id, err)
		}
	}
}
