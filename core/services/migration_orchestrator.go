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
	backupstorage "dylaris-core/storage/backup"
	"dylaris-core/store"

	"dylaris-pkg/migration"
	"dylaris-pkg/queue"

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
	// cpuPinning re-resolves a server's CPU pinning for the TARGET node at
	// cutover (auto recomputed, manual reset when the hardware differs). nil-safe.
	cpuPinning *CPUPinningService
}

// SetCPUPinning wires the CPU-pinning service. Call once at boot (optional).
func (o *MigrationOrchestrator) SetCPUPinning(c *CPUPinningService) { o.cpuPinning = c }

// migrationStreamKey is the durable Redis Stream the manual endpoint (and the
// rebalance worker) publish requests onto; the elected leader consumes them via
// a consumer group. New suffix avoids a WRONGTYPE collision with the old
// `dylaris:migration:queue` list.
const migrationStreamKey = "dylaris:migration:stream"

// migrationGroup / migrationConsumer name the single logical consumer. The
// consumer name is fixed (not per-instance) so when leadership moves to another
// Core, that Core's consumer recovers the previous leader's pending entries.
const (
	migrationGroup    = "migration"
	migrationConsumer = "migration-worker"
)

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
	StartedAt    int64  `json:"startedAt"` // unix seconds
	UpdatedAt    int64  `json:"updatedAt"` // unix seconds
}

const (
	migrationLockTTL           = 10 * time.Minute
	orchestrationStatusTTL     = time.Hour
	migrationStopTimeout       = 90 * time.Second
	migrationStageTimeout      = 5 * time.Minute  // archiving GBs on the source
	migrationTransferTimeout   = 30 * time.Minute // multi-GB transfer to target
	// migrationR2PhaseTimeout bounds each leg (upload, then download) of the
	// cross-LAN BYON R2 fallback. Longer than the LAN/overlay transfer because a
	// BYON home uplink is slow; still capped so one stuck transfer can't block the
	// serialized migration queue indefinitely. Within the BYON presign TTL (6h).
	migrationR2PhaseTimeout = 1 * time.Hour
	migrationStartTimeout      = 90 * time.Second // best-effort online confirm
	migrationPollInterval      = 2 * time.Second
	migrationTokenTTL          = 15 * time.Minute
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
	if _, err := queue.Publish(ctx, o.redis, migrationStreamKey, data); err != nil {
		return fmt.Errorf("enqueue migration request: %w", err)
	}
	return nil
}

// consume is the leader-gated durable-queue loop. When not leader it idles on a
// short timer so a follower that becomes leader picks up pending requests
// quickly without hot-spinning. When leader it runs a consumer-group reader
// (Concurrency 1 = one migration at a time) until leadership is lost.
//
// Durability: a request is ACKed only after Migrate returns, so a leader crash
// mid-migration leaves the request pending; the next leader recovers it (same
// fixed consumer name) and re-runs Migrate. The per-server lock makes the
// re-run safe — a stale lock from the dead leader makes the retry bail until the
// lock TTL expires, after which it proceeds. Source data is never deleted before
// cutover, so there is no data-loss window.
func (o *MigrationOrchestrator) consume(ctx context.Context) {
	consumer := queue.NewConsumer(o.redis, migrationStreamKey, migrationGroup, migrationConsumer)
	consumer.Concurrency = 1 // migrations are serialized

	handler := func(hctx context.Context, data []byte) error {
		var req MigrationRequest
		if err := json.Unmarshal(data, &req); err != nil {
			log.Printf("migration: bad request payload, dropping: %v", err)
			return nil // ack + drop malformed
		}
		o.Migrate(hctx, req)
		return nil // ack: Migrate records its own status/rollback
	}

	for {
		if ctx.Err() != nil {
			return
		}
		// Only the elected leader consumes; followers idle.
		if o.leader != nil && !o.leader.IsLeader() {
			select {
			case <-ctx.Done():
				return
			case <-time.After(migrationQueueBlockTimeout):
			}
			continue
		}
		// Run until this Core loses leadership (leaderCtx cancelled) or shutdown.
		leaderCtx, cancel := context.WithCancel(ctx)
		go o.watchLeadership(leaderCtx, cancel)
		_ = consumer.Run(leaderCtx, handler)
		cancel()
	}
}

