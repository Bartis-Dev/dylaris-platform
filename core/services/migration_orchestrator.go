package services

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"time"

	"dylaris-core/models"
	"dylaris-core/pkg/leader"
	"dylaris-core/store"

	"dylaris-pkg/migration"

	"github.com/redis/go-redis/v9"
)

// MigrationOrchestrator drives node-to-node server migrations (auto-move). It is
// leader-gated: only the elected Core consumes the request queue and runs the
// step machine, so manual moves and (Wave 4) rebalance moves share one executor
// and survive the requesting Core restarting.
//
// v1 limitations (acceptable, noted deliberately):
//   - Migrations are processed SEQUENTIALLY, one at a time. Safe and simple;
//     throughput is not a concern for an admin-triggered / rebalance flow.
//   - If the leader dies mid-migration the per-server lock (10m TTL) eventually
//     expires and the server may be left in "migrating". A future retry/worker
//     re-evaluates. We never delete source data before cutover, so no data loss.
type MigrationOrchestrator struct {
	store         store.Store
	redis         *redis.Client
	queue         *QueueService
	gateway       GatewayProvider
	leader        leader.Election
	clusterSecret string
}

// migrationQueueKey is the Redis list the manual endpoint (and Wave 4 worker)
// RPush requests onto; the leader BLPOPs them.
const migrationQueueKey = "dylaris:migration:queue"

// MigrationRequest is the JSON payload on the migration queue.
type MigrationRequest struct {
	ServerID     int    `json:"serverID"`
	TargetNodeID int    `json:"targetNodeID"`
	Reason       string `json:"reason"`      // "manual" | "rebalance"
	RequestedBy  string `json:"requestedBy"` // user ID or "system"
}

// orchestrationStatus is the orchestrator-owned progress record, written to
// dylaris:migration:<uuid>:orchestration so the panel (Wave 5) can poll. It is
// distinct from the node-owned :status / :meta keys — we never clobber those.
type orchestrationStatus struct {
	Phase        string `json:"phase"`
	Error        string `json:"error,omitempty"`
	SourceNodeID int    `json:"sourceNodeID"`
	TargetNodeID int    `json:"targetNodeID"`
	Reason       string `json:"reason"`
	StartedAt    int64  `json:"startedAt"`  // unix seconds
	UpdatedAt    int64  `json:"updatedAt"`  // unix seconds
}

const (
	migrationLockTTL          = 10 * time.Minute
	orchestrationStatusTTL    = time.Hour
	migrationStopTimeout      = 90 * time.Second
	migrationStageTimeout     = 5 * time.Minute  // archiving GBs on the source
	migrationTransferTimeout  = 30 * time.Minute // multi-GB transfer to target
	migrationStartTimeout     = 90 * time.Second // best-effort online confirm
	migrationPollInterval     = 2 * time.Second
	migrationTokenTTL         = 15 * time.Minute
	migrationQueueBlockTimeout = 5 * time.Second // BLPOP timeout; not a hot spin
)

func NewMigrationOrchestrator(s store.Store, r *redis.Client, q *QueueService, g GatewayProvider, clusterSecret string) *MigrationOrchestrator {
	return &MigrationOrchestrator{
		store:         s,
		redis:         r,
		queue:         q,
		gateway:       g,
		clusterSecret: clusterSecret,
	}
}

// SetLeader wires the leader-election gate. The queue consumer only runs on the
// elected Core; followers idle.
func (o *MigrationOrchestrator) SetLeader(l leader.Election) { o.leader = l }

// Start launches the queue-consumer goroutine. Returns immediately.
func (o *MigrationOrchestrator) Start(ctx context.Context) {
	log.Println("Migration Orchestrator started")
	go o.consume(ctx)
}

// EnqueueMigration pushes a migration request onto the queue. Callable from any
// Core (the manual endpoint, the Wave 4 worker); the leader executes it.
func (o *MigrationOrchestrator) EnqueueMigration(ctx context.Context, serverID, targetNodeID int, reason, requestedBy string) error {
	req := MigrationRequest{
		ServerID:     serverID,
		TargetNodeID: targetNodeID,
		Reason:       reason,
		RequestedBy:  requestedBy,
	}
	data, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal migration request: %w", err)
	}
	if err := o.redis.RPush(ctx, migrationQueueKey, data).Err(); err != nil {
		return fmt.Errorf("enqueue migration request: %w", err)
	}
	return nil
}

