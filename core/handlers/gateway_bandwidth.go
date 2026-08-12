package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"dylaris-core/services"
)

// GatewayBandwidthHandler serves the gateway bandwidth dashboard: a live overview
// from the F0 Redis mirror and history from gateway_bandwidth_stats. Admin-gated
// (settings.read) at the route.
type GatewayBandwidthHandler struct {
	state *AppState
}

func NewGatewayBandwidthHandler(state *AppState) *GatewayBandwidthHandler {
	return &GatewayBandwidthHandler{state: state}
}

// GetOverview GET /api/gateway-bandwidth/overview
// Live per-component + per-host utilisation and sustained-threshold alerts.
func (h *GatewayBandwidthHandler) GetOverview(w http.ResponseWriter, r *http.Request) {
	ov := services.LoadGatewayBandwidthOverview(r.Context(), h.state.Redis, h.state.Store, time.Now())
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ov)
}

// GetHistory GET /api/gateway-bandwidth/history?range=24h&host=&component=
// Time-bucketed summed throughput from gateway_bandwidth_stats.
func (h *GatewayBandwidthHandler) GetHistory(w http.ResponseWriter, r *http.Request) {
	if h.state.Store == nil {
		sendJSONError(w, "Service unavailable", http.StatusServiceUnavailable)
		return
	}
	duration := 24 * time.Hour
	switch r.URL.Query().Get("range") {
	case "1h":
		duration = time.Hour
	case "6h":
		duration = 6 * time.Hour
	case "12h":
		duration = 12 * time.Hour
	}
	host := r.URL.Query().Get("host")
	component := r.URL.Query().Get("component")
	since := time.Now().Add(-duration)

	rows, err := h.state.Store.GetGatewayBandwidthHistory(since, component, host)
	if err != nil {
		rows = nil
	}
	points := services.AggregateHostHistory(rows)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"points": points})
}
