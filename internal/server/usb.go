package server

import (
	"context"
	"encoding/json"
	"net/http"

	"darkserve/internal/auth"
	"darkserve/internal/usb"
)

// handleUnmountedDrives — GET /api/drives/unmounted
//
// Lists the USB drives that are plugged in but not mounted. On systems
// without automount they would otherwise be invisible.
func (s *Server) handleUnmountedDrives(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	json.NewEncoder(w).Encode(usb.Unmounted())
}

// handleMountDrive — POST /api/drives/mount
//
// Mounts a plugged-in drive so DarkServe can index it. Only the admin may
// do this, it changes the state of the machine.
func (s *Server) handleMountDrive(w http.ResponseWriter, r *http.Request) {
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
		Device string `json:"device"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "bad request", http.StatusBadRequest)
		return
	}
	mount, err := usb.Mount(req.Device)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	// Index it right away instead of waiting for the next drive poll.
	go s.scanner.SyncAll(context.Background(), s.detector.Scan())
	json.NewEncoder(w).Encode(map[string]string{"status": "mounted", "mount": mount})
}
