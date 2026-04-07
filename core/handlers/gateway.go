package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/gorilla/mux"
)

type GatewayHandler struct {
	state *AppState
}

func NewGatewayHandler(state *AppState) *GatewayHandler {
	return &GatewayHandler{state: state}
}

var domainRegex = regexp.MustCompile(`^(\*\.)?[a-z0-9]([a-z0-9.-]*[a-z0-9])?$`)

// ==========================================
// ADMIN: Links
// ==========================================

func (h *GatewayHandler) GetLinks(w http.ResponseWriter, r *http.Request) {
	links := h.state.HubBridge.Hub.GetLinks()
	json.NewEncoder(w).Encode(links)
}

// ==========================================
// ADMIN: Gates
// ==========================================

func (h *GatewayHandler) GetGates(w http.ResponseWriter, r *http.Request) {
	gates := h.state.HubBridge.Hub.GetGates()
	json.NewEncoder(w).Encode(gates)
}

// ==========================================
// ADMIN: Routes (all routes overview)
// ==========================================

func (h *GatewayHandler) GetAllRoutes(w http.ResponseWriter, r *http.Request) {
	routes := h.state.HubBridge.Hub.GetRoutesWithJoins("users", "servers")
	json.NewEncoder(w).Encode(routes)
}

func (h *GatewayHandler) AdminDeleteRoute(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(mux.Vars(r)["id"])
	if err := h.state.HubBridge.Hub.DeleteRoute(fmt.Sprint(id)); err != nil {
		http.Error(w, "Failed to delete route", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"message": "Route deleted"})
}

// ==========================================
// ADMIN: Logs, Stats, Sync
// ==========================================

func (h *GatewayHandler) GetLogs(w http.ResponseWriter, r *http.Request) {
	logs := h.state.HubBridge.Hub.GetLogs(100)
	json.NewEncoder(w).Encode(logs)
}

func (h *GatewayHandler) GetStats(w http.ResponseWriter, r *http.Request) {
	links := h.state.HubBridge.Hub.GetLinks()
	gates := h.state.HubBridge.Hub.GetGates()
	routeCount := h.state.HubBridge.Hub.CountRoutesTotal()

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
	h.state.HubBridge.SyncData()
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"message": "Sync triggered"})
}

// ==========================================
// ADMIN: Errors
// ==========================================

func (h *GatewayHandler) GetErrors(w http.ResponseWriter, r *http.Request) {
	service := r.URL.Query().Get("service")
	if service != "" {
		errors := h.state.HubBridge.Hub.GetServiceErrors(service, 50)
		json.NewEncoder(w).Encode(errors)
	} else {
		errors := h.state.HubBridge.Hub.GetAllServiceErrors(50)
		json.NewEncoder(w).Encode(errors)
	}
}

// ==========================================
// USER: Server Routes
// ==========================================

func (h *GatewayHandler) GetServerRoutes(w http.ResponseWriter, r *http.Request) {
	serverID, _ := strconv.Atoi(mux.Vars(r)["id"])
	routes := h.state.HubBridge.Hub.GetRoutesByServer(uint(serverID))
	json.NewEncoder(w).Encode(routes)
}

func (h *GatewayHandler) CreateServerRoute(w http.ResponseWriter, r *http.Request) {
	serverID, _ := strconv.Atoi(mux.Vars(r)["id"])
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

	route, err := h.state.HubBridge.CreateServerRoute(serverID, userID, req.Domain, req.TargetPort)
	if err != nil {
		errMsg := err.Error()
		if strings.Contains(errMsg, "not found") {
			http.Error(w, errMsg, http.StatusNotFound)
		} else if strings.Contains(errMsg, "limit") || strings.Contains(errMsg, "disabled") {
			http.Error(w, errMsg, http.StatusForbidden)
		} else {
			http.Error(w, fmt.Sprintf("Failed to create route: %s", errMsg), http.StatusConflict)
		}
		return
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(route)
}

func (h *GatewayHandler) DeleteServerRoute(w http.ResponseWriter, r *http.Request) {
	serverID, _ := strconv.Atoi(mux.Vars(r)["id"])
	routeID, _ := strconv.Atoi(mux.Vars(r)["routeId"])
	userID := r.Context().Value("userID").(int)
	isAdmin := r.Context().Value("isAdmin").(bool)

	// Get route to verify ownership
	route, err := h.state.HubBridge.Hub.GetRouteByID(uint(routeID))
	if err != nil {
		http.Error(w, "Route not found", http.StatusNotFound)
		return
	}

	// Verify ownership
	if (route.ServerID == nil || int(*route.ServerID) != serverID) && !isAdmin {
		http.Error(w, "Route does not belong to this server", http.StatusForbidden)
		return
	}
	if (route.OwnerID == nil || int(*route.OwnerID) != userID) && !isAdmin {
		http.Error(w, "Not authorized to delete this route", http.StatusForbidden)
		return
	}

	if err := h.state.HubBridge.Hub.DeleteRoute(fmt.Sprint(routeID)); err != nil {
		http.Error(w, "Failed to delete route", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"message": "Route deleted"})
}

