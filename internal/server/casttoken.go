package server

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"darkserve/internal/auth"
)

// A TV has no login cookie, so media sent to it travels under a signed,
// expiring URL instead. The token carries the user, the library it may play
// from, and an expiry, all covered by an HMAC kept server-side.
const castTokenTTL = 24 * time.Hour

// castSecret loads the signing key, creating it on first use so tokens stay
// valid across restarts.
func (s *Server) castSecret() []byte {
	var v string
	if err := s.db.QueryRow("SELECT value FROM settings WHERE key = 'cast_secret'").Scan(&v); err == nil && len(v) >= 32 {
		return []byte(v)
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		// Should never happen; fall back to a fixed-size zero key rather than panic.
		return make([]byte, 32)
	}
	secret := hex.EncodeToString(buf)
	s.db.Exec("INSERT INTO settings(key, value) VALUES('cast_secret', ?) "+
		"ON CONFLICT(key) DO UPDATE SET value=excluded.value", secret)
	return []byte(secret)
}

func castSign(secret []byte, payload string) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}

// GET /api/cast-token/{libraryID} → a URL-safe token for that library.
func (s *Server) handleCastToken(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		jsonError(w, "unauthorized", 401)
		return
	}
	libID, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/api/cast-token/"))
	if libID <= 0 {
		jsonError(w, "invalid library", 400)
		return
	}
	var count int
	s.db.QueryRow("SELECT COUNT(*) FROM libraries WHERE id = ?", libID).Scan(&count)
	if count == 0 {
		jsonError(w, "library not found", 404)
		return
	}
	exp := time.Now().Add(castTokenTTL).Unix()
	payload := fmt.Sprintf("%d.%d.%d", user.ID, libID, exp)
	sig := castSign(s.castSecret(), payload)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"token":   payload + "." + sig,
		"expires": exp,
	})
}

// castTokenUser validates a signed token from a query parameter and returns
// the user id it was issued for, plus the library it may play from.
func (s *Server) castTokenUser(r *http.Request) (libID int, ok bool) {
	tok := strings.TrimSpace(r.URL.Query().Get("token"))
	if tok == "" {
		return 0, false
	}
	parts := strings.Split(tok, ".")
	if len(parts) != 4 {
		return 0, false
	}
	payload := strings.Join(parts[:3], ".")
	if castSign(s.castSecret(), payload) != parts[3] {
		return 0, false
	}
	exp, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return 0, false
	}
	libID, err = strconv.Atoi(parts[1])
	if err != nil {
		return 0, false
	}
	return libID, true
}

// allowCastToken wraps the auth middleware so media endpoints also accept a
// signed cast token in place of a session cookie. The token is scoped to one
// library, so a leaked token cannot reach the rest of the index.
func (s *Server) allowCastToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if libID, ok := s.castTokenUser(r); ok && s.castTokenMayRead(libID, r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		s.auth.Middleware(next).ServeHTTP(w, r)
	})
}

// castTokenMayRead reports whether a path points at media inside a library.
func (s *Server) castTokenMayRead(libID int, path string) bool {
	var id int
	switch {
	case strings.HasPrefix(path, "/api/stream/"):
		id, _ = strconv.Atoi(strings.TrimPrefix(path, "/api/stream/"))
	case strings.HasPrefix(path, "/api/poster/"):
		id, _ = strconv.Atoi(strings.TrimPrefix(path, "/api/poster/"))
	case strings.HasPrefix(path, "/api/compat/"):
		id, _ = strconv.Atoi(strings.SplitN(strings.TrimPrefix(path, "/api/compat/"), "/", 2)[0])
	default:
		return false
	}
	if id <= 0 {
		return false
	}
	var itemLib int
	if err := s.db.QueryRow("SELECT library_id FROM media_items WHERE id = ?", id).Scan(&itemLib); err != nil {
		return false
	}
	return itemLib == libID
}
