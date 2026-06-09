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
}

// Get GET /api/admin/settings/features — current bundle of platform toggles.
func (h *FeatureSettingsHandler) Get(w http.ResponseWriter, r *http.Request) {
	if !IsAdmin(r) {
		sendJSONError(w, "Admin only", http.StatusForbidden)
		return
	}
	out := featureSettingsPayload{
		Tickets:  h.state.FeatureFlags.IsTicketsEnabled(r.Context()),
		Modpacks: h.state.FeatureFlags.IsModpacksEnabled(r.Context()),
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"features": out,
	})
}

// Set PUT /api/admin/settings/features — write the bundle. Each setting key
// is persisted, its cached flag is invalidated, and a features.changed event
// is published so the panel re-renders banners / nav gating.
func (h *FeatureSettingsHandler) Set(w http.ResponseWriter, r *http.Request) {
	if !IsAdmin(r) {
		sendJSONError(w, "Admin only", http.StatusForbidden)
		return
	}
	var req featureSettingsPayload
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", http.StatusBadRequest)
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
