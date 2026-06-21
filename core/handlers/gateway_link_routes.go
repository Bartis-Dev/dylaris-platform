package handlers

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"

	"dylaris-core/services"

	"github.com/gorilla/mux"
)

// Route-only ("via Link") routes: a DDoS-protected address pointed at a server
// the customer runs on their OWN machine, reached through their own outbound Link
// tunnel — no managed node, no exposed origin. The customer runs warp (joins the
// overlay) + link (tunnels out); the edge opens a stream on their Link and the
// Link dials the LOCAL target. Splice + rolling updates work exactly as for a
// managed server, because it uses the same tunnel path. The Link's own
// allow-list (LINK_ALLOWED_TARGETS) is the authority on what it will dial.

// validateLocalTarget sanity-checks a route-only target. The target is dialed by
// the customer's Link on its LOCAL network, so LAN / loopback / private addresses
// are EXPECTED and allowed here — the Link, not Core, decides what it will dial.
// We only reject empty or malformed input.
func validateLocalTarget(host string) error {
	host = strings.TrimSpace(strings.ToLower(host))
	if host == "" {
		return fmt.Errorf("target host is required")
	}
	if net.ParseIP(host) != nil {
		return nil // any IP literal, including LAN / loopback
	}
	// Hostname (e.g. localhost, my-pc.local): basic format check, no wildcard.
	if strings.HasPrefix(host, "*.") || !domainRegex.MatchString(host) {
		return fmt.Errorf("invalid target host")
	}
	return nil
}

// effectiveRouteLimit resolves the per-user route cap. user:{id} override wins
// (0 = disabled); otherwise user_default, then global. hasLimit=false = unlimited.
func (h *GatewayHandler) effectiveRouteLimit(userID string) (int, bool) {
	if l, err := h.state.Store.GetGatewayRouteLimit("user:" + userID); err == nil && l != nil {
		return l.MaxRoutes, true
	}
	if l, err := h.state.Store.GetGatewayRouteLimit("user_default"); err == nil && l != nil && l.MaxRoutes > 0 {
		return l.MaxRoutes, true
	}
	if l, err := h.state.Store.GetGatewayRouteLimit("global"); err == nil && l != nil && l.MaxRoutes > 0 {
		return l.MaxRoutes, true
	}
	return 0, false
}

func (h *GatewayHandler) countOwnerRoutes(userID string) int {
	n := 0
	for _, rt := range services.GetRoutesFromRedis(h.ctx(), h.state.Redis) {
		if rt.OwnerID == userID {
			n++
		}
	}
	return n
}

// resolveOwnedLinkToken confirms the caller owns the link kit identified by
// linkID and returns its derived Link tunnel token. Ownership is enforced by
// only ever looking at the caller's own kits, so a tenant can never point a
// route at another tenant's Link.
func (h *GatewayHandler) resolveOwnedLinkToken(userID, linkID string) (string, error) {
	linkID = strings.TrimSpace(linkID)
	if linkID == "" {
		return "", fmt.Errorf("link is required")
	}
	kits, err := h.state.Store.ListWarpAPIKeysByOwner(userID)
	if err != nil {
		return "", fmt.Errorf("failed to load your links")
	}
	for _, k := range kits {
		if k.NodeID == linkID {
			return h.state.Gateway.LinkToken(linkID), nil
		}
	}
	return "", fmt.Errorf("unknown link")
}

// CreateLinkRoute POST /api/gateway/link-routes — authed, gateway-gated.
func (h *GatewayHandler) CreateLinkRoute(w http.ResponseWriter, r *http.Request) {
	userID := r.Context().Value("userID").(string)

	var req struct {
		LinkID       string `json:"linkId"`
		Domain       string `json:"domain"`
		Subdomain    string `json:"subdomain"`
		HosterDomain string `json:"hosterDomain"`
		CustomDomain string `json:"customDomain"`
		TargetHost   string `json:"targetHost"`
		TargetPort   int    `json:"targetPort"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}
	if req.TargetPort == 0 {
		req.TargetPort = 25565
	}
	if req.TargetPort < 1 || req.TargetPort > 65535 {
		http.Error(w, "Invalid target port", http.StatusBadRequest)
		return
	}
	if err := validateLocalTarget(req.TargetHost); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Resolve (and authorize) the tenant's Link before doing anything else.
	linkToken, err := h.resolveOwnedLinkToken(userID, req.LinkID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Per-user route limit (0 with an explicit override = disabled).
	if limit, has := h.effectiveRouteLimit(userID); has {
		if limit <= 0 {
			http.Error(w, "Route creation is disabled for your account", http.StatusForbidden)
			return
		}
		if h.countOwnerRoutes(userID) >= limit {
			http.Error(w, fmt.Sprintf("Route limit reached (%d)", limit), http.StatusForbidden)
			return
		}
	}

	finalDomain, err := h.resolveRouteDomain(&struct {
		Domain       string `json:"domain"`
		Subdomain    string `json:"subdomain"`
		HosterDomain string `json:"hosterDomain"`
		CustomDomain string `json:"customDomain"`
		TargetPort   int    `json:"targetPort"`
	}{
		Domain: req.Domain, Subdomain: req.Subdomain, HosterDomain: req.HosterDomain,
		CustomDomain: req.CustomDomain, TargetPort: req.TargetPort,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if strings.HasPrefix(finalDomain, "*.") {
		http.Error(w, "Wildcard domains are not allowed for route-only", http.StatusBadRequest)
		return
	}

	host := strings.TrimSpace(strings.ToLower(req.TargetHost))
	if err := h.state.Gateway.CreateRouteViaLink(userID, finalDomain, linkToken, host, req.TargetPort); err != nil {
		msg := err.Error()
		if strings.Contains(msg, "disabled") {
			http.Error(w, msg, http.StatusForbidden)
		} else {
			http.Error(w, fmt.Sprintf("Failed to create route: %s", msg), http.StatusInternalServerError)
		}
		return
	}
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{"message": "Route created", "domain": finalDomain})
}

// ListLinkRoutes GET /api/gateway/link-routes — the caller's route-only entries.
func (h *GatewayHandler) ListLinkRoutes(w http.ResponseWriter, r *http.Request) {
	userID := r.Context().Value("userID").(string)
	out := make([]services.GatewayRoute, 0)
	for _, rt := range services.GetRoutesFromRedis(h.ctx(), h.state.Redis) {
		if rt.CoreOwned && rt.OwnerID == userID {
			out = append(out, rt)
		}
	}
	json.NewEncoder(w).Encode(out)
}

// DeleteLinkRoute DELETE /api/gateway/link-routes/{domain} — owner-scoped.
func (h *GatewayHandler) DeleteLinkRoute(w http.ResponseWriter, r *http.Request) {
	userID := r.Context().Value("userID").(string)
	isAdmin := r.Context().Value("isAdmin").(bool)
	domain := mux.Vars(r)["domain"]
	if domain == "" {
		http.Error(w, "domain required", http.StatusBadRequest)
		return
	}
	if !isAdmin {
		owned := false
		for _, rt := range services.GetRoutesFromRedis(h.ctx(), h.state.Redis) {
			if rt.Domain == domain && rt.CoreOwned && rt.OwnerID == userID {
				owned = true
				break
			}
		}
		if !owned {
			http.Error(w, "Route not found", http.StatusNotFound)
			return
		}
	}
	if err := h.state.Gateway.DeleteCoreOwnedRoute(domain); err != nil {
		http.Error(w, "Failed to delete route", http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"message": "Route deleted"})
}
