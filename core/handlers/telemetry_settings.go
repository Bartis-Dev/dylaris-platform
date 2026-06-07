package handlers

import (
	"encoding/json"
	"net/http"
)

// TelemetrySettingsHandler exposes the admin-side toggle for the anonymous
// usage-stats heartbeat (Phase 18). Mirrors the ModpackSettingsHandler
// pattern so the panel can read/write a single dedicated endpoint.
type TelemetrySettingsHandler struct {
	state *AppState
}

func NewTelemetrySettingsHandler(state *AppState) *TelemetrySettingsHandler {
	return &TelemetrySettingsHandler{state: state}
}

type telemetrySettings struct {
	Enabled  bool   `json:"enabled"`
	Endpoint string `json:"endpoint"`
}

// Get GET /api/admin/settings/telemetry
func (h *TelemetrySettingsHandler) Get(w http.ResponseWriter, r *http.Request) {
	if !IsAdmin(r) {
		sendJSONError(w, "Admin only", http.StatusForbidden)
		return
	}
	en, _ := h.state.Store.GetSetting("telemetry_enabled")
	ep, _ := h.state.Store.GetSetting("telemetry_endpoint")
	if ep == "" {
		ep = "https://dylaris.dev/api/heartbeat"
	}
	// Default ON: only an explicit "false" disables. Treats empty/missing
	// rows as enabled so the toggle doesn't silently flip off on
	// pre-Phase-18 databases that miss the seed.
	out := telemetrySettings{Enabled: en != "false", Endpoint: ep}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"settings": out,
	})
}

// Set PUT /api/admin/settings/telemetry
func (h *TelemetrySettingsHandler) Set(w http.ResponseWriter, r *http.Request) {
	if !IsAdmin(r) {
		sendJSONError(w, "Admin only", http.StatusForbidden)
		return
	}
	var req telemetrySettings
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	if req.Endpoint == "" {
		req.Endpoint = "https://dylaris.dev/api/heartbeat"
	}
	v := "true"
	if !req.Enabled {
		v = "false"
	}
	if err := h.state.Store.SetSetting("telemetry_enabled", v); err != nil {
		sendJSONError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := h.state.Store.SetSetting("telemetry_endpoint", req.Endpoint); err != nil {
		sendJSONError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]bool{"success": true})
}
