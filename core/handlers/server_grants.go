package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"dylaris-core/authz"
	"dylaris-core/models"
	"dylaris-core/store"
)

type assignGrantRequest struct {
	Username     string   `json:"username"`
	ServerID     *int     `json:"serverId"`     // nil = account-wide (acting user's own realm)
	ServerRoleID *int     `json:"serverRoleId"` // nil = overrides only
	GrantCaps    []string `json:"grantCaps"`
	DenyCaps     []string `json:"denyCaps"`
	Inherit      bool     `json:"inherit"`
}

// dedupeCaps returns in with duplicates removed, order preserved.
func dedupeCaps(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, c := range in {
		if seen[c] {
			continue
		}
		seen[c] = true
		out = append(out, c)
	}
	return out
}

// AssignGrant POST /api/grants
// Assigns a friend a server-role and/or granular overrides, per-server
// (serverId set) or account-wide (serverId null = the acting user's own realm).
// Owner/admin bypass the delegation cap; a non-owner assigner must hold
// members.write on the server AND may only grant caps they themselves hold.
// Phase 4/6 wire audit + permissions_mode enforcement here.
func (h *ServerRolesHandler) AssignGrant(w http.ResponseWriter, r *http.Request) {
	if h.state.Store == nil {
		sendJSONError(w, "Database not connected", 503)
		return
	}
	var req assignGrantRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", 400)
		return
	}
	if req.Username == "" {
		sendJSONError(w, "Username required", 400)
		return
	}
	if err := validateServerOwnerCaps(req.GrantCaps); err != nil {
		sendJSONError(w, err.Error(), 400)
		return
	}
	if err := validateServerOwnerCaps(req.DenyCaps); err != nil {
		sendJSONError(w, err.Error(), 400)
		return
	}

	idn := authz.IdentityFromContext(r.Context())
	if idn.UserID == "" {
		sendJSONError(w, "Forbidden", 403)
		return
	}
	if !idn.IsAdmin && PermissionsMode(h.state.Store) == authz.ModeOff {
		sendJSONError(w, "Delegation is disabled", 403)
		return
	}

	target, err := h.state.Store.GetUserByUsername(req.Username)
	if err != nil || target == nil {
		sendJSONError(w, "User not found", 404)
		return
	}
	if target.ID == idn.UserID {
		sendJSONError(w, "Cannot assign a grant to yourself", 400)
		return
	}

	// Determine the realm owner + whether this is a per-server or account-wide grant.
	var ownerUserID string
	var srv *models.Server
	if req.ServerID != nil {
		srv, err = h.state.Store.GetServerByID(*req.ServerID)
		if err != nil || srv == nil {
			sendJSONError(w, "Server not found", 404)
			return
		}
		ownerUserID = srv.OwnerID
		if target.ID == srv.OwnerID {
			sendJSONError(w, "Cannot assign a grant to the server owner", 400)
			return
		}
	} else {
		// Account-wide grants only ever target the acting user's OWN realm.
		ownerUserID = idn.UserID
	}

	// A server-role (if given) must belong to the realm owner.
	var roleCaps []string
	if req.ServerRoleID != nil {
		role, rerr := h.state.Store.GetServerRole(*req.ServerRoleID)
		if rerr != nil || role == nil || role.OwnerUserID != ownerUserID {
			sendJSONError(w, "Server role not found in this realm", 404)
			return
		}
		roleCaps = role.Capabilities
	}

	// Owner/admin bypass the delegation cap. Account-wide is always the acting
	// user's own realm, so they are the owner by construction.
	isOwnerOrAdmin := idn.IsAdmin || req.ServerID == nil || (srv != nil && srv.OwnerID == idn.UserID)
	if req.ServerID != nil && !isOwnerOrAdmin {
		res, rerr := h.state.Authz.Resolve(idn, *req.ServerID)
		if rerr != nil {
			sendJSONError(w, "Authorization check failed", 500)
			return
		}
		if !res.HasCap("members.write") {
			sendJSONError(w, "Forbidden", 403)
			return
		}
		requested := dedupeCaps(append(append([]string{}, roleCaps...), req.GrantCaps...))
		if len(authz.CapSubset(res, requested)) != len(requested) {
			sendJSONError(w, "Cannot grant capabilities you do not hold", 403)
			return
		}
	}

	ov := store.CapOverrides{Grant: req.GrantCaps, Deny: req.DenyCaps}
	if err := h.state.Store.UpsertServerGrant(req.ServerID, target.ID, ownerUserID, req.ServerRoleID, ov, req.Inherit); err != nil {
		sendJSONError(w, "Failed to assign grant", 500)
		return
	}
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

