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

	// Aggregate stats. Links are split by who RUNS them: a customer's BYON or
	// route-only link going down is not an outage of this platform, so the
	// operator's own count must not include it. The customer figures are
	// reported beside it, as a count and nothing more - see customerSummary.
	split := services.LoadLinkOwnership(h.state.Store).SplitLinks(links)
	onlineLinks := split.OursOnline

	onlineEdges := 0
	for _, e := range edges {
		if e.Status == "online" {
			onlineEdges++
		}
	}

	// Service errors from Redis Streams
	errors := services.GetAllServiceErrorsFromRedis(h.state.Redis, 20)

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"edges":   edges,
		// Ours only. The customer links are counted in `customers` and are
		// deliberately not listed: a link is identified by its TOKEN, which is
		// a credential, and there is nothing an operator can do with somebody
		// else's anyway.
		"links":       split.Ours,
		"nodes":       nodes,
		"routeCount":  routeCount,
		"onlineLinks": onlineLinks,
		"onlineEdges": onlineEdges,
		"customers":   h.customerSummary(ctx, split),
		"errors":      errors,
	})
}

// customerCounts is a total and how many of them are up. Nothing else - no
// status, no severity, no list.
//
// That shape is the whole point. These machines belong to tenants, run in places
// nobody here can reach, and are switched off for ordinary reasons. Attaching a
// severity to "3 of 4 up" would put an amber badge on this platform for somebody
// else's laptop being closed, and an operator who cannot act on a warning learns
// to stop reading warnings.
//
// Online is a POINTER because absent and zero are different answers: warp
// liveness comes from a gauge the leaders only publish after the gateway is
// updated, and reporting a confident 0/12 for a fleet nobody measured would be
// worse than saying nothing.
type customerCounts struct {
	Total  int  `json:"total"`
	Online *int `json:"online"`
}

type customerSummaryBlock struct {
	Nodes customerCounts `json:"nodes"`
	Links customerCounts `json:"links"`
	Warps customerCounts `json:"warps"`
}

// customerSummary counts what tenants run: BYON nodes, their links, and their
// warp overlay peers.
func (h *InfrastructureHandler) customerSummary(ctx context.Context, split services.LinkSplit) customerSummaryBlock {
	var out customerSummaryBlock

	all, err := h.state.Store.ListNodes()
	if err == nil {
		_, customer := services.SplitNodes(all)
		online := 0
		for i := range customer {
			if customer[i].Status == "online" {
				online++
			}
		}
		out.Nodes = customerCounts{Total: len(customer), Online: &online}
	}

	linksOnline := split.CustomerOnline
	out.Links = customerCounts{Total: len(split.Customer), Online: &linksOnline}

	if peers, perr := h.state.Store.ListAllWarpPeers(); perr == nil {
		out.Warps = customerCounts{Total: len(peers)}
		// A peer is a tenant machine on the overlay, and only the warp leader
		// can say whether its tunnel is actually up. It publishes that as an
		// aggregate; until every leader is on a build that does, the honest
		// answer is a total with no online count rather than a zero.
		if up, known := services.WarpPeersActive(ctx, h.state.Redis); known {
			out.Warps.Online = &up
		}
	}
	return out
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
