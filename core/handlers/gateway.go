package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"dylaris-core/services"

	"github.com/gorilla/mux"
)

type GatewayHandler struct {
	state *AppState
}

func NewGatewayHandler(state *AppState) *GatewayHandler {
	return &GatewayHandler{state: state}
}

var domainRegex = regexp.MustCompile(`^(\*\.)?[a-z0-9]([a-z0-9.-]*[a-z0-9])?$`)

// Per-hoster subdomain validation regexes — kept narrow on purpose so the
// admin's choice in settings actually constrains what users can register.
var (
	subRegexLetters      = regexp.MustCompile(`^[a-z]+$`)
	subRegexAlphanumeric = regexp.MustCompile(`^[a-z0-9]+$`)
	subRegexDNS          = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)
)

// validateSubdomain matches a user-entered subdomain against the validation
// mode the admin picked for that hoster domain.
func validateSubdomain(sub, mode string) bool {
	if sub == "" || len(sub) > 63 {
		return false
	}
	switch mode {
	case "letters":
		return subRegexLetters.MatchString(sub)
	case "alphanumeric":
		return subRegexAlphanumeric.MatchString(sub)
	case "dns":
		return subRegexDNS.MatchString(sub)
	default:
		return false
	}
}

func (h *GatewayHandler) ctx() context.Context {
	return context.Background()
}

// ==========================================
// ADMIN: Links
// ==========================================

func (h *GatewayHandler) GetLinks(w http.ResponseWriter, r *http.Request) {
	links := services.GetLinksFromRedis(h.ctx(), h.state.Redis)
	if links == nil {
		links = []services.GatewayLinkStatus{}
	}
	json.NewEncoder(w).Encode(links)
}

// ==========================================
// ADMIN: Gates
// ==========================================

func (h *GatewayHandler) GetGates(w http.ResponseWriter, r *http.Request) {
	gates := services.GetGatesFromRedis(h.ctx(), h.state.Redis)
	if gates == nil {
		gates = []services.GatewayGateInfo{}
	}
	json.NewEncoder(w).Encode(gates)
}

// ==========================================
// ADMIN: Routes (all routes overview)
// ==========================================

func (h *GatewayHandler) GetAllRoutes(w http.ResponseWriter, r *http.Request) {
	routes := services.GetRoutesFromRedis(h.ctx(), h.state.Redis)
	if routes == nil {
		routes = []services.GatewayRoute{}
	}
	json.NewEncoder(w).Encode(routes)
}

func (h *GatewayHandler) AdminDeleteRoute(w http.ResponseWriter, r *http.Request) {
	domain := mux.Vars(r)["domain"]
	if domain == "" {
		http.Error(w, "domain required", http.StatusBadRequest)
		return
	}
	if err := h.state.Gateway.DeleteRoute(domain); err != nil {
		http.Error(w, "Failed to delete route", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"message": "Route deleted"})
}

// ==========================================
// ADMIN: Stats, Sync
// ==========================================

func (h *GatewayHandler) GetLogs(w http.ResponseWriter, r *http.Request) {
	// Hub logs are not in Redis; return service error logs instead
	errors := services.GetAllServiceErrorsFromRedis(h.state.Redis, 100)
	json.NewEncoder(w).Encode(errors)
}

func (h *GatewayHandler) GetStats(w http.ResponseWriter, r *http.Request) {
	links := services.GetLinksFromRedis(h.ctx(), h.state.Redis)
	gates := services.GetGatesFromRedis(h.ctx(), h.state.Redis)
	routeCount := services.CountRoutesFromRedis(h.ctx(), h.state.Redis)

	onlineLinks := 0
	for _, l := range links {
		if l.Online {
			onlineLinks++
		}
	}
	onlineGates := 0
	for _, g := range gates {
		if g.Status == "online" {
			onlineGates++
		}
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"links":       len(links),
		"linksOnline": onlineLinks,
		"gates":       len(gates),
		"gatesOnline": onlineGates,
		"routes":      routeCount,
	})
}

func (h *GatewayHandler) TriggerSync(w http.ResponseWriter, r *http.Request) {
	// Sync is managed by Hub autonomously; no-op from Core side
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"message": "Sync is managed by Hub"})
}

