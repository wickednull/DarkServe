package server

import (
	"encoding/json"
	"net/http"
	"time"

	"darkserve/internal/auth"
)

// handleShutdown stops the server. Only the admin may call it.
//
// The AppImage starts DarkServe in the background, so the person who
// double-clicked the icon has no terminal to stop it with. This is the
// "Shut down server" button in the settings page.
func (s *Server) handleShutdown(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user := auth.GetUser(r.Context())
	if user == nil || user.Role != "admin" {
		jsonError(w, "admin required", http.StatusForbidden)
		return
	}
	json.NewEncoder(w).Encode(map[string]string{"status": "stopping"})
	// Answer first, stop afterwards — otherwise the browser sees a broken
	// connection instead of a confirmation.
	go func() {
		time.Sleep(300 * time.Millisecond)
		if s.shutdownHook != nil {
			s.shutdownHook()
		}
	}()
}
