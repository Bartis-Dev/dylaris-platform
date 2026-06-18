package handlers

import (
	"encoding/json"
	"net/http"
)

type SystemFeaturesHandler struct {
	state *AppState
}

func NewSystemFeaturesHandler(state *AppState) *SystemFeaturesHandler {
	return &SystemFeaturesHandler{state: state}
}

// Get GET /api/system/features
//
// Returns the platform-wide feature toggles the panel needs to gate UI
// without hitting per-resource endpoints first. Read-only, auth-required
// (so anonymous browsers can't fingerprint the platform config), no admin
// requirement.
func (h *SystemFeaturesHandler) Get(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"features": map[string]bool{
			"modpacks": h.state.FeatureFlags.IsModpacksEnabled(r.Context()),
			"tickets":  h.state.FeatureFlags.IsTicketsEnabled(r.Context()),
			// Raw admin flag; the panel ANDs it with the live routing mode,
			// since auto-move is only effective while the gateway is on.
			"autoMove": h.state.FeatureFlags.IsAutoMoveEnabled(r.Context()),
			// BYON tenancy: drives tenant-facing UI (e.g. the server transfer
			// control). Read-only flag; the actual authz is enforced backend-side.
			"byon": h.state.FeatureFlags.IsBYONEnabled(r.Context()),
		},
	})
}
