package handlers

import (
	"encoding/json"
	"net/http"
)

// gatewayEnabled reports whether platform routing currently uses the gateway.
// routing_mode "gateway" and "both" both run the gateway; "ip_port" (the
// default, also the value used on empty/error) does not.
func (s *AppState) gatewayEnabled() bool {
	mode, _ := s.Store.GetSetting("routing_mode")
	if mode == "" {
		mode = "ip_port"
	}
	return mode == "gateway" || mode == "both"
}

// RequireGatewayEnabled blocks the request with 503 gateway_disabled when the
// platform routing mode is ip_port. Wrap gateway-route WRITE endpoints with
// this so they refuse instead of silently accepting no-op work that the
// (absent) gateway would never apply. Reads + DELETE stay ungated: reads are
// harmless and DELETE must work for cleanup after switching back to ip_port.
func (s *AppState) RequireGatewayEnabled(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.gatewayEnabled() {
			gatewayDisabledResponse(w)
			return
		}
		next(w, r)
	}
}

// gatewayDisabledResponse writes the canonical 503 + machine-readable body,
// mirroring featureDisabledResponse.
func gatewayDisabledResponse(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusServiceUnavailable)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": false,
		"error":   "gateway_disabled",
		"message": "Gateway routing is disabled; switch routing mode to gateway or both to manage routes.",
	})
}
