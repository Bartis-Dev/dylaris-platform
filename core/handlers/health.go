package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"dylaris-core/database"
	"dylaris-core/services"
)

// HealthHandler powers the admin Status page. It runs a set of on-demand
// component checks (DB, metrics extension, Redis, nodes, gateway, storage) and
// reports each as up / degraded / down / disabled with a human-readable reason.
// It does no background work of its own.
//
// Most checks run per request behind healthCheckTimeout so a hung dependency
// cannot block the response. Two do not, and saying "every check" here was
// wrong: nodesComponent calls Store.ListNodes, which takes no context at all,
// so a wedged database blocks it for as long as the driver allows; and
// storageComponent takes no timeout because it needs none, reading only
// atomics that the storage layer's own background watchers maintain.
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

	// Cause is a machine-readable class for the failure, where the component
	// has one. Status alone cannot carry this: two components can both be
	// "down" for reasons that need completely different actions, and the panel
	// has no way to tell them apart from a free-text reason. Only the Redis
	// component sets it today (database.RedisFailure.Slug); an empty value
	// means the component has no classification and the panel falls back to
	// showing Detail and Reason alone.
	Cause string `json:"cause,omitempty"`
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
// Aggregated platform health, PANEL settings.read (RequireCap at the route).
// Each component is checked independently; a failing optional component
// degrades the overall status but only a DB/Redis outage marks the platform
// "down".
func (h *HealthHandler) GetStatus(w http.ResponseWriter, r *http.Request) {
	components := []healthComponent{}

	dbUp := false
	components = append(components, h.databaseComponent(r.Context(), &dbUp))
	components = append(components, h.metricsComponent(r.Context(), dbUp))
	redisUp := false
	components = append(components, h.redisComponent(r.Context(), &redisUp))
	components = append(components, h.nodesComponent())
	components = append(components, h.gatewayComponent(r.Context()))
	components = append(components, h.storageComponent())

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

// redisComponent pings Redis and reports WHY it failed, not just that it did.
//
// A single PING is enough to separate the classes: verified against valkey 8, a
// user without the +ping permission gets NOPERM from PING itself rather than a
// generic failure, so no second probe command is needed to detect an ACL gap.
//
// The reply is still carried verbatim in Reason. This route requires
// settings.read, so the server's own message - which names the command a NOPERM
// refers to - is exactly what an operator needs to fix it.
func (h *HealthHandler) redisComponent(ctx context.Context, up *bool) healthComponent {
	comp := healthComponent{Key: "redis", Name: "Redis"}
	cctx, cancel := context.WithTimeout(ctx, healthCheckTimeout)
	defer cancel()

	err := h.state.Redis.Ping(cctx).Err()
	failure := database.ClassifyRedisError(err)
	if failure != database.RedisOK {
		comp.Status = "down"
		comp.Detail = failure.Summary()
		comp.Cause = failure.Slug()
		comp.Reason = err.Error()
		return comp
	}

	*up = true
	comp.Status = "up"
	comp.Detail = failure.Summary()
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

// storageComponent reports the connection state of the configured core-storage
// backend: the host-path watchdog's verdict, or the s3 wrapper's.
//
// It reads atomics only and touches neither the filesystem nor the network.
// That is a requirement, not an optimisation. Statting a wedged network mount
// here would block inside a syscall no context can interrupt, so the status
// page would hang on exactly the outage it exists to report, and a timeout
// around it would abandon a goroutine pinned to an OS thread on every request.
// The reading it hands back can therefore be up to one probe interval stale.
//
// "up" means no evidence of a problem, NOT verified reachable. Neither backend
// proves reachability: the watchdog's verdict lags its probe, and s3 is only
// observed through calls something else made.
//
// A storage outage is degraded, never down. "down" is reserved for the
// dependencies Core cannot run without at all (see overallStatus); with storage
// unreachable the panel still serves, the API still answers and running servers
// keep running. Only the features that touch stored files fail.
//
// Unlike the SSE payload and GET /api/storage/connection, this may carry the
// cause: the route behind it requires the settings.read capability, so the host
// path, the errno and the endpoint are visible to an operator rather than to
// every authenticated user.
func (h *HealthHandler) storageComponent() healthComponent {
	comp := healthComponent{Key: "storage", Name: "Core storage"}
	cfg := h.state.LoadCoreStorageConfig()

	switch cfg.Backend {
	case "s3":
		reconnecting, since, lastErr := h.state.StorageS3.State()
		if !reconnecting {
			comp.Status = "up"
			comp.Detail = "S3 backend, no connection failure reported"
			return comp
		}
		comp.Status = "degraded"
		comp.Detail = "S3 backend reconnecting since " + since.UTC().Format(time.RFC3339)
		comp.Reason = "the object store stopped answering; replayable operations are paused and retried, uploads wait before they start, and both fail once the retry budget runs out"
		if lastErr != nil {
			comp.Reason += ": " + lastErr.Error()
		}
		return comp

	case "path", "local":
		healthy, cause := h.state.StorageGate.Healthy()
		if healthy {
			comp.Status = "up"
			comp.Detail = "Host path, no connection failure observed"
			return comp
		}
		comp.Status = "degraded"
		comp.Detail = "Host path not answering"
		comp.Reason = "the configured storage path stopped answering, so requests that touch stored files are failed immediately rather than piling up against it"
		if cause != nil {
			comp.Reason += ": " + cause.Error()
		}
		return comp

	default:
		// Neither mechanism applies because no backend is configured yet. That
		// is the fresh-install state, not a fault.
		comp.Status = "disabled"
		comp.Detail = "Not configured"
		comp.Reason = "core file storage has not been configured; features that store files are unavailable until it is"
		return comp
	}
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

// Healthz GET /healthz
//
// Unauthenticated infra readiness probe (Docker/Swarm HEALTHCHECK, load
// balancers). Pings only DB + Redis - the two dependencies Core cannot run
// without - skipping the heavier admin checks (nodes, gateway, metrics
// extension) that GetStatus (/api/admin/health) reports. Registered outside
// AuthMiddleware AND outside the /api setup-lock + maintenance middleware
// (see core/main.go), so it keeps answering during Fresh-Install/Lost-Admin
// setup states and while maintenance mode is active.
// Storage is REPORTED here but deliberately does NOT affect the status code.
// This endpoint is what Docker and Swarm consume to decide whether to kill and
// restart the container, and restarting Core cannot make an unreachable NAS or
// object store reachable. Gating on it would turn a storage blip into a restart
// loop that takes the panel and every running server's supervision down with
// it, converting a partial outage into a total one. The flag is there so an
// operator's own monitoring can see the state; the orchestrator ignores it.
//
// The same reasoning is applied to Redis, per class rather than wholesale. An
// UNREACHABLE Redis still fails this check: it can clear on its own, and a
// reschedule may land somewhere it is reachable. A rejected credential or a
// missing ACL permission does NOT, because a restart cannot repair either, and
// gating on them would restart-loop Core over a configuration mistake - taking
// down the panel an operator needs in order to fix it. Both are still reported
// in the body.
//
// The flag is coarse on purpose. This route is unauthenticated, so it must not
// carry the path, the bucket or the errno the admin endpoint reports. It is
// read from atomics only, adding no query to a path that runs on every health
// check and cannot be allowed to hang.
func (h *HealthHandler) Healthz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), healthCheckTimeout)
	defer cancel()

	dbErr := h.state.Store.Ping(ctx)
	redisErr := h.state.Redis.Ping(ctx).Err()

	// A rejected credential or a missing ACL permission must NOT fail this
	// check, for the same reason storage does not gate it: the orchestrator
	// responds to a failure by killing and restarting the container, and a
	// restart cannot repair a WRONGPASS or a NOPERM. It would restart-loop
	// instead, taking down the very panel an operator needs to fix the ACL and
	// turning a configuration mistake into a total outage. An unreachable Redis
	// still fails the check: that one can clear on its own, and a reschedule
	// may genuinely land somewhere it is reachable.
	//
	// Reported either way in the body, so an operator's own monitoring sees the
	// state even when the orchestrator is told to leave the container alone.
	redisFailure := database.ClassifyRedisError(redisErr)
	redisGates := redisFailure != database.RedisOK && !redisFailure.NeedsOperator()

	// Checked without consulting which backend is configured, which would cost
	// a settings read. The idle mechanism reports ok: the watchdog is stopped
	// when the backend is not a host path, and the s3 state is reset when it is
	// not s3, so at most one of these can be false at a time.
	storageOK := true
	if healthy, _ := h.state.StorageGate.Healthy(); !healthy {
		storageOK = false
	}
	if reconnecting, _, _ := h.state.StorageS3.State(); reconnecting {
		storageOK = false
	}

	w.Header().Set("Content-Type", "application/json")
	if dbErr != nil || redisGates {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "not ready",
			"db":      dbErr == nil,
			"redis":   redisErr == nil,
			"storage": storageOK,
		})
		return
	}

	// Reaching here with a Redis failure means it was one the orchestrator is
	// deliberately not asked to act on, so the body must still say so rather
	// than reporting a flat "ready" - the healthchecks discard it, but an
	// operator reading this endpoint by hand would otherwise be told everything
	// is fine while Redis is rejecting every command.
	status := "ready"
	if redisErr != nil {
		status = "degraded"
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  status,
		"db":      true,
		"redis":   redisErr == nil,
		"storage": storageOK,
	})
}
