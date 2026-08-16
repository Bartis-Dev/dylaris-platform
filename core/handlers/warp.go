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
		if errors.Is(err, services.ErrInvalidFixedWGIP) || errors.Is(err, store.ErrWarpIPTaken) {
			sendJSONError(w, err.Error(), http.StatusConflict)
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

// Assignment handles GET /api/warp/assignment?public_key=... - the client's
// lightweight poll for its current endpoint order (warp API-key auth). It never
// changes the peer's region; it only reflects the stored home leader.
func (h *WarpHandler) Assignment(w http.ResponseWriter, r *http.Request) {
	key, ok := r.Context().Value(warpKeyCtx).(store.WarpAPIKey)
	if !ok {
		sendJSONError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	pubkey := strings.TrimSpace(r.URL.Query().Get("public_key"))
	if pubkey == "" {
		sendJSONError(w, "public_key required", http.StatusBadRequest)
		return
	}
	res, err := h.svc.Assignment(r.Context(), key, pubkey)
	if err != nil {
		if errors.Is(err, services.ErrWarpPeerNotFound) {
			sendJSONError(w, "Peer not found", http.StatusNotFound)
			return
		}
		log.Printf("warp assignment failed (key=%d): %v", key.ID, err)
		sendJSONError(w, "Assignment failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}

// MintAPIKey (admin) creates a warp enrollment key and returns the plaintext ONCE.
func (h *WarpHandler) MintAPIKey(w http.ResponseWriter, r *http.Request) {
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
	// A pinned fixed overlay IP must be a legitimate host inside a known region's
	// subnet. Reject early so a junk key is never stored (the enroll path re-checks).
	if req.FixedWGIP != "" {
		if req.Region == "" {
			sendJSONError(w, "fixed_wg_ip requires a region", http.StatusBadRequest)
			return
		}
		reg, rerr := h.state.Store.GetWarpRegion(req.Region)
		if rerr != nil || reg == nil {
			sendJSONError(w, "Unknown region for fixed_wg_ip", http.StatusBadRequest)
			return
		}
		if verr := services.ValidateFixedWGIP(req.FixedWGIP, reg.Subnet); verr != nil {
			sendJSONError(w, verr.Error(), http.StatusBadRequest)
			return
		}
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
// through warp) — no managed node. Returns the plaintext warp key and the link
// identity ONCE; it does NOT return the link token. That same key doubles as the
// link's LINK_BOOT_KEY: the link exchanges it at /api/warp/link-boot for its
// derived token at boot, so the token never travels with the kit and the cluster
// secret never leaves Core (a tenant must never be able to derive another
// tenant's link token).
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
		"success":  true,
		"warp_key": plaintext,
		"link_id":  nodeID,
		"note":     "Shown once. Paste WARP_API_KEY into the route-only .env.",
	})
}

// linkHardSuspended reports whether a tenant's suspension has persisted past the
// enforcement grace, i.e. the point at which route-only links are actually cut
// off. A nil billing row, an active/past_due status, or a suspension still inside
// the grace window all return false (the link may boot). MUST stay equivalent to
// the SQL predicate in store.ListLinkKitsForACLReconcile (suspended AND
// suspended_at + grace <= now) so LinkBoot and the reconciler agree.
func linkHardSuspended(b *store.UserBilling, grace time.Duration, now time.Time) bool {
	return b != nil && b.Status == "suspended" &&
		b.SuspendedAt != nil && !now.Before(b.SuspendedAt.Add(grace))
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
	// A HARD-suspended tenant's link (suspended past the grace) cannot boot.
	// GetUserBilling never returns sql.ErrNoRows; a missing row yields Status
	// "active". A real DB fault fails OPEN so a transient blip does not lock out
	// paying tenants on reconnect, but it must be visible: the suspension gate is
	// silently degraded for that window. Within the grace a suspended tenant still
	// boots, consistent with the soft-enforcement posture.
	b, berr := h.state.Store.GetUserBilling(key.OwnerID)
	if berr != nil {
		log.Printf("link-boot: billing lookup for %s failed, suspension gate skipped: %v", key.NodeID, berr)
	} else if linkHardSuspended(b, h.state.SuspendGrace, time.Now()) {
		sendJSONError(w, "Account suspended", http.StatusForbidden)
		return
	}
	// Re-check revoked_at with a fresh read immediately before provisioning, to
	// shrink the TOCTOU window against a concurrent RevokeLinkKit: the middleware
	// already checked revoked_at once, but a revoke racing between that check and
	// this point could otherwise have EnsureRouteOnlyLinkACL resurrect the just-
	// deleted ACL user. The reconciler's cleanup sweep (acl_reconciler.go) is the
	// robust backstop regardless (self-heals within ~60s even if this race is lost).
	if fresh, ferr := h.state.Store.GetWarpAPIKeyByNodeID(key.NodeID); ferr != nil {
		log.Printf("link-boot: fresh revoke check for %s failed, proceeding: %v", key.NodeID, ferr)
	} else if fresh.RevokedAt != nil {
		sendJSONError(w, "Key revoked", http.StatusUnauthorized)
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
	// Surface the effective link cap + current link-kit count so the panel can show
	// "X of Y links used". 0 limit means unlimited.
	lim, _ := services.EffectiveLimits(h.state.Store, userID)
	used, _ := h.state.Store.CountLinkKitsByOwner(userID)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "kits": out, "used": used, "limit": lim.MaxLinks})
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

	// Durable-first revoke, ACL dropped before the tunnel key, routes cleaned up
	// last - shared with the admin force-suspend path (billing_lifecycle.go
	// SuspendNow) so the ordering has one source of truth. See its doc comment.
	removed, rerr := services.RevokeLinkKitTeardown(ctx, h.state.Store, h.state.Gateway, h.state.Redis, h.state.ACLProvisioner, linkID, key.OwnerID)
	if rerr != nil {
		log.Printf("revoke link %s: %v", linkID, rerr)
		sendJSONError(w, "Failed to revoke link", http.StatusInternalServerError)
		return
	}
	log.Printf("revoke link %s: revoked, %d route(s) removed", linkID, removed)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}

// ListRegions returns the full warp registry (regions + leaders + liveness +
// peer counts) for the admin panel.
func (h *WarpHandler) ListRegions(w http.ResponseWriter, r *http.Request) {
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
	// Mirror to Redis for the warp leader's boot resolve. Best-effort: the DB write
	// above is the source of truth, and the boot backfill self-heals a missed write.
	if err := services.PublishRegionSubnet(r.Context(), h.state.Redis, req.Region, req.Subnet); err != nil {
		log.Printf("warp region subnet mirror failed for %s: %v", req.Region, err)
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}

// DeleteRegion (admin) removes a region (cascades its leaders).
func (h *WarpHandler) DeleteRegion(w http.ResponseWriter, r *http.Request) {
	region := mux.Vars(r)["region"]
	if err := h.state.Store.DeleteWarpRegion(region); err != nil {
		sendJSONError(w, "Failed to delete region", http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}

// UpsertLeader (admin) creates or updates a leader endpoint within a region.
func (h *WarpHandler) UpsertLeader(w http.ResponseWriter, r *http.Request) {
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
	leaderID := mux.Vars(r)["leaderId"]
	if err := h.state.Store.DeleteWarpLeader(leaderID); err != nil {
		sendJSONError(w, "Failed to delete leader", http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}
