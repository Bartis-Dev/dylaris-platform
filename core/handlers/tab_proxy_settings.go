package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
)

// TabProxySettingsHandler is the admin GET/PUT bundle for the WS5 custom-tab
// reverse-proxy toggles. Mirrors FeatureSettingsHandler.
type TabProxySettingsHandler struct {
	state *AppState
}

func NewTabProxySettingsHandler(state *AppState) *TabProxySettingsHandler {
	return &TabProxySettingsHandler{state: state}
}

type tabProxySettings struct {
	Enabled              bool `json:"enabled"`
	AllowPublicLinks     bool `json:"allowPublicLinks"`
	MaxPerServer         int  `json:"maxPerServer"`
	MaxShareLinksPerUser int  `json:"maxShareLinksPerUser"`
}

// Get GET /api/admin/settings/tab-proxy
func (h *TabProxySettingsHandler) Get(w http.ResponseWriter, r *http.Request) {
	if !IsAdmin(r) {
		sendJSONError(w, "Admin only", http.StatusForbidden)
		return
	}
	ctx := r.Context()
	out := tabProxySettings{
		Enabled:              h.state.FeatureFlags.IsTabProxyEnabled(ctx),
		AllowPublicLinks:     h.state.FeatureFlags.TabProxyAllowPublicLinks(ctx),
		MaxPerServer:         h.state.FeatureFlags.TabProxyMaxPerServer(ctx),
		MaxShareLinksPerUser: h.state.FeatureFlags.TabProxyMaxShareLinksPerUser(ctx),
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "settings": out})
}

// Set PUT /api/admin/settings/tab-proxy
func (h *TabProxySettingsHandler) Set(w http.ResponseWriter, r *http.Request) {
	if !IsAdmin(r) {
		sendJSONError(w, "Admin only", http.StatusForbidden)
		return
	}
	var req tabProxySettings
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	if req.MaxPerServer <= 0 {
		req.MaxPerServer = 10
	}
	if req.MaxShareLinksPerUser <= 0 {
		req.MaxShareLinksPerUser = 20
	}
	writes := []struct{ k, v string }{
		{"feature_tab_proxy_enabled", boolStr(req.Enabled)},
		{"tab_proxy_allow_public_links", boolStr(req.AllowPublicLinks)},
		{"tab_proxy_max_per_server", strconv.Itoa(req.MaxPerServer)},
		{"tab_proxy_max_share_links_per_user", strconv.Itoa(req.MaxShareLinksPerUser)},
	}
	for _, kv := range writes {
		if err := h.state.Store.SetSetting(kv.k, kv.v); err != nil {
			sendJSONError(w, "Save failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		h.state.FeatureFlags.Invalidate(kv.k)
	}
	h.state.Events.Publish(r.Context(), "features.changed", map[string]interface{}{
		"feature": "tabProxy",
		"enabled": req.Enabled,
	})
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "settings": req})
}
