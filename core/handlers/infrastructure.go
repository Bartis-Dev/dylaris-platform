package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"dylaris-core/services"
)

type InfrastructureHandler struct {
	state *AppState
}

func NewInfrastructureHandler(state *AppState) *InfrastructureHandler {
	return &InfrastructureHandler{state: state}
}

// nodeHeartbeatStats is the subset we read from the Redis heartbeat key.
type nodeHeartbeatStats struct {
	CPUUsage  float64 `json:"cpuUsage"`
	RAMFree   int64   `json:"ramFree"`
	RAMTotal  uint64  `json:"ramTotal"`
	LinkCount int     `json:"linkCount"`
}

// GetOverview GET /api/infrastructure/overview
func (h *InfrastructureHandler) GetOverview(w http.ResponseWriter, r *http.Request) {
	if !IsAdmin(r) {
		sendJSONError(w, "Admin only", http.StatusForbidden)
		return
	}

	ctx := context.Background()

	// Gates with full stats (auto-discovered from Redis)
	gates := services.GetGatesFromRedis(ctx, h.state.Redis)

	// Links with online status
	links := services.GetLinksFromRedis(ctx, h.state.Redis)

	// Route count
	routeCount := services.CountRoutesFromRedis(ctx, h.state.Redis)

	// Nodes with server count + live heartbeat stats
	nodes, err := h.state.Store.ListNodes()
	if err != nil {
		nodes = nil
	}

	for i := range nodes {
		count, _ := h.state.Store.CountServersByNode(nodes[i].ID)
		nodes[i].ServerCount = count

		// Read live stats from heartbeat key (written by node every 5s)
		redisKey := fmt.Sprintf("dylaris:discovery:%s", nodes[i].Token)
		val, redisErr := h.state.Redis.Get(ctx, redisKey).Result()
		if redisErr == nil && val != "" {
			var hb nodeHeartbeatStats
			if jsonErr := json.Unmarshal([]byte(val), &hb); jsonErr == nil {
				nodes[i].CPUUsage = hb.CPUUsage
				nodes[i].RAMFree = hb.RAMFree
				nodes[i].RAMTotal = hb.RAMTotal
				nodes[i].LinkCount = hb.LinkCount
			}
		}
	}

	// Aggregate stats
	onlineLinks := 0
	totalTunnels := 0
	for _, l := range links {
		if l.Online {
			onlineLinks++
		}
		totalTunnels += l.ActiveTunnels
	}

	onlineGates := 0
	for _, g := range gates {
		if g.Status == "online" {
			onlineGates++
		}
	}

	// Service errors from Redis Streams
	errors := services.GetAllServiceErrorsFromRedis(h.state.Redis, 20)

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":      true,
		"gates":        gates,
		"links":        links,
		"nodes":        nodes,
		"routeCount":   routeCount,
		"onlineLinks":  onlineLinks,
		"onlineGates":  onlineGates,
		"totalTunnels": totalTunnels,
		"errors":       errors,
	})
}

// GetRoutingMigrationStatus GET /api/infrastructure/routing-migration
func (h *InfrastructureHandler) GetRoutingMigrationStatus(w http.ResponseWriter, r *http.Request) {
	if !IsAdmin(r) {
		sendJSONError(w, "Admin only", http.StatusForbidden)
		return
	}
	if h.state.RoutingMigration == nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "running": false})
		return
	}
	status := h.state.RoutingMigration.GetStatus(r.Context())
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"running": status.Running,
		"total":   status.Total,
		"done":    status.Done,
		"failed":  status.Failed,
	})
}
