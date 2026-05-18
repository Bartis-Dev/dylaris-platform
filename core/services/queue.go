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
