package handlers

import (
	"encoding/json"
	"log"
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

// GetRebalance handles GET /api/gateway-bandwidth/rebalance - the F3 rebalancer
// mode + recent decision feed. Admin-gated at the route (settings.read).
func (h *GatewayBandwidthHandler) GetRebalance(w http.ResponseWriter, r *http.Request) {
	view := services.LoadRebalanceView(r.Context(), h.state.Redis, h.state.FeatureFlags)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(view)
}

// SetRebalanceMode handles POST /api/gateway-bandwidth/rebalance - sets the warp
// rebalancer mode (off | dry-run | armed). Admin-gated (settings.write). The
// worker re-reads the setting each tick, so a change takes effect within one
// evaluation interval.
func (h *GatewayBandwidthHandler) SetRebalanceMode(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Mode string `json:"mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid request", http.StatusBadRequest)
		return
	}
	switch req.Mode {
	case "off", "dry-run", "armed":
	default:
		sendJSONError(w, "mode must be off, dry-run, or armed", http.StatusBadRequest)
		return
	}
	if err := h.state.Store.SetSetting("warp_rebalance_mode", req.Mode); err != nil {
		log.Printf("set warp_rebalance_mode: %v", err)
		sendJSONError(w, "Failed to save mode", http.StatusInternalServerError)
		return
	}
	h.state.FeatureFlags.Invalidate("warp_rebalance_mode")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "mode": req.Mode})
}
