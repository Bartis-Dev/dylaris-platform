package services

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"dylaris-core/services/redisacl"

	"dylaris-pkg/queue"

	"github.com/redis/go-redis/v9"
)

// QueueService handles the safe sending of commands to Nodes
type QueueService struct {
	redis *redis.Client
	// acl is the optional per-node Redis ACL handshake. nil = feature off / not
	// wired, in which case SendCommand behaves exactly as before.
	acl *redisacl.Handshake
}

func NewQueueService(r *redis.Client) *QueueService {
	return &QueueService{redis: r}
}

// SetACL wires the optional per-node Redis ACL handshake. nil = feature off.
func (q *QueueService) SetACL(h *redisacl.Handshake) { q.acl = h }

// nodeCmdStream is the per-node command stream key. It is a Redis Stream
// (durable, consumer-group based via dylaris-pkg/queue), replacing the old
// RPUSH/BLPOP list `dylaris:node:%s:queue` which lost commands if the node
// crashed mid-processing. The new suffix avoids a WRONGTYPE collision with any
// leftover list at the old key.
func nodeCmdStream(nodeToken string) string {
	return fmt.Sprintf("dylaris:node:%s:cmds", nodeToken)
}

// NodeCommand is the exact structure the Node expects
type NodeCommand struct {
	Action    string      `json:"action"`
	Config    interface{} `json:"config"`
	Installer interface{} `json:"installer,omitempty"`
}

// SendCommand pushes a command into the Node's queue (mailbox).
// Even if the Node is offline, the command remains in Redis until it comes back online.
func (q *QueueService) SendCommand(ctx context.Context, nodeToken string, action string, config interface{}, installer interface{}) error {
	// Pre-placement ACL ensure: the node keeps a long-lived gRPC connection, so
	// Core only re-applies its full Redis ACL on (re)connect. For actions that
	// add/keep server keys, re-apply now so the node has key access by the time
	// it processes this command. Gated + nil-safe; never fails the command (the
	// on-connect re-apply is the safety net, and the node retries on NOPERM).
	if q.acl != nil && aclRelevantAction(action) {
		if err := q.acl.EnsureForToken(ctx, nodeToken); err != nil {
			log.Printf("redisacl: pre-placement ACL ensure failed for node %s (action %s): %v — node self-heals on next reconnect", nodeToken, action, err)
		}
	}

	stream := nodeCmdStream(nodeToken)

	cmd := NodeCommand{
		Action:    action,
		Config:    config,
		Installer: installer,
	}

	jsonData, err := json.Marshal(cmd)
	if err != nil {
		return fmt.Errorf("failed to marshal command: %w", err)
	}

	// XADD appends the JSON payload to the node's command stream. Even if the
	// Node is offline, the command persists until it reads + ACKs it.
	if _, err = queue.Publish(ctx, q.redis, stream, jsonData); err != nil {
		return fmt.Errorf("failed to push to node command stream: %w", err)
	}

	return nil
}

// aclRelevantAction reports whether an action adds/keeps server keys on the node,
// so the node's ACL must already grant them before the command is processed.
// Only actions that actually flow through SendCommand are listed. Migration
// (migrate_in / migrate_pull_r2) uses dedicated Send* methods and is NOT covered
// here; BYON migration is a future path (Phase 4), and its ACL window will be
// closed at the node_id cutover when that ships. See the plan's migration note.
func aclRelevantAction(action string) bool {
	switch action {
	case "create", "setup", "reinstall", "switch_server", "update_resources":
		return true
	}
	return false
}

// SendRawCommand publishes a pre-built payload directly to the node's command
// stream, bypassing the NodeCommand{Action, Config, Installer} envelope
// SendCommand uses. Some commands (backup_run, backup_restore) carry their
// fields at the TOP LEVEL of the JSON, matching the migrate_* commands'
// pattern above (SendMigrateInCommand etc.) rather than nested under
// "config" - the node's decoder for those actions re-parses the raw stream
// payload into its own struct (e.g. BackupRunCommand), so wrapping it in
// NodeCommand's envelope would change the wire shape it expects. payload is
// marshaled as-is: callers own its exact field set (it must already include
// its own "action" field). Not ACL pre-placement gated like SendCommand -
// backup actions operate on an already-provisioned server key, they never
// add one, so aclRelevantAction's pre-placement re-apply does not apply here.
func (q *QueueService) SendRawCommand(ctx context.Context, nodeToken string, payload interface{}) error {
	stream := nodeCmdStream(nodeToken)

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal command: %w", err)
	}

	if _, err = queue.Publish(ctx, q.redis, stream, jsonData); err != nil {
		return fmt.Errorf("failed to push to node command stream: %w", err)
	}

	return nil
}

