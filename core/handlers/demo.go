package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"

	"dylaris-core/store"

	"github.com/gorilla/mux"
)

// Demo servers are normal servers an admin flags as public read-only showcases.
// The list lives in a single setting (demo_server_uuids, JSON array of UUIDs)
// rather than a servers column, so no server scan/insert path changes. Non-owner
// viewers get READ-ONLY access to these servers (file list + read, console + stats
// view); every write stays denied by the default-deny access model (a demo viewer
// has no invite, so checkServerAccess returns false for all write endpoints).

const demoServerUUIDsSetting = "demo_server_uuids"

// loadDemoServerUUIDs returns the admin-flagged demo server UUIDs.
func loadDemoServerUUIDs(st store.Store) []string {
	raw, _ := st.GetSetting(demoServerUUIDsSetting)
	if raw == "" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
}

// isDemoServer reports whether the given server UUID is on the demo list.
func isDemoServer(st store.Store, uuid string) bool {
	if uuid == "" {
		return false
	}
	for _, u := range loadDemoServerUUIDs(st) {
		if u == uuid {
			return true
		}
	}
	return false
}

// SetServerDemo PATCH /api/admin/servers/{id}/demo — admin only.
// Adds or removes the server from the demo list. Multiple servers may be demos.
func (h *ServerHandler) SetServerDemo(w http.ResponseWriter, r *http.Request) {
	if !IsAdmin(r) {
		sendJSONError(w, "Admin only", http.StatusForbidden)
		return
	}
	serverID, err := strconv.Atoi(mux.Vars(r)["id"])
	if err != nil {
		sendJSONError(w, "Invalid server ID", http.StatusBadRequest)
		return
	}
	srv, err := h.state.Store.GetServerByID(serverID)
	if err != nil {
		sendJSONError(w, "Server not found", http.StatusNotFound)
		return
	}
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	current := loadDemoServerUUIDs(h.state.Store)
	next := make([]string, 0, len(current)+1)
	seen := false
	for _, u := range current {
		if u == srv.UUID {
			seen = true
			if req.Enabled {
				next = append(next, u) // keep
			}
			// when disabling, drop it (don't append)
			continue
		}
		next = append(next, u)
	}
	if req.Enabled && !seen {
		next = append(next, srv.UUID)
	}

	data, _ := json.Marshal(next)
	if err := h.state.Store.SetSetting(demoServerUUIDsSetting, string(data)); err != nil {
		sendJSONError(w, "Failed to save demo setting", http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "enabled": req.Enabled})
}
