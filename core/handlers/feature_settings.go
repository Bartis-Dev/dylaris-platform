package handlers

import (
	"encoding/json"
	"net/http"
)

// FeatureSettingsHandler is the admin GET/PUT bundle for platform-wide
// feature toggles. Keeping all toggles behind a single endpoint avoids
// scattering "feature_*_enabled" into per-feature settings handlers and
// gives the panel a single round-trip to render the Features admin tab.
type FeatureSettingsHandler struct {
	state *AppState
}

func NewFeatureSettingsHandler(state *AppState) *FeatureSettingsHandler {
	return &FeatureSettingsHandler{state: state}
}

// featureSettingsPayload is the wire shape of GET + PUT. New platform-wide
// feature flags get added here as we ship them.
type featureSettingsPayload struct {
	Tickets  bool `json:"tickets"`
	Modpacks bool `json:"modpacks"`
	AutoMove bool `json:"autoMove"`
}

// Get GET /api/admin/settings/features — current bundle of platform toggles.
// PANEL settings.read (RequireCap at the route).
func (h *FeatureSettingsHandler) Get(w http.ResponseWriter, r *http.Request) {
	out := featureSettingsPayload{
		Tickets:  h.state.FeatureFlags.IsTicketsEnabled(r.Context()),
		Modpacks: h.state.FeatureFlags.IsModpacksEnabled(r.Context()),
		AutoMove: h.state.FeatureFlags.IsAutoMoveEnabled(r.Context()),
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"features": out,
	})
}

// Set PUT /api/admin/settings/features — write the bundle. Each setting key
// is persisted, its cached flag is invalidated, and a features.changed event
// is published so the panel re-renders banners / nav gating.
// PANEL settings.write (RequireCap at the route).
func (h *FeatureSettingsHandler) Set(w http.ResponseWriter, r *http.Request) {
	var req featureSettingsPayload
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// Auto-move only works while the gateway is routing (it's what keeps a
	// server's address stable across a node change). Refuse the whole PUT
	// rather than partially apply, so the admin gets a single clear error and
	// no flag is half-written.
	if req.AutoMove && !h.state.gatewayEnabled() {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "gateway_required",
			"message": "Auto-move can only be enabled while gateway routing is active.",
		})
		return
	}

	// Enabling Tickets requires Core file storage (attachments + backups need a
	// durable off-host home). Refuse the whole PUT so nothing is half-written.
	if req.Tickets && !h.state.CoreStorageConfigured() {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "core_storage_required",
			"message": "Configure Core file storage before enabling Tickets.",
		})
		return
	}

	writes := []struct {
		key      string
		val      bool
		cacheKey string
		evtName  string
	}{
		{"feature_tickets_enabled", req.Tickets, "feature_tickets_enabled", "tickets"},
		{"feature_modpacks_enabled", req.Modpacks, "feature_modpacks_enabled", "modpacks"},
		{"feature_auto_move_enabled", req.AutoMove, "feature_auto_move_enabled", "autoMove"},
	}
	for _, kv := range writes {
		if err := h.state.Store.SetSetting(kv.key, boolStr(kv.val)); err != nil {
			sendJSONError(w, "Save failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		h.state.FeatureFlags.Invalidate(kv.cacheKey)
		h.state.Events.Publish(r.Context(), "features.changed", map[string]interface{}{
			"feature": kv.evtName,
			"enabled": kv.val,
		})
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"features": req,
	})
}