// SendProxyNetworkCommand queues one of the proxy_network_* lifecycle
// commands. For create/destroy, serverUUID is the proxy UUID and proxyUUID
// can be empty. For connect/disconnect, serverUUID is the game-server
// container and proxyUUID is the proxy whose network it should join/leave.
func (q *QueueService) SendProxyNetworkCommand(ctx context.Context, nodeToken, action, serverUUID, proxyUUID string) error {
	stream := nodeCmdStream(nodeToken)
	type proxyNetCmd struct {
		Action    string                 `json:"action"`
		Config    map[string]interface{} `json:"config"`
		ProxyUUID string                 `json:"proxyUuid,omitempty"`
	}
	cmd := proxyNetCmd{
		Action:    action,
		Config:    map[string]interface{}{"uuid": serverUUID},
		ProxyUUID: proxyUUID,
	}
	jsonData, err := json.Marshal(cmd)
	if err != nil {
		return fmt.Errorf("failed to marshal proxy_network command: %w", err)
	}
	_, err = queue.Publish(ctx, q.redis, stream, jsonData)
	return err
}

// SendMigrateCommand queues a migrate_storage command for the given server.
func (q *QueueService) SendMigrateCommand(ctx context.Context, nodeToken, serverUUID, targetPath string) error {
	stream := nodeCmdStream(nodeToken)

	type migrateCmd struct {
		Action     string                 `json:"action"`
		Config     map[string]interface{} `json:"config"`
		TargetPath string                 `json:"targetPath"`
	}

	cmd := migrateCmd{
		Action:     "migrate_storage",
		Config:     map[string]interface{}{"uuid": serverUUID},
		TargetPath: targetPath,
	}

	jsonData, err := json.Marshal(cmd)
	if err != nil {
		return fmt.Errorf("failed to marshal migrate command: %w", err)
	}

	_, err = queue.Publish(ctx, q.redis, stream, jsonData)
	return err
}

// SendMigrateOutCommand queues a migrate_out (auto-move) command. The source
// node stages the (already-stopped) server directory as a zip and publishes its
// hash to Redis. Distinct from migrate_storage above, which moves between local
// storage paths on the same node.
func (q *QueueService) SendMigrateOutCommand(ctx context.Context, nodeToken, serverUUID string) error {
	stream := nodeCmdStream(nodeToken)

	type migrateOutCmd struct {
		Action string                 `json:"action"`
		Config map[string]interface{} `json:"config"`
	}
	cmd := migrateOutCmd{
		Action: "migrate_out",
		Config: map[string]interface{}{"uuid": serverUUID},
	}

	jsonData, err := json.Marshal(cmd)
	if err != nil {
		return fmt.Errorf("failed to marshal migrate_out command: %w", err)
	}
	_, err = queue.Publish(ctx, q.redis, stream, jsonData)
	return err
}

