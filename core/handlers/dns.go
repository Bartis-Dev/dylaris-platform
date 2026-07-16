package handlers

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"strings"

	"dylaris-core/services"
)

// DNSHandler serves the on-demand DNS / reachability check. It sits with the
// other /api/gateway/* admin endpoints (see GatewayHandler) and is gated on
// PANEL topology.read (Phase 4 Task 18).
type DNSHandler struct {
	state *AppState
}

func NewDNSHandler(state *AppState) *DNSHandler {
	return &DNSHandler{state: state}
}

// CheckDNS computes the required DNS records from the operator's OWN config
// (FRONTEND_URL + gateway settings + registered edges) and verifies them
// against the public DNS view plus a TCP reachability probe. Gated on
// topology.read at the route; the check is strictly bound to operator-configured
// domains and edge IPs, so it exposes no arbitrary-lookup / SSRF surface.
//
// GET /api/gateway/dns-check
func (h *DNSHandler) CheckDNS(w http.ResponseWriter, r *http.Request) {
	cfg := h.buildDNSCheckConfig()
	result := services.RunDNSCheck(context.Background(), h.state.Redis, cfg)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// buildDNSCheckConfig derives the checker input from the operator's config.
// Panel host/port comes from FRONTEND_URL; hoster domains and the custom-domain
// CNAME flag/target come from the same settings keys GatewayHandler uses.
func (h *DNSHandler) buildDNSCheckConfig() services.DNSCheckConfig {
	cfg := services.DNSCheckConfig{}

	// Panel: host of FRONTEND_URL, plus a host:port to TCP-dial (443 for https
	// without an explicit port, 80 for http without one).
	if u, err := url.Parse(h.state.FrontendURL); err == nil && u.Host != "" {
		cfg.PanelHost = u.Hostname()
		port := u.Port()
		if port == "" {
			if u.Scheme == "https" {
				port = "443"
			} else {
				port = "80"
			}
		}
		if cfg.PanelHost != "" {
			cfg.PanelDialTarget = net.JoinHostPort(cfg.PanelHost, port)
		}
	}

	// Hoster base domains from gateway_hoster_domains (same shape as
	// GatewayHandler.loadGatewayDomainConfig / HosterDomain).
	if raw, _ := h.state.Store.GetSetting("gateway_hoster_domains"); raw != "" {
		var hosters []HosterDomain
		if json.Unmarshal([]byte(raw), &hosters) == nil {
			for _, hd := range hosters {
				d := strings.ToLower(strings.TrimSpace(hd.Domain))
				if d != "" {
					cfg.HosterDomains = append(cfg.HosterDomains, d)
				}
			}
		}
	}

	// Custom-domain CNAME target (only meaningful when enabled).
	if enabled, _ := h.state.Store.GetSetting("gateway_custom_domains_enabled"); enabled == "true" {
		cfg.CustomDomainsOn = true
		cfg.CNAMETarget, _ = h.state.Store.GetSetting("gateway_cname_target")
	}

	return cfg
}