// watchLeadership cancels leaderCtx as soon as this Core stops being the leader,
// so the migration consumer stops reading (only the leader migrates). A nil
// leader (single-Core dev mode) means run unconditionally.
func (o *MigrationOrchestrator) watchLeadership(ctx context.Context, cancel context.CancelFunc) {
	if o.leader == nil {
		return
	}
	t := time.NewTicker(migrationQueueBlockTimeout)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if !o.leader.IsLeader() {
				cancel()
				return
			}
		}
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
	// For BYON transfers (either node owned), also hand the target the source's
	// LAN IPs so a same-LAN move pulls directly instead of hairpinning the warp
	// overlay. Platform<->platform stays overlay-only (empty list).
	var sourcePrivateIPs []string
	if sourceNode.OwnerID != nil || targetNode.OwnerID != nil {
		sourcePrivateIPs = sourceNode.PrivateIPs
	}
	if err := o.queue.SendMigrateInCommand(ctx, targetNode.Token, srv.UUID, strconv.Itoa(sourceNode.ID), token, meta.SHA256, sourcePrivateIPs); err != nil {
		log.Printf("migration %s: migrate_in queue failed: %v", srv.UUID, err)
		o.rollbackPreCutover(ctx, srv, sourceNode, wasRunning, preStatus, writeStatus, "migrate_in queue failed")
		return
	}
	// migrate_in reports "transferred" on success, or "need_remote" when this is
	// a BYON move and the target cannot reach the source over the LAN. In the
	// latter case we transfer through R2 (node-direct, no warp hairpin) instead.
	phase, nerr := o.waitForNodePhaseAny(ctx, srv.UUID, map[string]bool{"transferred": true, "need_remote": true}, migrationTransferTimeout)
	if phase == "need_remote" {
		log.Printf("migration %s: source LAN unreachable, falling back to R2 transfer", srv.UUID)
		if err := o.transferViaR2(ctx, srv, sourceNode, targetNode, meta.SHA256); err != nil {
			// Still pre-cutover: node_id unchanged, source authoritative — roll back.
			log.Printf("migration %s: R2 transfer failed: %v", srv.UUID, err)
			o.rollbackPreCutover(ctx, srv, sourceNode, wasRunning, preStatus, writeStatus, "r2 transfer failed: "+err.Error())
			return
		}
		// R2 transfer verified the archive on the target — fall through to cutover.
	} else if phase != "transferred" {
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

	// Re-resolve CPU pinning for the target host. Different hardware (fewer cores,
	// different P/E or X3D layout) means the source cpuset is no longer valid or
	// meaningful: auto is recomputed on the target, manual is reset to shared
	// unless the target is identical hardware. Persisted here; a running pinned
	// server is recreated with the corrected cpuset below instead of a plain start.
	effCpuset, pinned := o.reResolvePinningForTarget(ctx, srv, sourceNode, targetNode)

	// --- (i) Start on target if it was running (best-effort) ---
	if wasRunning {
		o.store.UpdateServerStatus(srv.ID, "starting")
		o.store.UpdateServerDesiredState(srv.ID, "online")
		writeStatus("starting", "")
		var startErr error
		if pinned {
			// Recreate with the corrected cpuset (and current resources) so the
			// container lands on valid target cores, then it comes up online.
			startErr = o.queue.SendCommand(ctx, targetNode.Token, "update_resources", map[string]interface{}{
				"uuid":            srv.UUID,
				"activeSubServer": srv.ActiveSubServer,
				"docker": map[string]interface{}{
					"ram":        srv.Memory,
					"cpuLimit":   srv.CPULimit,
					"diskLimit":  srv.DiskLimit,
					"cpusetCpus": effCpuset,
					"image":      srv.GameImage,
					"command":    srv.StartCommand,
				},
			}, nil)
		} else {
			startErr = o.queue.SendCommand(ctx, targetNode.Token, "start", map[string]interface{}{"uuid": srv.UUID}, nil)
		}
		if startErr != nil {
			// Data is safe on the target; only the auto-start dispatch failed.
			log.Printf("migration %s: start on target queue failed: %v", srv.UUID, startErr)
			writeStatus("failed_post_cutover", "start on target failed: "+startErr.Error())
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

// waitForNodePhaseAny is waitForNodePhase for a SET of acceptable terminal
// phases. It returns as soon as the node reports any phase in `accept` (or
// "error", with its message), or the timeout elapses. Used by migrate_in, which
// can end in either "transferred" (got the copy) or "need_remote" (BYON LAN
// unreachable, use the R2 fallback).
func (o *MigrationOrchestrator) waitForNodePhaseAny(ctx context.Context, serverUUID string, accept map[string]bool, timeout time.Duration) (string, string) {
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
				if accept[st.Phase] {
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

// transferViaR2 is the cross-LAN BYON fallback transport. The source uploads its
// already-staged archive to a temporary R2 object (pre-signed PUT) and the target
// downloads it (pre-signed GET), verifies the sha256, and extracts — node-direct,
// $0 egress, no warp hairpin, and the two nodes never need to be reachable to
// each other. The transfer object is temporary (NOT a backup_run, so it never
// counts against the tenant's R2 quota) and is deleted when we're done, success
// or fail. On success the target has reported "transferred" exactly like
// migrate_in, so the caller proceeds to the normal cutover.
func (o *MigrationOrchestrator) transferViaR2(ctx context.Context, srv *models.Server, sourceNode, targetNode *models.Node, expectedSha256 string) error {
	bs, err := o.store.GetDefaultBackupStorage()
	if err != nil {
		return fmt.Errorf("resolve backup storage: %w", err)
	}
	if bs == nil || bs.Provider != "s3" {
		return fmt.Errorf("cross-LAN BYON transfer requires an S3/R2 backup storage (none configured)")
	}
	prov, err := backupstorage.Open(ctx, bs, backupstorage.Deps{})
	if err != nil {
		return fmt.Errorf("open backup storage: %w", err)
	}

	// One stable key per server (migrations are serialized, so no collision).
	key := fmt.Sprintf("migration-transfer/%s.zip", srv.UUID)
	// Use the BYON presign TTL (longer) since at least one leg is a slow home link.
	ttl := presignTTL(o.store, true)
	putURL, err := prov.UploadURL(ctx, key, ttl)
	if err != nil || putURL == "" {
		return fmt.Errorf("presign put url: %v", err)
	}
	getURL, err := prov.DownloadURL(ctx, key, ttl)
	if err != nil || getURL == "" {
		return fmt.Errorf("presign get url: %v", err)
	}
	// Always remove the temporary transfer object — on success it's redundant once
	// the target has it, on failure we must not leak it. Background ctx so a
	// cancelled migration still cleans up.
	defer func() {
		if derr := prov.Delete(context.Background(), key); derr != nil {
			log.Printf("migration %s: R2 transfer object cleanup failed (%s): %v", srv.UUID, key, derr)
		}
	}()

	// Source uploads its staged archive to R2.
	if err := o.queue.SendMigratePushR2Command(ctx, sourceNode.Token, srv.UUID, putURL); err != nil {
		return fmt.Errorf("queue migrate_push_r2: %w", err)
	}
	if phase, nerr := o.waitForNodePhase(ctx, srv.UUID, "pushed", migrationR2PhaseTimeout); phase != "pushed" {
		return fmt.Errorf("source R2 upload failed (phase=%s): %s", phase, nerr)
	}

	// Target downloads from R2, verifies the hash, extracts. Reports "transferred".
	if err := o.queue.SendMigratePullR2Command(ctx, targetNode.Token, srv.UUID, getURL, expectedSha256); err != nil {
		return fmt.Errorf("queue migrate_pull_r2: %w", err)
	}
	if phase, nerr := o.waitForNodePhase(ctx, srv.UUID, "transferred", migrationR2PhaseTimeout); phase != "transferred" {
		return fmt.Errorf("target R2 download failed (phase=%s): %s", phase, nerr)
	}
	return nil
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

// currentPlayerCount reads the live player count for a server. Thin wrapper
// over playerCountFromStats so the orchestrator and the rebalance worker share
// one parser.
func (o *MigrationOrchestrator) currentPlayerCount(ctx context.Context, serverUUID string) int {
	return playerCountFromStats(ctx, o.redis, serverUUID)
}

// playerCountFromStats reads the latest entry of the per-server stats buffer
// stream and returns the player count, or 0 if unreadable. The buffer entry's
// "data" field is a JSON StatsPayload with a lowercase "players" field.
func playerCountFromStats(ctx context.Context, rdb *redis.Client, serverUUID string) int {
	key := fmt.Sprintf("dylaris:server:%s:stats:buffer", serverUUID)
	msgs, err := rdb.XRevRangeN(ctx, key, "+", "-", 1).Result()
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
// reResolvePinningForTarget re-evaluates a server's CPU pinning for the node it
// just moved to. auto is recomputed on the target topology; manual is kept only
// when the target hardware signature matches the source and the cpuset is still
// valid, otherwise it resets to shared so the operator re-pins intentionally on
// the new hardware. shared is unchanged. Returns the effective cpuset to apply
// and whether the server is pinned (so the caller recreates instead of plain start).
func (o *MigrationOrchestrator) reResolvePinningForTarget(ctx context.Context, srv *models.Server, source, target *models.Node) (string, bool) {
	if o.cpuPinning == nil {
		return target.CpusetCpus, false
	}
	switch srv.CPUPinningMode {
	case "auto":
		cs, _ := o.cpuPinning.AutoCpuset(ctx, target.Token, target.ID, srv.ID, srv.CPULimit, target.CpusetCpus)
		if err := o.store.UpdateServerCPUPinning(srv.ID, "auto", cs); err != nil {
			log.Printf("migration %s: persist recomputed auto cpuset failed: %v", srv.UUID, err)
		}
		srv.Cpuset = cs
		if cs == "" {
			return target.CpusetCpus, false // target has not reported a topology yet
		}
		log.Printf("migration %s: auto cpuset recomputed for target %s -> %s", srv.UUID, target.Name, cs)
		return cs, true
	case "manual":
		srcSig, _ := o.redis.Get(ctx, fmt.Sprintf("dylaris:node:%s:cpu:sig", source.Token)).Result()
		tgtSig, _ := o.redis.Get(ctx, fmt.Sprintf("dylaris:node:%s:cpu:sig", target.Token)).Result()
		sameHW := srcSig != "" && srcSig == tgtSig
		if sameHW && o.cpuPinning.ValidateCpuset(ctx, target.Token, srv.Cpuset, target.CpusetCpus) == nil {
			return srv.Cpuset, true // identical hardware -> keep the explicit pin
		}
		if err := o.store.UpdateServerCPUPinning(srv.ID, "shared", ""); err != nil {
			log.Printf("migration %s: reset manual pinning failed: %v", srv.UUID, err)
		}
		srv.CPUPinningMode = "shared"
		srv.Cpuset = ""
		log.Printf("migration %s: manual cpuset reset to shared (target %s hardware differs)", srv.UUID, target.Name)
		return target.CpusetCpus, false
	default: // shared / empty
		return target.CpusetCpus, false
	}
}

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
