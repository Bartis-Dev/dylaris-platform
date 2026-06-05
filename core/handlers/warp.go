package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"dylaris-core/services"
	"dylaris-core/store"
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
	var req struct {
		PublicKey         string   `json:"public_key"`
		RequestedHostname string   `json:"requested_hostname"`
		TunnelSubnets     []string `json:"tunnel_subnets"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.PublicKey == "" {
		sendJSONError(w, "Invalid request (public_key required)", http.StatusBadRequest)
		return
	}

	res, err := h.svc.Enroll(r.Context(), key, req.PublicKey, req.TunnelSubnets)
	if err != nil {
		sendJSONError(w, err.Error(), http.StatusConflict)
		return
	}

	leaderPub, err := h.svc.LeaderPublicKey()
	if err != nil {
		sendJSONError(w, "leader key error", http.StatusInternalServerError)
		return
	}
	endpoint, _ := h.state.Store.GetSetting("warp:leader_endpoint")
	res.LeaderPublicKey = leaderPub
	res.LeaderEndpoint = endpoint
	res.Keepalive = 25

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
		MaxConns: req.MaxConns, OnNewConn: req.OnNewConn, FixedWGIP: req.FixedWGIP, NodeID: req.NodeID,
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

// GetSettings returns warp client_subnet + leader_endpoint.
func (h *WarpHandler) GetSettings(w http.ResponseWriter, r *http.Request) {
	subnet, _ := h.state.Store.GetSetting("warp:client_subnet")
	if subnet == "" {
		subnet = "10.0.99.0/24"
	}
	endpoint, _ := h.state.Store.GetSetting("warp:leader_endpoint")
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true, "clientSubnet": subnet, "leaderEndpoint": endpoint,
	})
}

// SaveSettings (admin) persists + mirrors warp settings to Redis.
func (h *WarpHandler) SaveSettings(w http.ResponseWriter, r *http.Request) {
	if !IsAdmin(r) {
		sendJSONError(w, "Admin only", http.StatusForbidden)
		return
	}
	var req struct {
		ClientSubnet   string `json:"clientSubnet"`
		LeaderEndpoint string `json:"leaderEndpoint"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid request", http.StatusBadRequest)
		return
	}
	if req.ClientSubnet != "" {
		if err := h.state.Store.SetSetting("warp:client_subnet", req.ClientSubnet); err != nil {
			sendJSONError(w, "Failed to save", http.StatusInternalServerError)
			return
		}
		h.state.Redis.Set(r.Context(), "dylaris:warp:client_subnet", req.ClientSubnet, 0)
	}
	if err := h.state.Store.SetSetting("warp:leader_endpoint", req.LeaderEndpoint); err != nil {
		sendJSONError(w, "Failed to save", http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}
