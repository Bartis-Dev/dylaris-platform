package handlers

import (
	"encoding/json"
	"log"
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
	// ModpackAuthoring opens modpack authoring to non-admin users. Meaningless
	// on its own: the subsystem must be on (Modpacks) for anyone, admin or not,
	// to author at all.
	ModpackAuthoring bool `json:"modpackAuthoring"`
	// ApplyAuthoringToManual decides what a change to ModpackAuthoring does to
	// users whose per-user flag an admin set BY HAND. false (the safe default)
	// leaves those rows alone; true overwrites them and drops their manual
	// marker, so they follow the global switch again from here on. Write-only:
	// it is an instruction for this request, not stored state.
	ApplyAuthoringToManual bool `json:"applyAuthoringToManual"`
	AutoMove               bool `json:"autoMove"`
	Byon                   bool `json:"byon"`
}

// Get GET /api/admin/settings/features — current bundle of platform toggles.
// PANEL settings.read (RequireCap at the route).
func (h *FeatureSettingsHandler) Get(w http.ResponseWriter, r *http.Request) {
	out := featureSettingsPayload{
		Tickets:          h.state.FeatureFlags.IsTicketsEnabled(r.Context()),
		Modpacks:         h.state.FeatureFlags.IsModpacksEnabled(r.Context()),
		ModpackAuthoring: h.state.FeatureFlags.IsModpackAuthoringEnabled(r.Context()),
		AutoMove:         h.state.FeatureFlags.IsAutoMoveEnabled(r.Context()),
		Byon:             h.state.FeatureFlags.IsBYONEnabled(r.Context()),
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
			"message": "Configure Core file storage (Settings -> Core file storage) before enabling Tickets.",
		})
		return
	}

	// End-user authoring without the subsystem is not a state that means
	// anything: nobody, admin or user, can author while modpacks are off. Rather
	// than store a flag that does nothing (and would quietly take effect the day
	// modpacks are switched on), fold it down here.
	if !req.Modpacks {
		req.ModpackAuthoring = false
	}

	// Whether the authoring flag is actually moving, read BEFORE the writes. The
	// per-user bulk apply only runs on a transition: re-saving the Features form
	// with authoring unchanged must not re-flatten per-user rows.
	authoringWas := h.state.FeatureFlags.IsModpackAuthoringEnabled(r.Context())
	authoringChanged := authoringWas != req.ModpackAuthoring

	writes := []struct {
		key      string
		val      bool
		cacheKey string
		evtName  string
	}{
		{"feature_tickets_enabled", req.Tickets, "feature_tickets_enabled", "tickets"},
		{"feature_modpacks_enabled", req.Modpacks, "feature_modpacks_enabled", "modpacks"},
		{"feature_modpack_authoring_enabled", req.ModpackAuthoring, "feature_modpack_authoring_enabled", "modpackAuthoring"},
		{"feature_auto_move_enabled", req.AutoMove, "feature_auto_move_enabled", "autoMove"},
		{"feature_byon_enabled", req.Byon, "feature_byon_enabled", "byon"},
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

	// Bring the per-user column in line with the new global answer, so the users
	// list shows the truth rather than a stale TRUE default from before the split.
	// Only on a transition, and non-fatal: the flag above is the authoritative
	// gate (RequireUserCanCreateModpacks checks it first), so a failed bulk write
	// leaves the platform correctly gated, just with rows the admin may need to
	// re-apply. Reporting it is better than 500-ing a settings save that already
	// took effect.
	usersChanged := int64(0)
	if authoringChanged {
		n, err := h.state.Store.BulkSetCanCreateModpacks(req.ModpackAuthoring, req.ApplyAuthoringToManual)
		if err != nil {
			log.Printf("features: modpack authoring bulk apply (to=%v includeManual=%v) failed: %v",
				req.ModpackAuthoring, req.ApplyAuthoringToManual, err)
		}
		usersChanged = n
	}

	// Keep the navbar module in step: it appears when the subsystem is on, and
	// widens from admin-only to everyone when authoring opens. Non-fatal for the
	// same reason - the row is presentation, the flags are the gate.
	if err := syncModpackModule(h.state, req.Modpacks, req.ModpackAuthoring); err != nil {
		log.Printf("features: syncing the Modpacks module row failed: %v", err)
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"features": req,
		// How many user rows the authoring toggle actually rewrote, so the admin
		// gets "42 users updated" instead of guessing what the switch did.
		"usersChanged": usersChanged,
	})
}