// consume is the leader-gated loop. When not leader it idles on a short timer so
// a follower that becomes leader picks up pending requests quickly without
// hot-spinning. When leader it BLPOPs requests and processes them one at a time.
func (o *MigrationOrchestrator) consume(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if o.leader != nil && !o.leader.IsLeader() {
			select {
			case <-ctx.Done():
				return
			case <-time.After(migrationQueueBlockTimeout):
			}
			continue
		}

		// BLPOP blocks up to the timeout, then returns redis.Nil. The bounded
		// block keeps us responsive to leadership changes + ctx cancellation
		// without a tight poll loop.
		res, err := o.redis.BLPop(ctx, migrationQueueBlockTimeout, migrationQueueKey).Result()
		if err == redis.Nil {
			continue // timed out, no work
		}
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("migration: BLPOP error: %v", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(migrationQueueBlockTimeout):
			}
			continue
		}
		// res = [key, value]
		if len(res) != 2 {
			continue
		}

		var req MigrationRequest
		if err := json.Unmarshal([]byte(res[1]), &req); err != nil {
			log.Printf("migration: bad request payload, dropping: %v", err)
			continue
		}

		o.Migrate(ctx, req)
	}
}

// Migrate runs the full step machine for one request. Errors are logged and
// surfaced via the orchestration status key; we never return them to a caller
// (this runs async on the leader).
func (o *MigrationOrchestrator) Migrate(ctx context.Context, req MigrationRequest) {
	// --- (a) Load + validate ---
	srv, err := o.store.GetServerByID(req.ServerID)
	if err != nil {
		log.Printf("migration: server %d not found: %v", req.ServerID, err)
		return
	}
	sourceNode, err := o.store.GetNodeByID(srv.NodeID)
	if err != nil {
		log.Printf("migration %s: source node %d not found: %v", srv.UUID, srv.NodeID, err)
		o.writeStatus(ctx, srv.UUID, "failed", "source node not found", srv.NodeID, req.TargetNodeID, req.Reason, time.Now())
		return
	}
	targetNode, err := o.store.GetNodeByID(req.TargetNodeID)
	if err != nil {
		log.Printf("migration %s: target node %d not found: %v", srv.UUID, req.TargetNodeID, err)
		o.writeStatus(ctx, srv.UUID, "failed", "target node not found", srv.NodeID, req.TargetNodeID, req.Reason, time.Now())
		return
	}

	startedAt := time.Now()
	writeStatus := func(phase, errMsg string) {
		o.writeStatus(ctx, srv.UUID, phase, errMsg, sourceNode.ID, targetNode.ID, req.Reason, startedAt)
	}

	// Migration is gateway-only: a server's reachable address is tied to its
	// node unless the gateway re-points the route, so a move without it breaks
	// the server.
	if !o.gatewayEnabled() {
		log.Printf("migration %s: gateway disabled, refusing", srv.UUID)
		writeStatus("failed", "gateway routing disabled")
		return
	}
	if targetNode.ID == sourceNode.ID {
		log.Printf("migration %s: target == source (%d), refusing", srv.UUID, targetNode.ID)
		writeStatus("failed", "target node equals source node")
		return
	}
	if targetNode.Status != "online" {
		log.Printf("migration %s: target node %d not online (%s)", srv.UUID, targetNode.ID, targetNode.Status)
		writeStatus("failed", "target node not online")
		return
	}

	// --- (b) Acquire per-server lock ---
	lockKey := fmt.Sprintf("dylaris:server:%s:migration", srv.UUID)
	acquired, err := o.redis.SetNX(ctx, lockKey, req.RequestedBy, migrationLockTTL).Result()
	if err != nil {
		log.Printf("migration %s: lock SETNX error: %v", srv.UUID, err)
		writeStatus("failed", "lock error")
		return
	}
	if !acquired {
		log.Printf("migration %s: already migrating (lock held), skipping", srv.UUID)
		return
	}
	defer o.redis.Del(context.Background(), lockKey)

	// --- (c) Starting ---
	writeStatus("starting", "")
	wasRunning := srv.Status == "online"
	preStatus := srv.Status

	// --- (d) Rebalance player gate (manual moves skip — admin chose to move) ---
	if req.Reason == "rebalance" && wasRunning {
		if players := o.currentPlayerCount(ctx, srv.UUID); players > 0 {
			log.Printf("migration %s: aborting rebalance, %d players online", srv.UUID, players)
			writeStatus("aborted_players", fmt.Sprintf("%d players online", players))
			return
		}
	}

	// --- (e) Stop the running server ---
	if wasRunning {
		writeStatus("migrating", "")
		if err := o.stopServer(ctx, srv, sourceNode); err != nil {
			log.Printf("migration %s: stop failed: %v", srv.UUID, err)
			o.rollbackPreCutover(ctx, srv, sourceNode, wasRunning, preStatus, writeStatus, "stop failed")
			return
		}
		if !o.waitForStopped(ctx, srv.ID, migrationStopTimeout) {
			log.Printf("migration %s: stop timed out", srv.UUID)
			o.rollbackPreCutover(ctx, srv, sourceNode, wasRunning, preStatus, writeStatus, "stop timed out")
			return
		}
	}

	// --- (f) migrate_out (source stages archive) ---
	o.store.UpdateServerStatus(srv.ID, "migrating")
	writeStatus("migrating", "")
	if err := o.queue.SendMigrateOutCommand(ctx, sourceNode.Token, srv.UUID); err != nil {
		log.Printf("migration %s: migrate_out queue failed: %v", srv.UUID, err)
		o.rollbackPreCutover(ctx, srv, sourceNode, wasRunning, preStatus, writeStatus, "migrate_out queue failed")
		return
	}
	if phase, nerr := o.waitForNodePhase(ctx, srv.UUID, "staged", migrationStageTimeout); phase != "staged" {
		log.Printf("migration %s: staging failed (phase=%s, err=%s)", srv.UUID, phase, nerr)
		o.rollbackPreCutover(ctx, srv, sourceNode, wasRunning, preStatus, writeStatus, "staging failed: "+nerr)
		return
	}

	// --- (g) migrate_in (target pulls + verifies) ---
	meta, err := o.readMeta(ctx, srv.UUID)
	if err != nil {
		log.Printf("migration %s: cannot read meta: %v", srv.UUID, err)
		o.rollbackPreCutover(ctx, srv, sourceNode, wasRunning, preStatus, writeStatus, "meta read failed")
		return
	}
	token, err := migration.MintToken(o.clusterSecret, srv.UUID, strconv.Itoa(sourceNode.ID), migrationTokenTTL)
	if err != nil {
		log.Printf("migration %s: mint token failed: %v", srv.UUID, err)
		o.rollbackPreCutover(ctx, srv, sourceNode, wasRunning, preStatus, writeStatus, "token mint failed")
		return
	}
	// sourceNodeID is the node ID as a string — the target resolves the pull
	// endpoint via dylaris:migration:endpoint:<sourceNodeID>.
	if err := o.queue.SendMigrateInCommand(ctx, targetNode.Token, srv.UUID, strconv.Itoa(sourceNode.ID), token, meta.SHA256); err != nil {
		log.Printf("migration %s: migrate_in queue failed: %v", srv.UUID, err)
		o.rollbackPreCutover(ctx, srv, sourceNode, wasRunning, preStatus, writeStatus, "migrate_in queue failed")
		return
	}
	if phase, nerr := o.waitForNodePhase(ctx, srv.UUID, "transferred", migrationTransferTimeout); phase != "transferred" {
		// Target self-cleans its partial (Wave 2b). node_id is NOT yet changed,
		// so the source remains authoritative — safe to roll back.
		log.Printf("migration %s: transfer failed (phase=%s, err=%s)", srv.UUID, phase, nerr)
		o.rollbackPreCutover(ctx, srv, sourceNode, wasRunning, preStatus, writeStatus, "transfer failed: "+nerr)
		return
	}

	// --- (h) CUTOVER: flip node_id + re-point route. Target now authoritative. ---
	if err := o.store.UpdateServerNode(srv.ID, targetNode.ID); err != nil {
		log.Printf("migration %s: UpdateServerNode failed: %v", srv.UUID, err)
		// Pre-cutover from the data POV (node_id flip failed, source still owns
		// data), so roll back like the earlier steps.
		o.rollbackPreCutover(ctx, srv, sourceNode, wasRunning, preStatus, writeStatus, "node reassign failed")
		return
	}
	if err := o.gateway.MigrateServerRoutes(uint(srv.ID), uint(targetNode.ID)); err != nil {
		// node_id already flipped; data is on the target. Route re-point failed,
		// but reverting node_id would point the route back at data the source is
		// about to be told to keep — and we have not sent cleanup yet. Surface
		// the failure; the route can be re-pointed manually / on retry. Do not
		// revert node_id (target is authoritative now).
		log.Printf("migration %s: MigrateServerRoutes failed post node-flip: %v", srv.UUID, err)
		writeStatus("failed_post_cutover", "route re-point failed: "+err.Error())
		return
	}

	// --- (i) Start on target if it was running (best-effort) ---
	if wasRunning {
		o.store.UpdateServerStatus(srv.ID, "starting")
		o.store.UpdateServerDesiredState(srv.ID, "online")
		writeStatus("starting", "")
		if err := o.queue.SendCommand(ctx, targetNode.Token, "start", map[string]interface{}{"uuid": srv.UUID}, nil); err != nil {
			// Data is safe on the target; only the auto-start dispatch failed.
			log.Printf("migration %s: start on target queue failed: %v", srv.UUID, err)
			writeStatus("failed_post_cutover", "start on target failed: "+err.Error())
			// Still send cleanup — the move itself succeeded.
		} else {
			// Best-effort confirm; do not fail the migration if start is slow.
			o.waitForOnline(ctx, srv.ID, migrationStartTimeout)
		}
	} else {
		o.store.UpdateServerStatus(srv.ID, "stopped")
		o.store.UpdateServerDesiredState(srv.ID, "stopped")
	}

	// --- (j) Cleanup source copy ---
	if err := o.queue.SendMigrateCleanupCommand(ctx, sourceNode.Token, srv.UUID); err != nil {
		// Non-fatal: the move succeeded, this just leaves a stale copy on the
		// source that an operator / orphan sweep can reclaim.
		log.Printf("migration %s: cleanup queue failed (stale copy left on source %d): %v", srv.UUID, sourceNode.ID, err)
	}

	// --- (k) Final status ---
	// The server's status was already set in step (i): "starting" if it was
	// running (StatusWatcher will flip it to "online") or "stopped" otherwise.
	writeStatus("done", "")
	log.Printf("migration %s: done (node %d -> %d, reason=%s)", srv.UUID, sourceNode.ID, targetNode.ID, req.Reason)
}

