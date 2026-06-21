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

// Route-only ("external") routes: a DDoS-protected address pointed at a server
// the customer already runs (a public host:port), with NO managed node/Link. The
// edge dials the origin directly. The customer firewalls their origin to the edge
// IPs so the real IP can't be hit directly (documented in the panel + docs).

// validateExternalTarget rejects obviously-private / loopback targets up front
// (the edge re-validates after DNS resolution as the real SSRF guard). A bare
// hostname is allowed here and resolved at dial time.
func validateExternalTarget(host string) error {
	host = strings.TrimSpace(strings.ToLower(host))
	if host == "" {
		return fmt.Errorf("target host is required")
	}
	if host == "localhost" {
		return fmt.Errorf("target may not be localhost")
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
			ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
			return fmt.Errorf("target must be a public address (private/loopback addresses are not allowed)")
		}
		return nil
	}
	// Hostname: basic format check (no wildcard, valid domain chars).
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

// CreateExternalRoute POST /api/gateway/external-routes — authed, gateway-gated.
func (h *GatewayHandler) CreateExternalRoute(w http.ResponseWriter, r *http.Request) {
	userID := r.Context().Value("userID").(string)

	var req struct {
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
	if err := validateExternalTarget(req.TargetHost); err != nil {
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
	if err := h.state.Gateway.CreateExternalRoute(userID, finalDomain, host, req.TargetPort); err != nil {
		msg := err.Error()
		if strings.Contains(msg, "disabled") {
			http.Error(w, msg, http.StatusForbidden)
		} else {
			http.Error(w, fmt.Sprintf("Failed to create route: %s", msg), http.StatusInternalServerError)
		}
		return
	}
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{"message": "Route creation queued", "domain": finalDomain})
}

// ListExternalRoutes GET /api/gateway/external-routes — the caller's route-only entries.
func (h *GatewayHandler) ListExternalRoutes(w http.ResponseWriter, r *http.Request) {
	userID := r.Context().Value("userID").(string)
	out := make([]services.GatewayRoute, 0)
	for _, rt := range services.GetRoutesFromRedis(h.ctx(), h.state.Redis) {
		if rt.External && rt.OwnerID == userID {
			out = append(out, rt)
		}
	}
	json.NewEncoder(w).Encode(out)
}

// DeleteExternalRoute DELETE /api/gateway/external-routes/{domain} — owner-scoped.
func (h *GatewayHandler) DeleteExternalRoute(w http.ResponseWriter, r *http.Request) {
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
			if rt.Domain == domain && rt.External && rt.OwnerID == userID {
				owned = true
				break
			}
		}
		if !owned {
			http.Error(w, "Route not found", http.StatusNotFound)
			return
		}
	}
	if err := h.state.Gateway.DeleteRoute(domain); err != nil {
		http.Error(w, "Failed to delete route", http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"message": "Route deletion queued"})
}