// ==========================================
// ADMIN: Errors
// ==========================================

func (h *GatewayHandler) GetErrors(w http.ResponseWriter, r *http.Request) {
	service := r.URL.Query().Get("service")
	if service != "" {
		errors := services.GetServiceErrorsFromRedis(h.state.Redis, service, 50)
		json.NewEncoder(w).Encode(errors)
	} else {
		errors := services.GetAllServiceErrorsFromRedis(h.state.Redis, 50)
		json.NewEncoder(w).Encode(errors)
	}
}

// ==========================================
// USER: Server Routes
// ==========================================

func (h *GatewayHandler) GetServerRoutes(w http.ResponseWriter, r *http.Request) {
	serverID := mux.Vars(r)["id"]

	// Get server UUID from Core's store
	server, err := h.state.Store.GetServerByID(mustAtoi(serverID))
	if err != nil {
		http.Error(w, "Server not found", http.StatusNotFound)
		return
	}

	// Filter routes by server_uuid
	all := services.GetRoutesFromRedis(h.ctx(), h.state.Redis)
	var routes []services.GatewayRoute
	for _, rt := range all {
		if rt.ServerUUID == server.UUID {
			routes = append(routes, rt)
		}
	}
	if routes == nil {
		routes = []services.GatewayRoute{}
	}
	json.NewEncoder(w).Encode(routes)
}

