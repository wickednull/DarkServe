package iptv

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Channel is one parsed playlist entry.
type Channel struct {
	Name  string
	Group string
	Logo  string
	URL   string
}

// Playlist is a parsed m3u/m3u8 document.
type Playlist struct {
	Channels []Channel
}

// Fetch downloads playlist content from an HTTP(S) URL. userAgent and timeout
// come from the in-app IPTV settings (many providers gate non-player UAs).
func Fetch(url, userAgent string, timeout time.Duration) (string, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "*/*")
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("provider responded %s", resp.Status)
	}
	limit := int64(32 << 20) // 32 MiB playlists are already generous
	if resp.ContentLength > limit {
		return "", fmt.Errorf("playlist too large (%d bytes)", resp.ContentLength)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// Parse reads #EXTM3U content (m3u / m3u8) into channels.
// Supports tvg-logo, tvg-name, group-title attributes and the
// comma-suffixed display name of each #EXTINF directive.
func Parse(content string) *Playlist {
	p := &Playlist{}
	scan := bufio.NewScanner(strings.NewReader(content))
	scan.Buffer(make([]byte, 0, 1024*1024), 8*1024*1024)

	var name, group, logo string
	seenExtInf := false
	for scan.Scan() {
		line := strings.TrimSpace(scan.Text())
		if line == "" {
			continue
		}
		upper := strings.ToUpper(line)

		switch {
		case strings.HasPrefix(upper, "#EXTM3U"):
			continue

		case strings.HasPrefix(upper, "#EXTINF"):
			name, group, logo = "", "", ""
			seenExtInf = true
			for _, attr := range []struct {
				key  string
				dest *string
			}{
				{"group-title", &group},
				{"tvg-logo", &logo},
				{"tvg-name", &name},
			} {
				if v := extractAttr(line, attr.key); v != "" {
					*attr.dest = v
				}
			}
			// Display name follows the last comma of the directive.
			if idx := strings.LastIndex(line, ","); idx != -1 {
				name = strings.TrimSpace(line[idx+1:])
			}
			continue

		case strings.HasPrefix(line, "#"):
			continue // other directives (EXTVLCOPT etc.)
		}

		// Anything not starting with '#' here is a channel URL.
		if !seenExtInf {
			name, group, logo = "", "", ""
		}
		if name == "" {
			// Title-less EXTINF — try "Group - Name" before a pipe in the URL.
			if i := strings.Index(line, "|"); i > 0 {
				name = strings.TrimSpace(line[:i])
			}
		}
		p.Channels = append(p.Channels, Channel{
			Name:  name,
			Group: group,
			Logo:  logo,
			URL:   line,
		})
		name, group, logo = "", "", ""
		seenExtInf = false
	}
	return p
}

// extractAttr pulls attribute="value" pairs from an EXTINF line.
func extractAttr(line, key string) string {
	lower := strings.ToLower(line)
	needle := key + "=\""
	start := strings.Index(lower, needle)
	if start == -1 {
		return ""
	}
	start += len(needle)
	end := strings.Index(line[start:], "\"")
	if end == -1 {
		return ""
	}
	return strings.TrimSpace(line[start : start+end])
}
