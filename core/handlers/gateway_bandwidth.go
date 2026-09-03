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

// bandwidthRange maps a range name to the window it covers and the bucket the
// points are reduced onto.
//
// The steps are chosen so every range lands at roughly a hundred points per
// series: enough to see the shape, few enough that six components plus their
// hosts stay one small response. 15m is deliberately raw - the persist cadence
// is 30 seconds, so there is nothing to reduce, and the shortest range is the
// one somebody opens to watch a spike as it happens.
//
// 24h is the ceiling because gateway_bandwidth_stats keeps 24 hours (a
// TimescaleDB retention policy, or the hourly DELETE sweep on plain Postgres).
// Asking for more would return a window that is simply empty at its start.
func bandwidthRange(name string) (window, step time.Duration) {
	switch name {
	case "15m":
		return 15 * time.Minute, 0
	case "1h":
		return time.Hour, time.Minute
	case "6h":
		return 6 * time.Hour, 5 * time.Minute
	case "12h":
		return 12 * time.Hour, 10 * time.Minute
	default:
		return 24 * time.Hour, 15 * time.Minute
	}
}

// GetHistory GET /api/gateway-bandwidth/history?range=24h&host=&component=
//
// Every series the bandwidth screen draws, in one response: one per component
// and one per host. It used to return a single pre-summed series, which is why
// the screen could only chart one host at a time and had nothing to draw a
// per-component sparkline from.
func (h *GatewayBandwidthHandler) GetHistory(w http.ResponseWriter, r *http.Request) {
	if h.state.Store == nil {
		sendJSONError(w, "Service unavailable", http.StatusServiceUnavailable)
		return
	}
	window, step := bandwidthRange(r.URL.Query().Get("range"))
	host := r.URL.Query().Get("host")
	component := r.URL.Query().Get("component")

	rows, err := h.state.Store.GetGatewayBandwidthHistory(time.Now().Add(-window), component, host)
	if err != nil {
		rows = nil
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(services.BuildBandwidthHistory(rows, step))
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
