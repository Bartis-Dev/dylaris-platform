package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"strings"

	"dylaris-core/services"
	"dylaris-core/store"

	"github.com/gorilla/mux"
)

type warpCtxKey string

const warpKeyCtx warpCtxKey = "warpAPIKey"

// WarpHandler serves enrollment + admin warp endpoints.
type WarpHandler struct {
	state *AppState
	svc   *services.WarpService
}

func NewWarpHandler(state *AppState, svc *services.WarpService) *WarpHandler {
	return &WarpHandler{state: state, svc: svc}
}

// WarpAPIKeyMiddleware authenticates a Bearer warp API key (separate from user
// sessions) and stuffs the resolved key into the request context.
func (h *WarpHandler) WarpAPIKeyMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			sendJSONError(w, "Missing Authorization", http.StatusUnauthorized)
			return
		}
		plaintext := strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
		key, err := h.state.Store.GetWarpAPIKeyByHash(HashAPIKey(plaintext))
		if err != nil {
			sendJSONError(w, "Invalid warp key", http.StatusUnauthorized)
			return
		}
		if key.RevokedAt != nil {
			sendJSONError(w, "Key revoked", http.StatusUnauthorized)
			return
		}
		ctx := context.WithValue(r.Context(), warpKeyCtx, *key)
		next(w, r.WithContext(ctx))
	}
}