// rollbackPreCutover restores the server's DB status to its pre-migration value
// and, if it was running and we stopped it, re-starts it on the SOURCE node.
// Never deletes source data — by definition this is called before cutover, so
// the source is still authoritative. Writes orchestration phase "failed".
func (o *MigrationOrchestrator) rollbackPreCutover(ctx context.Context, srv *models.Server, sourceNode *models.Node, wasRunning bool, preStatus string, writeStatus func(string, string), reason string) {
	if wasRunning {
		o.store.UpdateServerDesiredState(srv.ID, "online")
		o.store.UpdateServerStatus(srv.ID, "starting")
		if err := o.queue.SendCommand(ctx, sourceNode.Token, "start", map[string]interface{}{"uuid": srv.UUID}, nil); err != nil {
			log.Printf("migration %s: rollback restart on source failed: %v", srv.UUID, err)
		}
	} else {
		o.store.UpdateServerStatus(srv.ID, preStatus)
	}
	writeStatus("failed", reason)
}

// --- Helpers ---

func (o *MigrationOrchestrator) gatewayEnabled() bool {
	mode, _ := o.store.GetSetting("routing_mode")
	if mode == "" {
		mode = "ip_port"
	}
	return mode == "gateway" || mode == "both"
}

// stopServer sends a stop and sets desired_state=stopped so the node reconciler
// doesn't fight the stop by restarting the container.
func (o *MigrationOrchestrator) stopServer(ctx context.Context, srv *models.Server, node *models.Node) error {
	o.store.UpdateServerDesiredState(srv.ID, "stopped")
	o.store.UpdateServerStatus(srv.ID, "stopping")
	return o.queue.SendCommand(ctx, node.Token, "stop", map[string]interface{}{"uuid": srv.UUID}, nil)
}

