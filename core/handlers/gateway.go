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

	var req struct {
		Domain     string `json:"domain"`
		TargetPort int    `json:"targetPort"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Domain == "" {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	if req.TargetPort == 0 {
		req.TargetPort = 25565
	}

	if !domainRegex.MatchString(req.Domain) {
		http.Error(w, "Invalid domain format", http.StatusBadRequest)
		return
	}

	if len(req.Domain) > 2 && req.Domain[:2] == "*." && req.TargetPort != 80 && req.TargetPort != 443 {
		http.Error(w, "Wildcard domains are only allowed for port 80/443", http.StatusBadRequest)
		return
	}

	if err := h.state.Gateway.CreateServerRoute(uint(serverID), uint(userID), req.Domain, req.TargetPort); err != nil {
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
		"domain":  req.Domain,
	})
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
