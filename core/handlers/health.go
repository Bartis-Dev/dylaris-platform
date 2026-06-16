package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"dylaris-core/services"
)

// HealthHandler powers the admin Status page. It runs a set of on-demand
// component checks (DB, metrics extension, Redis, nodes, gateway) and reports
// each as up / degraded / down / disabled with a human-readable reason. It does
// no background work — every check runs per request behind a short timeout so a
// hung dependency can't block the response.
type HealthHandler struct {
	state *AppState
}

func NewHealthHandler(state *AppState) *HealthHandler {
	return &HealthHandler{state: state}
}

// healthComponent is one row on the status page.
type healthComponent struct {
	Key    string       `json:"key"`
	Name   string       `json:"name"`
	Status string       `json:"status"`           // up | degraded | down | disabled
	Detail string       `json:"detail,omitempty"` // short one-line summary
	Reason string       `json:"reason,omitempty"` // why it's degraded/down
	Items  []healthItem `json:"items,omitempty"`  // sub-rows (per node, per edge, ...)
}

// healthItem is a sub-row (e.g. a single node) under a component.
type healthItem struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

type healthReport struct {
	Overall    string            `json:"overall"` // healthy | degraded | down
	Components []healthComponent `json:"components"`
	CheckedAt  string            `json:"checkedAt"`
}

const healthCheckTimeout = 3 * time.Second

// GetStatus GET /api/admin/health
//
// Admin-only aggregated platform health. Each component is checked
// independently; a failing optional component degrades the overall status but
// only a DB/Redis outage marks the platform "down".
func (h *HealthHandler) GetStatus(w http.ResponseWriter, r *http.Request) {
	if !IsAdmin(r) {
		sendJSONError(w, "Admin only", http.StatusForbidden)
		return
	}

	components := []healthComponent{}

	dbUp := false
	components = append(components, h.databaseComponent(r.Context(), &dbUp))
	components = append(components, h.metricsComponent(r.Context(), dbUp))
	redisUp := false
	components = append(components, h.redisComponent(r.Context(), &redisUp))
	components = append(components, h.nodesComponent())
	components = append(components, h.gatewayComponent(r.Context()))

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"health": healthReport{
			Overall:    overallStatus(components),
			Components: components,
			CheckedAt:  time.Now().UTC().Format(time.RFC3339),
		},
	})
}

// overallStatus rolls components up: a DB/Redis outage is "down" (Core can't
// function); any other down/degraded component is "degraded"; "disabled" is
// neutral and never lowers the overall status.
func overallStatus(components []healthComponent) string {
	overall := "healthy"
	for _, c := range components {
		switch {
		case c.Status == "down" && (c.Key == "database" || c.Key == "redis"):
			overall = "down"
		case c.Status == "down" || c.Status == "degraded":
			if overall == "healthy" {
				overall = "degraded"
			}
		}
	}
	return overall
}

func (h *HealthHandler) databaseComponent(ctx context.Context, up *bool) healthComponent {
	comp := healthComponent{Key: "database", Name: "Database (PostgreSQL)"}
	cctx, cancel := context.WithTimeout(ctx, healthCheckTimeout)
	defer cancel()
	if err := h.state.Store.Ping(cctx); err != nil {
		comp.Status = "down"
		comp.Detail = "Connection failed"
		comp.Reason = err.Error()
		return comp
	}
	*up = true
	comp.Status = "up"
	comp.Detail = "Connection alive"
	return comp
}

// metricsComponent reports whether time-series history is fully available. This
// is the "DB green, but history features limited because TimescaleDB is
// missing" signal: it never marks the platform down, only degraded.
func (h *HealthHandler) metricsComponent(ctx context.Context, dbUp bool) healthComponent {
	comp := healthComponent{Key: "metrics", Name: "Metrics history (time-series)"}
	if !dbUp {
		comp.Status = "degraded"
		comp.Detail = "Unknown"
		comp.Reason = "database unreachable, extension state cannot be checked"
		return comp
	}
	cctx, cancel := context.WithTimeout(ctx, healthCheckTimeout)
	defer cancel()
	enabled, err := h.state.Store.TimescaleEnabled(cctx)
	switch {
	case err != nil:
		comp.Status = "degraded"
		comp.Detail = "Unknown"
		comp.Reason = "extension check failed: " + err.Error()
	case enabled:
		comp.Status = "up"
		comp.Detail = "TimescaleDB hypertable active (native 24h retention)"
	case h.state.DBType == "postgres":
		// Intended plain-PostgreSQL deployment (DB_TYPE=postgres): server_stats
		// is a regular table and retention is enforced by the hourly sweep. This
		// is a supported configuration, not a fault.
		comp.Status = "up"
		comp.Detail = "Plain PostgreSQL (server_stats table, 24h retention via hourly sweep)"
	default:
		// DB_TYPE=timescaledb was requested but the extension isn't loaded — a
		// real misconfiguration. The platform still works (plain table + sweep),
		// so it's degraded, not down.
		comp.Status = "degraded"
		comp.Detail = "TimescaleDB requested but extension not installed"
		comp.Reason = "DB_TYPE=timescaledb but the timescaledb extension is not loaded. server_stats falls back to a plain table with the hourly retention sweep, so live + recent stats work; only TimescaleDB's hypertable optimizations are missing. Set DB_TYPE=postgres to silence this, or load the extension."
	}
	return comp
}

