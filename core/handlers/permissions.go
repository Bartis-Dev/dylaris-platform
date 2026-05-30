package handlers

import (
	"dylaris-core/models"
)

// EffectivePermissions is the materialized set of capability flags for one user.
// Admins always get everything; regular users get what their role + per-user
// flags allow. The Role field reflects the canonical role string ("user" /
// "support" / "admin") so downstream code doesn't have to recompute it.
//
// Compute once per request and pass around instead of re-reading the user
// row in every gate — both cheaper and avoids stale-state races.
type EffectivePermissions struct {
	Role                string
	IsAdmin             bool
	IsSupport           bool
	CanAccessAllRegions bool
	AllowedRegions      []string
	CanDeleteServers    bool
	CanChangeResources  bool
}

// ComputeEffectivePermissions materializes the permissions for a user.
// Admin-shortcut: an admin is_admin or role=='admin' grants everything,
// regardless of per-user flag values. This matches the legacy contract
// where is_admin was the only gate.
func ComputeEffectivePermissions(user *models.User, allowedRegions []string) EffectivePermissions {
	if user == nil {
		return EffectivePermissions{Role: "user"}
	}
	role := user.Role
	if role == "" {
		role = "user"
	}
	if user.IsAdmin || role == "admin" {
		return EffectivePermissions{
			Role:                "admin",
			IsAdmin:             true,
			CanAccessAllRegions: true,
			CanDeleteServers:    true,
			CanChangeResources:  true,
		}
	}
	return EffectivePermissions{
		Role:                role,
		IsAdmin:             false,
		IsSupport:           role == "support",
		CanAccessAllRegions: user.AllRegionsAccess,
		AllowedRegions:      allowedRegions,
		CanDeleteServers:    user.CanDeleteServers,
		CanChangeResources:  user.CanChangeResources,
	}
}

// CanAccessRegion is the single gate used by region-aware list/show endpoints.
// Admins and all-regions users pass everything; explicit-region users get a
// set-membership check.
func (p EffectivePermissions) CanAccessRegion(regionID string) bool {
	if p.CanAccessAllRegions {
		return true
	}
	if regionID == "" {
		// Defensive: a server with no region (shouldn't happen post-migration
		// since the column defaults to 'default') is treated as default.
		regionID = "default"
	}
	for _, r := range p.AllowedRegions {
		if r == regionID {
			return true
		}
	}
	return false
}

// FilterServersByRegion drops servers whose region the user can't access.
// In-place semantics on a copy — pass the original slice safely.
func FilterServersByRegion(servers []models.Server, p EffectivePermissions) []models.Server {
	if p.CanAccessAllRegions {
		return servers
	}
	out := make([]models.Server, 0, len(servers))
	for _, s := range servers {
		if p.CanAccessRegion(s.Region) {
			out = append(out, s)
		}
	}
	return out
}

// LoadEffectivePermissions is a convenience for handlers: fetches the user
// + their region IDs, then computes the permissions. Returns zero-value
// permissions (role="user", everything denied) if the lookup fails.
func LoadEffectivePermissions(state *AppState, userID string) EffectivePermissions {
	if state == nil || state.Store == nil || userID == "" {
		return EffectivePermissions{Role: "user"}
	}
	user, err := state.Store.GetUserByID(userID)
	if err != nil || user == nil {
		return EffectivePermissions{Role: "user"}
	}
	regions, _ := state.Store.GetUserRegionIDs(userID)
	return ComputeEffectivePermissions(user, regions)
}