// waitForStopped polls the DB status (synced from Redis by StatusWatcher) until
// the server reports stopped/offline or the timeout elapses.
func (o *MigrationOrchestrator) waitForStopped(ctx context.Context, serverID int, timeout time.Duration) bool {
	return o.pollDBStatus(ctx, serverID, timeout, func(status string) bool {
		return status == "stopped" || status == "offline"
	})
}

// waitForOnline polls the DB status until "online" or the timeout. Best-effort.
func (o *MigrationOrchestrator) waitForOnline(ctx context.Context, serverID int, timeout time.Duration) bool {
	return o.pollDBStatus(ctx, serverID, timeout, func(status string) bool {
		return status == "online"
	})
}

func (o *MigrationOrchestrator) pollDBStatus(ctx context.Context, serverID int, timeout time.Duration, done func(string) bool) bool {
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(migrationPollInterval)
	defer ticker.Stop()
	for {
		srv, err := o.store.GetServerByID(serverID)
		if err == nil && done(srv.Status) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
		}
	}
}

// waitForNodePhase polls the node-owned dylaris:migration:<uuid>:status key until
// it reaches wantPhase or "error", or the timeout elapses. Returns the last
// observed phase + any error message. A timeout returns the last phase seen
// (often "" if the node never wrote one).
func (o *MigrationOrchestrator) waitForNodePhase(ctx context.Context, serverUUID, wantPhase string, timeout time.Duration) (string, string) {
	key := fmt.Sprintf("dylaris:migration:%s:status", serverUUID)
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(migrationPollInterval)
	defer ticker.Stop()

	lastPhase := ""
	for {
		raw, err := o.redis.Get(ctx, key).Result()
		if err == nil && raw != "" {
			var st struct {
				Phase string `json:"phase"`
				Error string `json:"error,omitempty"`
			}
			if json.Unmarshal([]byte(raw), &st) == nil {
				lastPhase = st.Phase
				if st.Phase == wantPhase {
					return st.Phase, ""
				}
				if st.Phase == "error" {
					return st.Phase, st.Error
				}
			}
		}
		if time.Now().After(deadline) {
			return lastPhase, "timed out"
		}
		select {
		case <-ctx.Done():
			return lastPhase, "context cancelled"
		case <-ticker.C:
		}
	}
}

