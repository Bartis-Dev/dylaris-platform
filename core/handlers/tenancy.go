package handlers

import (
	"net/http"

	"dylaris-core/models"
)

// byonCallerID returns the authenticated user's id from the request context.
func byonCallerID(r *http.Request) string {
	id, _ := r.Context().Value("userID").(string)
	return id
}

// byonActive reports whether BYON multi-tenancy node-ownership scoping is on.
// When off (the default), the platform behaves as today's single-operator panel
// and NONE of the ownership scoping below applies. This is the gate that keeps
// solo / hoster behavior identical.
func byonActive(state *AppState, r *http.Request) bool {
	return state != nil && state.FeatureFlags != nil && state.FeatureFlags.IsBYONEnabled(r.Context())
}

// canManageNode reports whether the caller may manage a specific node. Admins
// (operators) always may. In BYON mode the node's owning tenant may manage their
// own node. Outside BYON mode, node management stays admin-only (today's behavior).
func canManageNode(state *AppState, r *http.Request, node *models.Node) bool {
	if IsAdmin(r) {
		return true
	}
	if byonActive(state, r) && node != nil && node.OwnerID != nil {
		uid := byonCallerID(r)
		return uid != "" && *node.OwnerID == uid
	}
	return false
}

// canPlaceOnNode reports whether the caller may place a server on a node. Admins
// always may. In BYON mode a tenant may place ONLY on their own node. Platform
// nodes (owner_id nil) are operator-only here: tenants get capacity on those via
// the rented-server path (auto-provisioned by the operator), not by self-service
// placement. Outside BYON mode, placement stays admin-only (today). Plan-limit
// and node-capacity checks layer on top of this elsewhere.
func canPlaceOnNode(state *AppState, r *http.Request, node *models.Node) bool {
	if IsAdmin(r) {
		return true
	}
	if byonActive(state, r) && node != nil && node.OwnerID != nil {
		uid := byonCallerID(r)
		return uid != "" && *node.OwnerID == uid
	}
	return false
}