func (h *HealthHandler) redisComponent(ctx context.Context, up *bool) healthComponent {
	comp := healthComponent{Key: "redis", Name: "Redis"}
	cctx, cancel := context.WithTimeout(ctx, healthCheckTimeout)
	defer cancel()
	if err := h.state.Redis.Ping(cctx).Err(); err != nil {
		comp.Status = "down"
		comp.Detail = "Connection failed"
		comp.Reason = err.Error()
		return comp
	}
	*up = true
	comp.Status = "up"
	comp.Detail = "Connection alive"
	return comp
}

// nodesComponent reports node fleet health from the persisted node.Status,
// which the discovery service keeps current from heartbeats.
func (h *HealthHandler) nodesComponent() healthComponent {
	comp := healthComponent{Key: "nodes", Name: "Nodes"}
	nodes, err := h.state.Store.ListNodes()
	if err != nil {
		comp.Status = "down"
		comp.Detail = "Could not list nodes"
		comp.Reason = err.Error()
		return comp
	}
	if len(nodes) == 0 {
		comp.Status = "degraded"
		comp.Detail = "No nodes registered"
		comp.Reason = "no compute nodes are registered; servers cannot be placed until a node joins"
		return comp
	}

	online := 0
	items := make([]healthItem, 0, len(nodes))
	for i := range nodes {
		n := &nodes[i]
		item := healthItem{Name: n.Name}
		if n.Status == "online" {
			online++
			item.Status = "up"
			item.Detail = n.Region
		} else {
			item.Status = "down"
			item.Detail = "offline"
			if n.LastSeenAt != nil {
				item.Detail = "offline, last seen " + n.LastSeenAt.UTC().Format(time.RFC3339)
			}
		}
		items = append(items, item)
	}
	comp.Items = items

	switch {
	case online == len(nodes):
		comp.Status = "up"
		comp.Detail = fmt.Sprintf("%d/%d online", online, len(nodes))
	case online == 0:
		comp.Status = "down"
		comp.Detail = fmt.Sprintf("0/%d online", len(nodes))
		comp.Reason = "all registered nodes are offline"
	default:
		comp.Status = "degraded"
		comp.Detail = fmt.Sprintf("%d/%d online", online, len(nodes))
		comp.Reason = fmt.Sprintf("%d node(s) offline", len(nodes)-online)
	}
	return comp
}

// gatewayComponent reports gateway liveness. When routing is ip_port the
// subsystem is intentionally dormant ("disabled"), which is not a fault.
func (h *HealthHandler) gatewayComponent(ctx context.Context) healthComponent {
	comp := healthComponent{Key: "gateway", Name: "Gateway"}
	if !h.state.gatewayEnabled() {
		comp.Status = "disabled"
		comp.Detail = "Routing mode is ip_port"
		comp.Reason = "gateway subsystem is dormant; servers are reached via direct host ports"
		return comp
	}

	cctx, cancel := context.WithTimeout(ctx, healthCheckTimeout)
	defer cancel()
	edges := services.GetEdgesFromRedis(cctx, h.state.Redis)
	links := services.GetLinksFromRedis(cctx, h.state.Redis)
	routes := services.CountRoutesFromRedis(cctx, h.state.Redis)

	onlineEdges := 0
	for _, e := range edges {
		if e.Status == "" || e.Status == "online" {
			onlineEdges++
		}
	}
	onlineLinks := 0
	for _, l := range links {
		if l.Online {
			onlineLinks++
		}
	}

	comp.Items = []healthItem{
		{Name: "Edges", Status: countStatus(onlineEdges, len(edges)), Detail: fmt.Sprintf("%d/%d online", onlineEdges, len(edges))},
		{Name: "Links", Status: countStatus(onlineLinks, len(links)), Detail: fmt.Sprintf("%d/%d online", onlineLinks, len(links))},
		{Name: "Routes", Status: "up", Detail: fmt.Sprintf("%d active", routes)},
	}

	switch {
	case onlineEdges == 0:
		// Gateway routing is on but no edge is reachable: every gateway-routed
		// server is currently unreachable. This is the one gateway state that
		// is a real outage rather than a partial degrade.
		comp.Status = "down"
		comp.Detail = "No edges online"
		comp.Reason = "gateway routing is enabled but no edge is reachable; gateway-routed servers are unreachable"
	case onlineEdges < len(edges) || (len(links) > 0 && onlineLinks < len(links)):
		comp.Status = "degraded"
		comp.Detail = fmt.Sprintf("%d/%d edges, %d/%d links online", onlineEdges, len(edges), onlineLinks, len(links))
		comp.Reason = "some gateway components are offline"
	default:
		comp.Status = "up"
		comp.Detail = fmt.Sprintf("%d edge(s), %d link(s), %d route(s)", onlineEdges, len(links), routes)
	}
	return comp
}

// countStatus maps an online/total pair to a component status. Zero total means
// nothing of that kind is registered yet, which is neutral ("disabled") rather
// than a fault.
func countStatus(online, total int) string {
	switch {
	case total == 0:
		return "disabled"
	case online == 0:
		return "down"
	case online < total:
		return "degraded"
	default:
		return "up"
	}
}