func (h *GatewayHandler) CreateServerRoute(w http.ResponseWriter, r *http.Request) {
	serverID := mustAtoi(mux.Vars(r)["id"])
	userID := r.Context().Value("userID").(int)

	// Three input shapes, listed in priority order:
	//   1. {subdomain, hosterDomain}      — user picked from the admin's hoster list
	//   2. {customDomain}                 — user brings their own domain (CNAME path)
	//   3. {domain}                       — legacy / admin / scripts; raw FQDN
	var req struct {
		Domain       string `json:"domain"`
		Subdomain    string `json:"subdomain"`
		HosterDomain string `json:"hosterDomain"`
		CustomDomain string `json:"customDomain"`
		TargetPort   int    `json:"targetPort"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	if req.TargetPort == 0 {
		req.TargetPort = 25565
	}

	finalDomain, err := h.resolveRouteDomain(&req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if len(finalDomain) > 2 && finalDomain[:2] == "*." && req.TargetPort != 80 && req.TargetPort != 443 {
		http.Error(w, "Wildcard domains are only allowed for port 80/443", http.StatusBadRequest)
		return
	}

	if err := h.state.Gateway.CreateServerRoute(uint(serverID), uint(userID), finalDomain, req.TargetPort); err != nil {
		errMsg := err.Error()
		if strings.Contains(errMsg, "not found") {
			http.Error(w, errMsg, http.StatusNotFound)
		} else if strings.Contains(errMsg, "disabled") {
			http.Error(w, errMsg, http.StatusForbidden)
		} else {
			http.Error(w, fmt.Sprintf("Failed to create route: %s", errMsg), http.StatusInternalServerError)
		}
		return
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Route creation queued",
		"domain":  finalDomain,
	})
}

// resolveRouteDomain inspects the three accepted input shapes and returns the
// final lowercase FQDN to register. It enforces the admin's hoster-domain
// configuration: subdomains must match the per-hoster validation mode, custom
// domains may only be used when the admin enabled them and may not collide
// with any hoster domain.
func (h *GatewayHandler) resolveRouteDomain(req *struct {
	Domain       string `json:"domain"`
	Subdomain    string `json:"subdomain"`
	HosterDomain string `json:"hosterDomain"`
	CustomDomain string `json:"customDomain"`
	TargetPort   int    `json:"targetPort"`
}) (string, error) {
	hosters, customEnabled, _ := h.loadGatewayDomainConfig()

	// 1) Hoster-picker path
	if req.Subdomain != "" || req.HosterDomain != "" {
		sub := strings.ToLower(strings.TrimSpace(req.Subdomain))
		host := strings.ToLower(strings.TrimSpace(req.HosterDomain))
		if sub == "" || host == "" {
			return "", fmt.Errorf("subdomain and hosterDomain must both be set")
		}
		var hd *HosterDomain
		for i := range hosters {
			if hosters[i].Domain == host {
				hd = &hosters[i]
				break
			}
		}
		if hd == nil {
			return "", fmt.Errorf("hoster domain not configured: %s", host)
		}
		if !validateSubdomain(sub, hd.Validation) {
			return "", fmt.Errorf("subdomain does not match the allowed format for %s", host)
		}
		return sub + "." + host, nil
	}

	// 2) Custom-domain path
	if req.CustomDomain != "" {
		if !customEnabled {
			return "", fmt.Errorf("custom domains are not enabled")
		}
		dom := strings.ToLower(strings.TrimSpace(req.CustomDomain))
		if !domainRegex.MatchString(dom) || strings.HasPrefix(dom, "*.") {
			return "", fmt.Errorf("invalid custom domain format")
		}
		labels := strings.Split(dom, ".")
		// Apex (mc.de = 2 labels) up to apex + 2 subdomains (a.b.c.d = 4 labels)
		if len(labels) < 2 || len(labels) > 4 {
			return "", fmt.Errorf("custom domain may have at most two subdomain levels")
		}
		for _, h := range hosters {
			if dom == h.Domain || strings.HasSuffix(dom, "."+h.Domain) {
				return "", fmt.Errorf("custom domain may not be a subdomain of a hoster domain (%s) — use the subdomain picker instead", h.Domain)
			}
		}
		return dom, nil
	}

	// 3) Legacy raw-domain path (admin tools, scripts, backwards compat)
	if req.Domain != "" {
		dom := strings.ToLower(strings.TrimSpace(req.Domain))
		if !domainRegex.MatchString(dom) {
			return "", fmt.Errorf("invalid domain format")
		}
		return dom, nil
	}

	return "", fmt.Errorf("no domain provided")
}

// loadGatewayDomainConfig reads the hoster-domain list + custom flag straight
// from the settings store. Mirrors SettingsHandler.LoadGatewayDomainConfig
// but inlined here so GatewayHandler doesn't need a SettingsHandler reference.
func (h *GatewayHandler) loadGatewayDomainConfig() ([]HosterDomain, bool, string) {
	raw, _ := h.state.Store.GetSetting("gateway_hoster_domains")
	var hosters []HosterDomain
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &hosters)
	}
	enabled, _ := h.state.Store.GetSetting("gateway_custom_domains_enabled")
	cname, _ := h.state.Store.GetSetting("gateway_cname_target")
	return hosters, enabled == "true", cname
}

func (h *GatewayHandler) DeleteServerRoute(w http.ResponseWriter, r *http.Request) {
	serverID := mustAtoi(mux.Vars(r)["id"])
	domain := mux.Vars(r)["domain"]
	userID := r.Context().Value("userID").(int)
	isAdmin := r.Context().Value("isAdmin").(bool)

	if domain == "" {
		http.Error(w, "domain required", http.StatusBadRequest)
		return
	}

	// Ownership check: verify server belongs to this user (or admin)
	if !isAdmin {
		server, err := h.state.Store.GetServerByID(serverID)
		if err != nil {
			http.Error(w, "Server not found", http.StatusNotFound)
			return
		}
		if server.OwnerID != userID {
			http.Error(w, "Not authorized", http.StatusForbidden)
			return
		}
	}

	// Ensure the route belongs to this server (optional but safe)
	if !isAdmin {
		server, _ := h.state.Store.GetServerByID(serverID)
		all := services.GetRoutesFromRedis(h.ctx(), h.state.Redis)
		found := false
		for _, rt := range all {
			if rt.Domain == domain && rt.ServerUUID == server.UUID {
				found = true
				break
			}
		}
		if !found {
			http.Error(w, "Route not found for this server", http.StatusNotFound)
			return
		}
	}

	if err := h.state.Gateway.DeleteRoute(domain); err != nil {
		http.Error(w, "Failed to delete route", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"message": "Route deletion queued"})
}

func mustAtoi(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}
