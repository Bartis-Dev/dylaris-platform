package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"dylaris-core/models"
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
	CPUUsage        float64 `json:"cpuUsage"`
	RAMFree         int64   `json:"ramFree"`
	RAMTotal        uint64  `json:"ramTotal"`
	LinkCount       int     `json:"linkCount"`
	PortRange       string  `json:"portRange"`
	PortRangeNotice string  `json:"portRangeNotice"`
	// SharedStorage carries the node's own detection of a storage path mounted
	// into more than one node. Passed straight through: the node is the only
	// party that can see it, and it must not stay in that node's log.
	SharedStorage []models.SharedStorageConflict `json:"sharedStorage"`
}

// GetOverview GET /api/infrastructure/overview - one payload for the
// infrastructure page: edges, links and nodes with their live heartbeat stats,
// plus route, tunnel and online counts and the recent service errors.
func (h *InfrastructureHandler) GetOverview(w http.ResponseWriter, r *http.Request) {
	ctx := context.Background()

	// Edges with full stats (auto-discovered from Redis)
	edges := services.GetEdgesFromRedis(ctx, h.state.Redis)

	// Links with online status
	links := services.GetLinksFromRedis(ctx, h.state.Redis)

	routeCount := services.CountRoutesFromRedis(ctx, h.state.Redis)

	// Nodes with server count + live heartbeat stats
	nodes, err := h.state.Store.ListNodes()
	if err != nil {
		nodes = nil
	}

	for i := range nodes {
		count, _ := h.state.Store.CountServersByNode(nodes[i].ID)
		nodes[i].ServerCount = count

		// Node writes the heartbeat key every 5s — values may be up to that stale.
		redisKey := fmt.Sprintf("dylaris:discovery:%s", nodes[i].Token)
		val, redisErr := h.state.Redis.Get(ctx, redisKey).Result()
		if redisErr == nil && val != "" {
			var hb nodeHeartbeatStats
			if jsonErr := json.Unmarshal([]byte(val), &hb); jsonErr == nil {
				nodes[i].CPUUsage = hb.CPUUsage
				nodes[i].RAMFree = hb.RAMFree
				nodes[i].RAMTotal = hb.RAMTotal
				nodes[i].LinkCount = hb.LinkCount
				nodes[i].PortRange = hb.PortRange
				nodes[i].PortRangeNotice = hb.PortRangeNotice
				nodes[i].SharedStorage = hb.SharedStorage
			}
		}
	}

	// Aggregate stats
	onlineLinks := 0
	for _, l := range links {
		if l.Online {
			onlineLinks++
		}
	}

	onlineEdges := 0
	for _, e := range edges {
		if e.Status == "online" {
			onlineEdges++
		}
	}

	// Service errors from Redis Streams
	errors := services.GetAllServiceErrorsFromRedis(h.state.Redis, 20)

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":     true,
		"edges":       edges,
		"links":       links,
		"nodes":       nodes,
		"routeCount":  routeCount,
		"onlineLinks": onlineLinks,
		"onlineEdges": onlineEdges,
		"errors":      errors,
	})
}

// GetRoutingMigrationStatus GET /api/infrastructure/routing-migration -
// progress of the routing migration job. With no job wired it answers
// running:false instead of failing, so the page still renders on an instance
// that never ran one.
func (h *InfrastructureHandler) GetRoutingMigrationStatus(w http.ResponseWriter, r *http.Request) {
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
