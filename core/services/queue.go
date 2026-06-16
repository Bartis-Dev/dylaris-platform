package services

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/redis/go-redis/v9"
)

// QueueService handles the safe sending of commands to Nodes
type QueueService struct {
	redis *redis.Client
}

func NewQueueService(r *redis.Client) *QueueService {
	return &QueueService{redis: r}
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
	queueKey := fmt.Sprintf("dylaris:node:%s:queue", nodeToken)

	cmd := NodeCommand{
		Action:    action,
		Config:    config,
		Installer: installer,
	}

	jsonData, err := json.Marshal(cmd)
	if err != nil {
		return fmt.Errorf("failed to marshal command: %w", err)
	}

	// RPush appends the JSON payload to the end of the queue
	err = q.redis.RPush(ctx, queueKey, jsonData).Err()
	if err != nil {
		return fmt.Errorf("failed to push to redis queue: %w", err)
	}

	return nil
}

// SendProxyNetworkCommand queues one of the proxy_network_* lifecycle
// commands. For create/destroy, serverUUID is the proxy UUID and proxyUUID
// can be empty. For connect/disconnect, serverUUID is the game-server
// container and proxyUUID is the proxy whose network it should join/leave.
func (q *QueueService) SendProxyNetworkCommand(ctx context.Context, nodeToken, action, serverUUID, proxyUUID string) error {
	queueKey := fmt.Sprintf("dylaris:node:%s:queue", nodeToken)
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
	return q.redis.RPush(ctx, queueKey, jsonData).Err()
}

// SendMigrateCommand queues a migrate_storage command for the given server.
func (q *QueueService) SendMigrateCommand(ctx context.Context, nodeToken, serverUUID, targetPath string) error {
	queueKey := fmt.Sprintf("dylaris:node:%s:queue", nodeToken)

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

	return q.redis.RPush(ctx, queueKey, jsonData).Err()
}

// SendMigrateOutCommand queues a migrate_out (auto-move) command. The source
// node stages the (already-stopped) server directory as a zip and publishes its
// hash to Redis. Distinct from migrate_storage above, which moves between local
// storage paths on the same node.
func (q *QueueService) SendMigrateOutCommand(ctx context.Context, nodeToken, serverUUID string) error {
	queueKey := fmt.Sprintf("dylaris:node:%s:queue", nodeToken)

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
	return q.redis.RPush(ctx, queueKey, jsonData).Err()
}

// SendMigrateInCommand queues a migrate_in (auto-move) command. The target node
// pulls the staged archive from sourceNodeID using token, verifies it against
// expectedSha256, and extracts it. The move parameters ride as top-level fields
// (matching the node's NodeCommand shape), not inside Config.
func (q *QueueService) SendMigrateInCommand(ctx context.Context, nodeToken, serverUUID, sourceNodeID, token, expectedSha256 string) error {
	queueKey := fmt.Sprintf("dylaris:node:%s:queue", nodeToken)

	type migrateInCmd struct {
		Action         string                 `json:"action"`
		Config         map[string]interface{} `json:"config"`
		SourceNodeID   string                 `json:"sourceNodeId"`
		MigrateToken   string                 `json:"migrateToken"`
		ExpectedSha256 string                 `json:"expectedSha256"`
	}
	cmd := migrateInCmd{
		Action:         "migrate_in",
		Config:         map[string]interface{}{"uuid": serverUUID},
		SourceNodeID:   sourceNodeID,
		MigrateToken:   token,
		ExpectedSha256: expectedSha256,
	}

	jsonData, err := json.Marshal(cmd)
	if err != nil {
		return fmt.Errorf("failed to marshal migrate_in command: %w", err)
	}
	return q.redis.RPush(ctx, queueKey, jsonData).Err()
}

// SendMigrateCleanupCommand queues a migrate_cleanup (auto-move) command. The
// source node deletes the staged archive and the original server directory.
// Sent only after the target confirms transfer; the orchestrator owns ordering.
func (q *QueueService) SendMigrateCleanupCommand(ctx context.Context, nodeToken, serverUUID string) error {
	queueKey := fmt.Sprintf("dylaris:node:%s:queue", nodeToken)

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
	return q.redis.RPush(ctx, queueKey, jsonData).Err()
}