// nodeMeta mirrors the node-owned dylaris:migration:<uuid>:meta JSON.
type nodeMeta struct {
	SHA256       string `json:"sha256"`
	Size         int64  `json:"size"`
	SourceNodeID string `json:"sourceNodeID"`
	StagedAt     int64  `json:"stagedAt"`
}

func (o *MigrationOrchestrator) readMeta(ctx context.Context, serverUUID string) (nodeMeta, error) {
	key := fmt.Sprintf("dylaris:migration:%s:meta", serverUUID)
	raw, err := o.redis.Get(ctx, key).Result()
	if err != nil {
		return nodeMeta{}, err
	}
	var m nodeMeta
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return nodeMeta{}, err
	}
	if m.SHA256 == "" {
		return nodeMeta{}, fmt.Errorf("meta missing sha256")
	}
	return m, nil
}

// currentPlayerCount reads the latest entry of the per-server stats buffer
// stream and returns the player count, or 0 if unreadable. The buffer entry's
// "data" field is a JSON StatsPayload with a lowercase "players" field.
func (o *MigrationOrchestrator) currentPlayerCount(ctx context.Context, serverUUID string) int {
	key := fmt.Sprintf("dylaris:server:%s:stats:buffer", serverUUID)
	msgs, err := o.redis.XRevRangeN(ctx, key, "+", "-", 1).Result()
	if err != nil || len(msgs) == 0 {
		return 0
	}
	raw, ok := msgs[0].Values["data"].(string)
	if !ok || raw == "" {
		return 0
	}
	var p struct {
		Players int `json:"players"`
	}
	if json.Unmarshal([]byte(raw), &p) != nil {
		return 0
	}
	return p.Players
}

// writeStatus writes the orchestration progress record (best-effort).
func (o *MigrationOrchestrator) writeStatus(ctx context.Context, serverUUID, phase, errMsg string, sourceNodeID, targetNodeID int, reason string, startedAt time.Time) {
	st := orchestrationStatus{
		Phase:        phase,
		Error:        errMsg,
		SourceNodeID: sourceNodeID,
		TargetNodeID: targetNodeID,
		Reason:       reason,
		StartedAt:    startedAt.Unix(),
		UpdatedAt:    time.Now().Unix(),
	}
	data, err := json.Marshal(st)
	if err != nil {
		return
	}
	key := fmt.Sprintf("dylaris:migration:%s:orchestration", serverUUID)
	if err := o.redis.Set(ctx, key, data, orchestrationStatusTTL).Err(); err != nil {
		log.Printf("migration %s: failed to write orchestration status: %v", serverUUID, err)
	}
}
