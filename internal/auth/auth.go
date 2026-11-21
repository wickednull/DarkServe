package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"
)

type User struct {
	ID       int    `json:"id"`
	Username string `json:"username"`
	Role     string `json:"role"`
}

type Manager struct {
	db *sql.DB
}

func NewManager(database *sql.DB) *Manager {
	return &Manager{db: database}
}

func (m *Manager) Register(username, password, role string) (*User, error) {
	hash := HashPassword(password)
	res, err := m.db.Exec(
		"INSERT INTO users(username, password_hash, role, created_at) VALUES(?,?,?,?)",
		username, hash, role, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &User{ID: int(id), Username: username, Role: role}, nil
}

func (m *Manager) Login(username, password string) (*User, error) {
	username = strings.TrimSpace(username)
	// The same name in a different case signs in as well — "dad" and "Dad"
	// are the same person at the kitchen table. Two accounts that differ
	// only in case stay apart: the exact name wins, and if there is none
	// the sign in is refused instead of guessing.
	rows, err := m.db.Query(
		"SELECT id, username, password_hash, role FROM users WHERE username = ? COLLATE NOCASE LIMIT 2",
		username)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var match *User
	var hash string
	count := 0
	for rows.Next() {
		var u User
		var h string
		if rows.Scan(&u.ID, &u.Username, &h, &u.Role) != nil {
			continue
		}
		count++
		if u.Username == username {
			match, hash = &u, h
		} else if match == nil {
			match, hash = &u, h
		}
	}
	if match == nil || (count > 1 && match.Username != username) {
		return nil, sql.ErrNoRows
	}
	if !VerifyPassword(password, hash) {
		return nil, sql.ErrNoRows
	}
	return match, nil
}

// SetPassword replaces the password of a user. The admin uses it when
// somebody cannot get into their account — resetting keeps their watch
// progress and favourites, deleting the account would not. It returns the
// name of the account that was changed.
func (m *Manager) SetPassword(userID int, password string) (string, error) {
	var username string
	if err := m.db.QueryRow("SELECT username FROM users WHERE id = ?", userID).Scan(&username); err != nil {
		return "", errors.New("no such user")
	}
	hash := HashPassword(password)
	if _, err := m.db.Exec("UPDATE users SET password_hash = ? WHERE id = ?", hash, userID); err != nil {
		return "", err
	}
	// Old sessions of that user are gone, they have to sign in again.
	m.db.Exec("DELETE FROM sessions WHERE user_id = ?", userID)
	return username, nil
}

func (m *Manager) GetUser(id int) (*User, error) {
	var u User
	err := m.db.QueryRow("SELECT id, username, role FROM users WHERE id = ?", id).
		Scan(&u.ID, &u.Username, &u.Role)
	return &u, err
}

func (m *Manager) ListUsers() ([]User, error) {
	rows, err := m.db.Query("SELECT id, username, role FROM users ORDER BY username")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var users []User
	for rows.Next() {
		var u User
		rows.Scan(&u.ID, &u.Username, &u.Role)
		users = append(users, u)
	}
	return users, nil
}

// DeleteUser removes a regular user. The admin account can never be deleted,
// guaranteeing there is always exactly one admin.
func (m *Manager) DeleteUser(id int) error {
	var role string
	if err := m.db.QueryRow("SELECT role FROM users WHERE id = ?", id).Scan(&role); err != nil {
		return err
	}
	if role == "admin" {
		return errors.New("cannot delete the admin account")
	}
	_, err := m.db.Exec("DELETE FROM users WHERE id = ?", id)
	return err
}

func (m *Manager) CreateSession(userID int) (string, error) {
	tokenBytes := make([]byte, 32)
	rand.Read(tokenBytes)
	token := base64.URLEncoding.EncodeToString(tokenBytes)
	_, err := m.db.Exec(
		"INSERT INTO sessions(token, user_id, expires_at) VALUES(?,?,?)",
		token, userID, time.Now().Add(24*time.Hour).UTC().Format(time.RFC3339))
	return token, err
}

func (m *Manager) GetSessionUser(token string) (*User, error) {
	var u User
	err := m.db.QueryRow(`
		SELECT u.id, u.username, u.role
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token = ? AND s.expires_at > ?`,
		token, time.Now().UTC().Format(time.RFC3339)).
		Scan(&u.ID, &u.Username, &u.Role)
	return &u, err
}

func (m *Manager) DestroySession(token string) error {
	_, err := m.db.Exec("DELETE FROM sessions WHERE token = ?", token)
	return err
}

func (m *Manager) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/static/") ||
			strings.HasPrefix(r.URL.Path, "/api/login") ||
			strings.HasPrefix(r.URL.Path, "/api/register") ||
			strings.HasPrefix(r.URL.Path, "/api/user-count") ||
			r.URL.Path == "/" {
			next.ServeHTTP(w, r)
			return
		}

		token := extractToken(r)
		if token == "" {
			jsonError(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		user, err := m.GetSessionUser(token)
		if err != nil {
			jsonError(w, "session expired", http.StatusUnauthorized)
			return
		}
		ctx := context.WithValue(r.Context(), userKey, user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func jsonError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	w.Write([]byte(`{"error":"` + msg + `"}`))
}

type ctxKey string

const userKey ctxKey = "user"

func GetUser(ctx context.Context) *User {
	u, _ := ctx.Value(userKey).(*User)
	return u
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

func HashPassword(password string) string {
	salt := make([]byte, 16)
	rand.Read(salt)
	h := sha256.New()
	h.Write(salt)
	h.Write([]byte(password))
	hash := h.Sum(nil)
	return hex.EncodeToString(salt) + ":" + hex.EncodeToString(hash)
}

func VerifyPassword(password, encoded string) bool {
	parts := strings.SplitN(encoded, ":", 2)
	if len(parts) != 2 {
		return false
	}
	salt, _ := hex.DecodeString(parts[0])
	expectedHash, _ := hex.DecodeString(parts[1])
	h := sha256.New()
	h.Write(salt)
	h.Write([]byte(password))
	hash := h.Sum(nil)
	return subtle.ConstantTimeCompare(hash, expectedHash) == 1
}
