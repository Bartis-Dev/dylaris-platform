package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"
)

type MemberHandler struct {
	state *AppState
}

func NewMemberHandler(state *AppState) *MemberHandler {
	return &MemberHandler{state: state}
}

// checkMembersAccess checks if the requesting user is admin, owner, or an invited user with "members" permission.
// Returns true if access is granted.
func (h *MemberHandler) checkMembersAccess(w http.ResponseWriter, r *http.Request, serverID int) bool {
	srv, err := h.state.Store.GetServerByID(serverID)
	if err != nil {
		sendJSONError(w, "Server not found", http.StatusNotFound)
		return false
	}

	username := r.Context().Value("username").(string)
	isAdmin := r.Context().Value("isAdmin").(bool)
	if isAdmin || srv.OwnerName == username {
		return true
	}

	// Check if invited user has members permission
	userID, _ := r.Context().Value("userID").(int)
	if userID == 0 {
		sendJSONError(w, "Forbidden", http.StatusForbidden)
		return false
	}
	invite, err := h.state.Store.GetInvite(serverID, userID)
	if err != nil || !invite.Permissions.Members {
		sendJSONError(w, "Forbidden", http.StatusForbidden)
		return false
	}
	return true
}

// capPermissions ensures the given permissions don't exceed the inviter's own permissions.
// Owner/admin skip capping. Returns capped permissions map.
func (h *MemberHandler) capPermissions(r *http.Request, serverID int, perms map[string]bool) map[string]bool {
	username := r.Context().Value("username").(string)
	isAdmin := r.Context().Value("isAdmin").(bool)

	srv, err := h.state.Store.GetServerByID(serverID)
	if err != nil {
		return perms
	}
	if isAdmin || srv.OwnerName == username {
		return perms // no cap for owner/admin
	}

	// Get inviter's own permissions
	userID, _ := r.Context().Value("userID").(int)
	invite, err := h.state.Store.GetInvite(serverID, userID)
	if err != nil {
		return perms
	}

	// Cap each permission: invited user can only grant permissions they themselves have
	inviterPerms := map[string]bool{
		"console":  invite.Permissions.Console,
		"files":    invite.Permissions.Files,
		"config":   invite.Permissions.Config,
		"setup":    invite.Permissions.Setup,
		"power":    invite.Permissions.Power,
		"members":  invite.Permissions.Members,
		"overview": invite.Permissions.Overview,
		"network":  invite.Permissions.Network,
		"inherit":  invite.Permissions.Inherit,
	}

	capped := make(map[string]bool)
	for k, v := range perms {
		if inviterMax, exists := inviterPerms[k]; exists {
			capped[k] = v && inviterMax
		} else {
			capped[k] = v
		}
	}
	return capped
}

// GetMembers GET /api/servers/{id}/members
func (h *MemberHandler) GetMembers(w http.ResponseWriter, r *http.Request) {
	if h.state.Store == nil {
		sendJSONError(w, "Service unavailable", http.StatusServiceUnavailable)
		return
	}

	vars := mux.Vars(r)
	serverID, err := strconv.Atoi(vars["id"])
	if err != nil {
		sendJSONError(w, "Invalid server ID", http.StatusBadRequest)
		return
	}

	if !h.checkMembersAccess(w, r, serverID) {
		return
	}

	invites, err := h.state.Store.ListInvitesByServer(serverID)
	if err != nil {
		sendJSONError(w, "Failed to load members", http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"members": invites,
	})
}