type revokeGrantRequest struct {
	Username string `json:"username"`
	ServerID *int   `json:"serverId"` // nil = account-wide (acting user's own realm)
}

// RevokeGrant DELETE /api/grants
// Removes a friend's per-server or account-wide grant. Owner/admin, or a
// non-owner holding members.delete on the server, may revoke. No delegation cap
// (removing access is always safe for an authorized manager).
func (h *ServerRolesHandler) RevokeGrant(w http.ResponseWriter, r *http.Request) {
	if h.state.Store == nil {
		sendJSONError(w, "Database not connected", 503)
		return
	}
	var req revokeGrantRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", 400)
		return
	}
	if req.Username == "" {
		sendJSONError(w, "Username required", 400)
		return
	}
	idn := authz.IdentityFromContext(r.Context())
	if idn.UserID == "" {
		sendJSONError(w, "Forbidden", 403)
		return
	}
	target, err := h.state.Store.GetUserByUsername(req.Username)
	if err != nil || target == nil {
		sendJSONError(w, "User not found", 404)
		return
	}

	var ownerUserID string
	if req.ServerID != nil {
		srv, serr := h.state.Store.GetServerByID(*req.ServerID)
		if serr != nil || srv == nil {
			sendJSONError(w, "Server not found", 404)
			return
		}
		ownerUserID = srv.OwnerID
		if !(idn.IsAdmin || srv.OwnerID == idn.UserID) {
			res, rerr := h.state.Authz.Resolve(idn, *req.ServerID)
			if rerr != nil {
				sendJSONError(w, "Authorization check failed", 500)
				return
			}
			if !res.HasCap("members.delete") {
				sendJSONError(w, "Forbidden", 403)
				return
			}
		}
	} else {
		ownerUserID = idn.UserID
	}

	if err := h.state.Store.DeleteServerGrant(req.ServerID, ownerUserID, target.ID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			sendJSONError(w, "Grant not found", 404)
			return
		}
		sendJSONError(w, "Failed to revoke grant", 500)
		return
	}
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

type grantView struct {
	Username       string   `json:"username"`
	ServerID       *int     `json:"serverId"`
	ServerName     string   `json:"serverName"`
	ServerRoleID   *int     `json:"serverRoleId"`
	ServerRoleName string   `json:"serverRoleName"`
	GrantCaps      []string `json:"grantCaps"`
	DenyCaps       []string `json:"denyCaps"`
	Inherit        bool     `json:"inherit"`
	AccountWide    bool     `json:"accountWide"`
}

// ListGrants GET /api/grants - every grant in the acting owner's realm
// (account-wide server_id NULL + per-server), for the /access grants table.
// Gated at the route with RequireCap("roles.read"); admin short-circuits.
func (h *ServerRolesHandler) ListGrants(w http.ResponseWriter, r *http.Request) {
	if h.state.Store == nil {
		sendJSONError(w, "Database not connected", 503)
		return
	}
	owner := actingUserID(r)
	if owner == "" {
		sendJSONError(w, "Forbidden", 403)
		return
	}
	grants, err := h.state.Store.ListGrantsByOwner(owner)
	if err != nil {
		sendJSONError(w, "Failed to list grants", 500)
		return
	}
	views := make([]grantView, 0, len(grants))
	for _, g := range grants {
		views = append(views, grantView{
			Username:       g.Username,
			ServerID:       g.ServerID,
			ServerName:     g.ServerName,
			ServerRoleID:   g.ServerRoleID,
			ServerRoleName: g.ServerRoleName,
			GrantCaps:      normalizeCaps(g.CapOverrides.Grant),
			DenyCaps:       normalizeCaps(g.CapOverrides.Deny),
			Inherit:        g.Inherit,
			AccountWide:    g.ServerID == nil,
		})
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "grants": views})
}