// Enroll handles POST /api/warp/enroll.
func (h *WarpHandler) Enroll(w http.ResponseWriter, r *http.Request) {
	key, ok := r.Context().Value(warpKeyCtx).(store.WarpAPIKey)
	if !ok {
		sendJSONError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Warp peers are only reachable through the gateway overlay; with platform
	// routing in ip_port mode there is nothing to enroll into. Refuse here so
	// home nodes don't enroll into a dormant overlay (matches the panel, which
	// only offers the mint-key button when routing is gateway+beam).
	if !h.state.gatewayEnabled() {
		sendJSONError(w, "Gateway routing is disabled; enable gateway or both mode before enrolling warp peers.", http.StatusConflict)
		return
	}
	var req struct {
		PublicKey     string   `json:"public_key"`
		TunnelSubnets []string `json:"tunnel_subnets"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.PublicKey == "" {
		sendJSONError(w, "Invalid request (public_key required)", http.StatusBadRequest)
		return
	}

	res, err := h.svc.Enroll(r.Context(), key, req.PublicKey, req.TunnelSubnets)
	if err != nil {
		// 409 only for a genuine connection-limit conflict; everything else is
		// a server-side fault (no region configured, DB, IP allocation, leader
		// key) — surface it as 500 and log it instead of leaking internals.
		if errors.Is(err, store.ErrWarpLimitReached) {
			sendJSONError(w, "Connection limit reached for this key", http.StatusConflict)
			return
		}
		log.Printf("warp enroll failed (key=%d): %v", key.ID, err)
		sendJSONError(w, "Enrollment failed", http.StatusInternalServerError)
		return
	}
	// The service now fills region subnet, region pubkey and the failover endpoint
	// list, so an idempotent re-enroll can never disagree with a fresh one.
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}

// MintAPIKey (admin) creates a warp enrollment key and returns the plaintext ONCE.
func (h *WarpHandler) MintAPIKey(w http.ResponseWriter, r *http.Request) {
	if !IsAdmin(r) {
		sendJSONError(w, "Admin only", http.StatusForbidden)
		return
	}
	var req struct {
		Name      string `json:"name"`
		Policy    string `json:"policy"`
		MaxConns  int    `json:"max_conns"`
		OnNewConn string `json:"on_new_conn"`
		FixedWGIP string `json:"fixed_wg_ip"`
		NodeID    string `json:"node_id"`
		Region    string `json:"region"` // "" = auto-assign at enroll
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid request", http.StatusBadRequest)
		return
	}
	if req.Policy != "fixed" && req.Policy != "general" {
		req.Policy = "general"
	}
	if req.OnNewConn != "kill_old" && req.OnNewConn != "block" {
		req.OnNewConn = "block"
	}
	if req.MaxConns < 1 {
		req.MaxConns = 1
	}
	plaintext, err := generatePlaintextKey()
	if err != nil {
		sendJSONError(w, "Failed to generate key", http.StatusInternalServerError)
		return
	}
	id, err := h.state.Store.CreateWarpAPIKey(store.WarpAPIKey{
		Name: req.Name, KeyHash: HashAPIKey(plaintext), Policy: req.Policy,
		MaxConns: req.MaxConns, OnNewConn: req.OnNewConn, FixedWGIP: req.FixedWGIP,
		NodeID: req.NodeID, Region: req.Region,
	})
	if err != nil {
		sendJSONError(w, "Failed to create key", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true, "id": id, "api_key": plaintext,
	})
}

// ListRegions returns the full warp registry (regions + leaders + liveness +
// peer counts) for the admin panel.
func (h *WarpHandler) ListRegions(w http.ResponseWriter, r *http.Request) {
	if !IsAdmin(r) {
		sendJSONError(w, "Admin only", http.StatusForbidden)
		return
	}
	regions, err := h.svc.RegionsOverview(r.Context())
	if err != nil {
		sendJSONError(w, "Failed to load regions", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "regions": regions})
}

// UpsertRegion (admin) creates or updates a region's subnet + enabled flag.
func (h *WarpHandler) UpsertRegion(w http.ResponseWriter, r *http.Request) {
	if !IsAdmin(r) {
		sendJSONError(w, "Admin only", http.StatusForbidden)
		return
	}
	var req struct {
		Region  string `json:"region"`
		Subnet  string `json:"subnet"`
		Enabled bool   `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Region == "" || req.Subnet == "" {
		sendJSONError(w, "region and subnet are required", http.StatusBadRequest)
		return
	}
	if _, _, err := net.ParseCIDR(req.Subnet); err != nil {
		sendJSONError(w, "subnet must be a CIDR (e.g. 10.99.1.0/24)", http.StatusBadRequest)
		return
	}
	if err := h.state.Store.UpsertWarpRegion(req.Region, req.Subnet, req.Enabled); err != nil {
		sendJSONError(w, "Failed to save region", http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}

// DeleteRegion (admin) removes a region (cascades its leaders).
func (h *WarpHandler) DeleteRegion(w http.ResponseWriter, r *http.Request) {
	if !IsAdmin(r) {
		sendJSONError(w, "Admin only", http.StatusForbidden)
		return
	}
	region := mux.Vars(r)["region"]
	if err := h.state.Store.DeleteWarpRegion(region); err != nil {
		sendJSONError(w, "Failed to delete region", http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}

// UpsertLeader (admin) creates or updates a leader endpoint within a region.
func (h *WarpHandler) UpsertLeader(w http.ResponseWriter, r *http.Request) {
	if !IsAdmin(r) {
		sendJSONError(w, "Admin only", http.StatusForbidden)
		return
	}
	var req struct {
		LeaderID string `json:"leaderId"`
		Region   string `json:"region"`
		Endpoint string `json:"endpoint"`
		Enabled  bool   `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.LeaderID == "" || req.Region == "" || req.Endpoint == "" {
		sendJSONError(w, "leaderId, region and endpoint are required", http.StatusBadRequest)
		return
	}
	if err := h.state.Store.UpsertWarpLeader(req.LeaderID, req.Region, req.Endpoint, req.Enabled); err != nil {
		sendJSONError(w, "Failed to save leader (does the region exist?)", http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}

// DeleteLeader (admin) removes a leader endpoint.
func (h *WarpHandler) DeleteLeader(w http.ResponseWriter, r *http.Request) {
	if !IsAdmin(r) {
		sendJSONError(w, "Admin only", http.StatusForbidden)
		return
	}
	leaderID := mux.Vars(r)["leaderId"]
	if err := h.state.Store.DeleteWarpLeader(leaderID); err != nil {
		sendJSONError(w, "Failed to delete leader", http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}
