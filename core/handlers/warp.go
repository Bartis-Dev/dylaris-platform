package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

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

// MintLinkKit (tenant) creates a route-only "link kit": a warp enrollment key
// bound to the calling user plus an auto-generated link identity (node_id). The
// customer runs warp (joins the overlay) + link (tunnels their LOCAL server out
// through warp) — no managed node. Returns the plaintext warp key, the link
// identity and the DERIVED link token ONCE. The customer sets the link token as
// the link's AGENT_SECRET, so the cluster secret never leaves Core (a tenant must
// never be able to derive another tenant's link token).
func (h *WarpHandler) MintLinkKit(w http.ResponseWriter, r *http.Request) {
	if !byonActive(h.state, r) {
		sendJSONError(w, "BYON is not enabled", http.StatusForbidden)
		return
	}
	// Route-only needs the overlay: warp enroll + link tunnel only exist in
	// gateway/both routing mode. Match Enroll, which refuses in ip_port mode.
	if !h.state.gatewayEnabled() || h.state.Gateway == nil {
		sendJSONError(w, "Gateway routing is disabled; enable gateway or both mode first.", http.StatusConflict)
		return
	}
	userID := byonCallerID(r)
	if userID == "" {
		sendJSONError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	// Enforce the tenant's link cap (0 = unlimited, mirrors max_nodes).
	lim, lerr := services.EffectiveLimits(h.state.Store, userID)
	if lerr != nil {
		sendJSONError(w, "Failed to resolve limits", http.StatusInternalServerError)
		return
	}
	if lim.MaxLinks > 0 {
		used, cerr := h.state.Store.CountLinkKitsByOwner(userID)
		if cerr != nil {
			sendJSONError(w, "Failed to count links", http.StatusInternalServerError)
			return
		}
		if int64(used) >= lim.MaxLinks {
			sendJSONError(w, fmt.Sprintf("Link limit reached (%d)", lim.MaxLinks), http.StatusForbidden)
			return
		}
	}
	var req struct {
		Name string `json:"name"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "Route-only link"
	}

	nodeID, err := generateLinkIdentity()
	if err != nil {
		sendJSONError(w, "Failed to generate link identity", http.StatusInternalServerError)
		return
	}
	plaintext, err := generatePlaintextKey()
	if err != nil {
		sendJSONError(w, "Failed to generate key", http.StatusInternalServerError)
		return
	}
	// general policy + single connection: one customer machine, one warp peer.
	// kill_old so a restart re-enrolls cleanly instead of hitting the limit.
	if _, err := h.state.Store.CreateWarpAPIKey(store.WarpAPIKey{
		Name:      name,
		KeyHash:   HashAPIKey(plaintext),
		Policy:    "general",
		MaxConns:  1,
		OnNewConn: "kill_old",
		NodeID:    nodeID,
		OwnerID:   userID,
	}); err != nil {
		sendJSONError(w, "Failed to create link kit", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":    true,
		"warp_key":   plaintext,
		"link_id":    nodeID,
		"link_token": h.state.Gateway.LinkToken(nodeID),
		"note":       "Shown once. Deploy warp with the warp key, and link with the link token as its AGENT_SECRET.",
	})
}

// LinkBoot POST /api/warp/link-boot - a route-only link presents its warp key and
// receives its derived tunnel token plus a Redis credential scoped to its own keys.
// WarpAPIKeyMiddleware has already rejected an unknown or revoked key. The response
// is never logged.
func (h *WarpHandler) LinkBoot(w http.ResponseWriter, r *http.Request) {
	key, ok := r.Context().Value(warpKeyCtx).(store.WarpAPIKey)
	if !ok {
		sendJSONError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	// A BYON node's warp key must never mint a link credential.
	if !strings.HasPrefix(key.NodeID, "link-") {
		sendJSONError(w, "Not a route-only link key", http.StatusForbidden)
		return
	}
	if !h.state.gatewayEnabled() || h.state.Gateway == nil {
		sendJSONError(w, "Gateway routing is disabled", http.StatusConflict)
		return
	}
	// A suspended tenant's link cannot boot. GetUserBilling never returns
	// sql.ErrNoRows; a missing row yields Status "active". A real DB fault fails
	// OPEN so a transient blip does not lock out paying tenants on reconnect, but
	// it must be visible: the suspension gate is silently degraded for that window.
	b, berr := h.state.Store.GetUserBilling(key.OwnerID)
	if berr != nil {
		log.Printf("link-boot: billing lookup for %s failed, suspension gate skipped: %v", key.NodeID, berr)
	} else if b != nil && b.Status == "suspended" {
		sendJSONError(w, "Account suspended", http.StatusForbidden)
		return
	}
	tunnelToken := h.state.Gateway.LinkToken(key.NodeID)
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	user, pass, err := h.state.ACLProvisioner.EnsureRouteOnlyLinkACL(ctx, h.state.ClusterSecret, key.NodeID, tunnelToken)
	if err != nil {
		log.Printf("link-boot: provision ACL for %s failed: %v", key.NodeID, err)
		sendJSONError(w, "Failed to provision credentials", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":    true,
		"link_id":    key.NodeID,
		"link_token": tunnelToken,
		"redis_user": user,
		"redis_pass": pass,
		"redis_db":   h.state.Redis.Options().DB,
	})
}

// ListLinkKits (tenant) returns the caller's route-only link kits (metadata only,
// no secrets). The link token is re-derivable from link_id, so it is re-revealed
// via the kit's reveal flow, not listed here.
func (h *WarpHandler) ListLinkKits(w http.ResponseWriter, r *http.Request) {
	if !byonActive(h.state, r) {
		sendJSONError(w, "BYON is not enabled", http.StatusForbidden)
		return
	}
	userID := byonCallerID(r)
	if userID == "" {
		sendJSONError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	keys, err := h.state.Store.ListWarpAPIKeysByOwner(userID)
	if err != nil {
		sendJSONError(w, "Failed to load link kits", http.StatusInternalServerError)
		return
	}
	type linkKit struct {
		ID        int    `json:"id"`
		Name      string `json:"name"`
		LinkID    string `json:"link_id"`
		CreatedAt string `json:"created_at"`
	}
	out := make([]linkKit, 0, len(keys))
	for _, k := range keys {
		out = append(out, linkKit{ID: k.ID, Name: k.Name, LinkID: k.NodeID, CreatedAt: k.CreatedAt.Format("2006-01-02T15:04:05Z07:00")})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "kits": out})
}

// RevokeLinkKit DELETE /api/warp/link-kits/{linkID} - owner or admin. Tears the
// route-only link down end to end: marks the warp key revoked so it can never
// re-enroll or boot again, drops its scoped Redis ACL user (ACL DELUSER terminates
// its live connections), deletes its tunnel key so the edge closes the tunnel within
// 30s, and removes its Core-owned routes. Order matters twice: the durable revoke
// comes first so a partial failure cannot undo itself, and the ACL user is dropped
// before the tunnel key so a restart cannot re-register (see the design doc).
func (h *WarpHandler) RevokeLinkKit(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value("userID").(string)
	isAdmin, _ := r.Context().Value("isAdmin").(bool)
	if userID == "" {
		sendJSONError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	linkID := mux.Vars(r)["linkID"]
	if !strings.HasPrefix(linkID, "link-") {
		sendJSONError(w, "Invalid link id", http.StatusBadRequest)
		return
	}
	key, err := h.state.Store.GetWarpAPIKeyByNodeID(linkID)
	if err != nil {
		sendJSONError(w, "Link not found", http.StatusNotFound)
		return
	}
	if !isAdmin && key.OwnerID != userID {
		sendJSONError(w, "Link not found", http.StatusNotFound)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	tunnelToken := h.state.Gateway.LinkToken(linkID)

	// 1. Revoke the warp key FIRST. It is the only durable step, and it is the one
	// that blocks link-boot. If the teardown below ran first and this failed, the
	// link's next ordinary reconnect would call link-boot, EnsureRouteOnlyLinkACL
	// would idempotently re-provision it, and the revoke would silently undo itself.
	// Failing here aborts before any Redis state changed, so a retry is clean.
	if derr := h.state.Store.RevokeWarpAPIKeyByNodeID(linkID); derr != nil {
		log.Printf("revoke link %s: mark revoked: %v", linkID, derr)
		sendJSONError(w, "Failed to revoke link", http.StatusInternalServerError)
		return
	}

	// The remaining steps are best-effort teardown and are safe to repeat: a retried
	// DELETE re-runs them, because the lookup above does not filter revoked keys.

	// 2. Drop the ACL user before deleting the tunnel key: DELUSER terminates the
	// link's live Redis connections and blocks re-registration on restart, while
	// DEL alone would let a restart re-register. Neither step alone suffices.
	h.state.ACLProvisioner.RemoveRouteOnlyLinkACL(ctx, linkID)
	// 3. Delete the tunnel key so the edge drops the tunnel within 30s.
	if derr := h.state.Redis.Del(ctx, "link:"+tunnelToken).Err(); derr != nil {
		log.Printf("revoke link %s: delete tunnel key: %v", linkID, derr)
	}
	// 4. Remove the link's Core-owned routes so a dead link does not squat domains.
	// GetRoutesFromRedis returns nil on a Redis error, indistinguishable from "no
	// routes", so log the count: a silent zero is the only signal cleanup was skipped.
	removed := 0
	for _, rt := range services.GetRoutesFromRedis(ctx, h.state.Redis) {
		if rt.CoreOwned && rt.OwnerID == key.OwnerID && rt.TunnelID == tunnelToken {
			if derr := h.state.Gateway.DeleteCoreOwnedRoute(rt.Domain); derr != nil {
				log.Printf("revoke link %s: delete route %s: %v", linkID, rt.Domain, derr)
				continue
			}
			removed++
		}
	}
	log.Printf("revoke link %s: revoked, %d route(s) removed", linkID, removed)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
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
