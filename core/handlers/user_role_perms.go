package handlers

import (
	"encoding/json"
	"net/http"

	"dylaris-core/store"
)

// These endpoints live on UserHandler alongside the existing user CRUD —
// kept in a separate file so the additions don't bloat users.go.

type setRoleRequest struct {
	Role string `json:"role"`
}

// SetUserRole PUT /api/admin/users/{id}/role
// Valid roles: "user", "support", "admin". Admins cannot demote themselves
// — prevents the last-admin-locks-themselves-out scenario.
func (h *UserHandler) SetUserRoleHandler(w http.ResponseWriter, r *http.Request) {
	if h.state.Store == nil {
		sendJSONError(w, "Database not connected", 503)
		return
	}
	if !IsAdmin(r) {
		sendJSONError(w, "Forbidden", 403)
		return
	}
	id, ok := parseUserID(w, r)
	if !ok {
		return
	}
	var req setRoleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", 400)
		return
	}
	if req.Role != "user" && req.Role != "support" && req.Role != "admin" {
		sendJSONError(w, "Invalid role (must be user, support, or admin)", 400)
		return
	}

	// Self-demotion guard: an admin demoting themselves is fine as long as
	// at least one other admin remains. Otherwise refuse — the operator
	// would lock themselves out of /admin/* immediately.
	actorID, _ := r.Context().Value("userID").(string)
	if actorID == id && req.Role != "admin" {
		users, _ := h.state.Store.ListUsers()
		adminCount := 0
		for _, u := range users {
			if u.IsAdmin && u.ID != id {
				adminCount++
			}
		}
		if adminCount == 0 {
			sendJSONError(w, "Cannot demote the last admin — promote someone else first", 409)
			return
		}
	}

	target, err := h.state.Store.GetUserByID(id)
	if err != nil || target == nil {
		sendJSONError(w, "User not found", 404)
		return
	}
	previousRole := target.Role
	if previousRole == "" {
		previousRole = "user"
	}

	if err := h.state.Store.SetUserRole(id, req.Role); err != nil {
		sendJSONError(w, "Failed to update role", 500)
		return
	}

	LogIdentityAudit(h.state, r, AuditEventUserRoleChanged, actorID, id, map[string]interface{}{
		"from": previousRole,
		"to":   req.Role,
	})

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"role":    req.Role,
	})
}

type setPermissionsRequest struct {
	CanDeleteServers   bool   `json:"canDeleteServers"`
	CanChangeResources bool   `json:"canChangeResources"`
	SupportTeam        string `json:"supportTeam"`
}

// SetUserPermissions PUT /api/admin/users/{id}/permissions
// Sets per-user capability flags. Admin overrides still apply at the
// EffectivePermissions level — these flags only ever matter for non-admins.
func (h *UserHandler) SetUserPermissionsHandler(w http.ResponseWriter, r *http.Request) {
	if h.state.Store == nil {
		sendJSONError(w, "Database not connected", 503)
		return
	}
	if !IsAdmin(r) {
		sendJSONError(w, "Forbidden", 403)
		return
	}
	id, ok := parseUserID(w, r)
	if !ok {
		return
	}
	var req setPermissionsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", 400)
		return
	}

	if err := h.state.Store.SetUserPermissionFlags(id, req.CanDeleteServers, req.CanChangeResources, req.SupportTeam); err != nil {
		sendJSONError(w, "Failed to update permissions", 500)
		return
	}

	actorID, _ := r.Context().Value("userID").(string)
	LogIdentityAudit(h.state, r, AuditEventUserPermissionsChanged, actorID, id, map[string]interface{}{
		"can_delete_servers":   req.CanDeleteServers,
		"can_change_resources": req.CanChangeResources,
		"support_team":         req.SupportTeam,
	})

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":            true,
		"canDeleteServers":   req.CanDeleteServers,
		"canChangeResources": req.CanChangeResources,
		"supportTeam":        req.SupportTeam,
	})
}

type setPanelRoleRequest struct {
	PanelRoleID *int     `json:"panelRoleId"`
	GrantCaps   []string `json:"grantCaps"`
	DenyCaps    []string `json:"denyCaps"`
}

// SetUserPanelRoleHandler PUT /api/admin/users/{id}/panel-role
// Assigns (panelRoleId) or clears (panelRoleId=null) the user's level-1 panel
// role and their per-user panel cap grant/deny overrides. Admin-gated inline
// for now (phase 4 -> RequireCap("users.write")). Every override cap must be a
// real PANEL-scope capability, and a non-null panelRoleId must reference an
// existing role. The legacy role/is_admin path is untouched (removed later).
func (h *UserHandler) SetUserPanelRoleHandler(w http.ResponseWriter, r *http.Request) {
	if h.state.Store == nil {
		sendJSONError(w, "Database not connected", 503)
		return
	}
	if !IsAdmin(r) {
		sendJSONError(w, "Forbidden", 403)
		return
	}
	id, ok := parseUserID(w, r)
	if !ok {
		return
	}
	var req setPanelRoleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", 400)
		return
	}
	// Override caps must be real PANEL-scope caps (validatePanelCaps lives in
	// panel_roles.go, same package).
	if err := validatePanelCaps(req.GrantCaps); err != nil {
		sendJSONError(w, err.Error(), 400)
		return
	}
	if err := validatePanelCaps(req.DenyCaps); err != nil {
		sendJSONError(w, err.Error(), 400)
		return
	}
	if req.PanelRoleID != nil {
		role, err := h.state.Store.GetPanelRole(*req.PanelRoleID)
		if err != nil || role == nil {
			sendJSONError(w, "Panel role not found", 404)
			return
		}
	}
	if err := h.state.Store.SetUserPanelRole(id, req.PanelRoleID); err != nil {
		sendJSONError(w, "Failed to set panel role", 500)
		return
	}
	ov := store.CapOverrides{Grant: req.GrantCaps, Deny: req.DenyCaps}
	if err := h.state.Store.SetUserPanelCapOverrides(id, ov); err != nil {
		sendJSONError(w, "Failed to set overrides", 500)
		return
	}

	actorID, _ := r.Context().Value("userID").(string)
	LogIdentityAudit(h.state, r, AuditEventUserPanelRoleChanged, actorID, id, map[string]interface{}{
		"panel_role_id": req.PanelRoleID,
		"grant_caps":    req.GrantCaps,
		"deny_caps":     req.DenyCaps,
	})

	json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}
