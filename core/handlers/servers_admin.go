package handlers

import (
	"dylaris-core/models"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/gorilla/mux"
)

// GetAdminServers GET /api/admin/servers — returns all DB servers with optional search filter
func (h *ServerHandler) GetAdminServers(w http.ResponseWriter, r *http.Request) {
	if !IsAdmin(r) {
		sendJSONError(w, "Forbidden", 403)
		return
	}

	servers, err := h.state.Store.ListServersForUser("", true)
	if err != nil {
		sendJSONError(w, "Database error", 500)
		return
	}
	if servers == nil {
		servers = []models.Server{}
	}

	search := strings.ToLower(r.URL.Query().Get("search"))
	if search != "" {
		filtered := servers[:0]
		for _, s := range servers {
			if strings.Contains(strings.ToLower(s.Name), search) ||
				strings.Contains(strings.ToLower(s.UUID), search) ||
				strings.Contains(strings.ToLower(s.OwnerName), search) {
				filtered = append(filtered, s)
			}
		}
		servers = filtered
	}

	memberCounts, _ := h.state.Store.CountInvitesPerServer()
	type adminServerRow struct {
		models.Server
		MemberCount int `json:"memberCount"`
	}
	rows := make([]adminServerRow, len(servers))
	for i, s := range servers {
		rows[i] = adminServerRow{Server: s, MemberCount: memberCounts[s.ID]}
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"servers": rows,
	})
}

// AdminUpdateServerOwner PATCH /api/admin/servers/{id}/owner — reassigns a server to a different user
func (h *ServerHandler) AdminUpdateServerOwner(w http.ResponseWriter, r *http.Request) {
	if !IsAdmin(r) {
		sendJSONError(w, "Forbidden", 403)
		return
	}
	vars := mux.Vars(r)
	serverID, _ := strconv.Atoi(vars["id"])

	var req struct {
		UserID string `json:"userId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.UserID == "" {
		sendJSONError(w, "userId required", 400)
		return
	}

	if _, err := h.state.Store.GetUserByID(req.UserID); err != nil {
		sendJSONError(w, "User not found", 404)
		return
	}

	if err := h.state.Store.UpdateServerOwner(serverID, &req.UserID); err != nil {
		sendJSONError(w, "Failed to update owner", 500)
		return
	}

	h.state.Events.Publish(r.Context(), "servers.changed", nil)

	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}