// SendMigrateInCommand queues a migrate_in (auto-move) command. The target node
// pulls the staged archive from sourceNodeID using token, verifies it against
// expectedSha256, and extracts it. The move parameters ride as top-level fields
// (matching the node's NodeCommand shape), not inside Config.
func (q *QueueService) SendMigrateInCommand(ctx context.Context, nodeToken, serverUUID, sourceNodeID, token, expectedSha256 string, sourcePrivateIPs []string) error {
	stream := nodeCmdStream(nodeToken)

	type migrateInCmd struct {
		Action         string                 `json:"action"`
		Config         map[string]interface{} `json:"config"`
		SourceNodeID   string                 `json:"sourceNodeId"`
		MigrateToken   string                 `json:"migrateToken"`
		ExpectedSha256 string                 `json:"expectedSha256"`
		// SourcePrivateIPs are the source node's LAN host IPs. When set (BYON
		// transfers), the target probes them first so a same-LAN move pulls
		// directly over the LAN instead of hairpinning through the warp overlay.
		SourcePrivateIPs []string `json:"sourcePrivateIps,omitempty"`
	}
	cmd := migrateInCmd{
		Action:           "migrate_in",
		Config:           map[string]interface{}{"uuid": serverUUID},
		SourceNodeID:     sourceNodeID,
		MigrateToken:     token,
		ExpectedSha256:   expectedSha256,
		SourcePrivateIPs: sourcePrivateIPs,
	}

	jsonData, err := json.Marshal(cmd)
	if err != nil {
		return fmt.Errorf("failed to marshal migrate_in command: %w", err)
	}
	_, err = queue.Publish(ctx, q.redis, stream, jsonData)
	return err
}

// SendMigratePushR2Command queues a migrate_push_r2 command (cross-LAN BYON
// fallback). The source node uploads its already-staged archive to the pre-signed
// PUT URL and reports phase "pushed". The URL carries its own auth, so the node
// never receives bucket credentials.
func (q *QueueService) SendMigratePushR2Command(ctx context.Context, nodeToken, serverUUID, putURL string) error {
	stream := nodeCmdStream(nodeToken)

	type migratePushR2Cmd struct {
		Action          string                 `json:"action"`
		Config          map[string]interface{} `json:"config"`
		PresignedPutURL string                 `json:"presignedPutUrl"`
	}
	cmd := migratePushR2Cmd{
		Action:          "migrate_push_r2",
		Config:          map[string]interface{}{"uuid": serverUUID},
		PresignedPutURL: putURL,
	}

	jsonData, err := json.Marshal(cmd)
	if err != nil {
		return fmt.Errorf("failed to marshal migrate_push_r2 command: %w", err)
	}
	_, err = queue.Publish(ctx, q.redis, stream, jsonData)
	return err
}

// SendMigratePullR2Command queues a migrate_pull_r2 command (cross-LAN BYON
// fallback). The target node downloads from the pre-signed GET URL, verifies the
// archive against expectedSha256, extracts it, and reports phase "transferred" —
// the same terminal phase as migrate_in, so cutover proceeds identically.
func (q *QueueService) SendMigratePullR2Command(ctx context.Context, nodeToken, serverUUID, getURL, expectedSha256 string) error {
	stream := nodeCmdStream(nodeToken)

	type migratePullR2Cmd struct {
		Action          string                 `json:"action"`
		Config          map[string]interface{} `json:"config"`
		PresignedGetURL string                 `json:"presignedGetUrl"`
		ExpectedSha256  string                 `json:"expectedSha256"`
	}
	cmd := migratePullR2Cmd{
		Action:          "migrate_pull_r2",
		Config:          map[string]interface{}{"uuid": serverUUID},
		PresignedGetURL: getURL,
		ExpectedSha256:  expectedSha256,
	}

	jsonData, err := json.Marshal(cmd)
	if err != nil {
		return fmt.Errorf("failed to marshal migrate_pull_r2 command: %w", err)
	}
	_, err = queue.Publish(ctx, q.redis, stream, jsonData)
	return err
}

// SendMigrateCleanupCommand queues a migrate_cleanup (auto-move) command. The
// source node deletes the staged archive and the original server directory.
// Sent only after the target confirms transfer; the orchestrator owns ordering.
func (q *QueueService) SendMigrateCleanupCommand(ctx context.Context, nodeToken, serverUUID string) error {
	stream := nodeCmdStream(nodeToken)

	type migrateCleanupCmd struct {
		Action string                 `json:"action"`
		Config map[string]interface{} `json:"config"`
	}
	cmd := migrateCleanupCmd{
		Action: "migrate_cleanup",
		Config: map[string]interface{}{"uuid": serverUUID},
	}

	jsonData, err := json.Marshal(cmd)
	if err != nil {
		return fmt.Errorf("failed to marshal migrate_cleanup command: %w", err)
	}
	_, err = queue.Publish(ctx, q.redis, stream, jsonData)
	return err
}