// InviteMember POST /api/servers/{id}/members
func (h *MemberHandler) InviteMember(w http.ResponseWriter, r *http.Request) {
	if h.state.Store == nil {
		sendJSONError(w, "Service unavailable", http.StatusServiceUnavailable)
		return
	}

	vars := mux.Vars(r)
	serverID, err := strconv.Atoi(vars["id"])
	if err != nil {
		sendJSONError(w, "Invalid server ID", http.StatusBadRequest)
		return
	}

	var req struct {
		Username    string          `json:"username"`
		Permissions map[string]bool `json:"permissions"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if req.Username == "" {
		sendJSONError(w, "Username required", http.StatusBadRequest)
		return
	}

	srv, err := h.state.Store.GetServerByID(serverID)
	if err != nil {
		sendJSONError(w, "Server not found", http.StatusNotFound)
		return
	}

	if !h.checkMembersAccess(w, r, serverID) {
		return
	}

	// Look up the target user
	targetUser, err := h.state.Store.GetUserByUsername(req.Username)
	if err != nil {
		sendJSONError(w, "User not found", http.StatusNotFound)
		return
	}

	// Cannot invite the server owner
	if targetUser.Username == srv.OwnerName {
		sendJSONError(w, "Cannot invite the server owner", http.StatusBadRequest)
		return
	}

	// Get inviter's user ID
	inviterID := 0
	if id, ok := r.Context().Value("userID").(int); ok {
		inviterID = id
	}

	// Default permissions: all true except members
	if req.Permissions == nil {
		req.Permissions = map[string]bool{
			"console": true, "files": true,
			"config": true, "setup": true, "overview": true,
			"power": true, "members": false,
		}
	}

	// Cap permissions: invited users can only grant what they have
	req.Permissions = h.capPermissions(r, serverID, req.Permissions)

	if err := h.state.Store.CreateInvite(serverID, targetUser.ID, inviterID, req.Permissions); err != nil {
		sendJSONError(w, "Failed to create invite (user may already be invited)", http.StatusConflict)
		return
	}

	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// UpdateMemberPermissions PATCH /api/servers/{id}/members/{userId}
func (h *MemberHandler) UpdateMemberPermissions(w http.ResponseWriter, r *http.Request) {
	if h.state.Store == nil {
		sendJSONError(w, "Service unavailable", http.StatusServiceUnavailable)
		return
	}

	vars := mux.Vars(r)
	serverID, err := strconv.Atoi(vars["id"])
	if err != nil {
		sendJSONError(w, "Invalid server ID", http.StatusBadRequest)
		return
	}
	targetUserID, err := strconv.Atoi(vars["userId"])
	if err != nil {
		sendJSONError(w, "Invalid user ID", http.StatusBadRequest)
		return
	}

	var req struct {
		Permissions map[string]bool `json:"permissions"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if !h.checkMembersAccess(w, r, serverID) {
		return
	}

	// Cap permissions: invited users can only grant what they have
	req.Permissions = h.capPermissions(r, serverID, req.Permissions)

	if err := h.state.Store.UpdateInvitePermissions(serverID, targetUserID, req.Permissions); err != nil {
		sendJSONError(w, "Failed to update permissions", http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// RemoveMember DELETE /api/servers/{id}/members/{userId}
func (h *MemberHandler) RemoveMember(w http.ResponseWriter, r *http.Request) {
	if h.state.Store == nil {
		sendJSONError(w, "Service unavailable", http.StatusServiceUnavailable)
		return
	}

	vars := mux.Vars(r)
	serverID, err := strconv.Atoi(vars["id"])
	if err != nil {
		sendJSONError(w, "Invalid server ID", http.StatusBadRequest)
		return
	}
	targetUserID, err := strconv.Atoi(vars["userId"])
	if err != nil {
		sendJSONError(w, "Invalid user ID", http.StatusBadRequest)
		return
	}

	if !h.checkMembersAccess(w, r, serverID) {
		return
	}

	if err := h.state.Store.DeleteInvite(serverID, targetUserID); err != nil {
		sendJSONError(w, "Failed to remove member", http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// GetInheritedMembers GET /api/servers/{id}/members/inherited
// Returns users who have access via proxy inheritance (read-only view).
func (h *MemberHandler) GetInheritedMembers(w http.ResponseWriter, r *http.Request) {
	if h.state.Store == nil {
		sendJSONError(w, "Service unavailable", http.StatusServiceUnavailable)
		return
	}

	vars := mux.Vars(r)
	serverID, err := strconv.Atoi(vars["id"])
	if err != nil {
		sendJSONError(w, "Invalid server ID", http.StatusBadRequest)
		return
	}

	srv, err := h.state.Store.GetServerByID(serverID)
	if err != nil {
		sendJSONError(w, "Server not found", http.StatusNotFound)
		return
	}

	// Only child servers (those with a proxy_id) can have inherited members
	if srv.ProxyID == nil {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"members": []interface{}{},
		})
		return
	}

	// Get all invites on the proxy that have inherit=true
	proxyInvites, err := h.state.Store.ListInvitesByServer(*srv.ProxyID)
	if err != nil {
		sendJSONError(w, "Failed to load inherited members", http.StatusInternalServerError)
		return
	}

	var inherited []interface{}
	for _, inv := range proxyInvites {
		if inv.Permissions.Inherit {
			// Exclude the server owner (they already have full access)
			if inv.UserID == srv.OwnerID {
				continue
			}
			// Exclude users who have a direct invite on this server
			_, directErr := h.state.Store.GetInvite(serverID, inv.UserID)
			if directErr == nil {
				continue
			}
			inherited = append(inherited, map[string]interface{}{
				"userId":      inv.UserID,
				"username":    inv.Username,
				"permissions": inv.Permissions,
				"proxyId":     *srv.ProxyID,
			})
		}
	}

	if inherited == nil {
		inherited = []interface{}{}
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"members": inherited,
	})
}
