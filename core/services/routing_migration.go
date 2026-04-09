package services

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"dylaris-core/models"
	"dylaris-core/store"

	"github.com/redis/go-redis/v9"
)

const routingMigrationKey = "dylaris:routing_migration"

type MigrationStatus struct {
	Running bool `json:"running"`
	Total   int  `json:"total"`
	Done    int  `json:"done"`
	Failed  int  `json:"failed"`
}

type RoutingMigrationService struct {
	store store.Store
	queue *QueueService
	redis *redis.Client
	mu    sync.Mutex
}

func NewRoutingMigrationService(s store.Store, q *QueueService, r *redis.Client) *RoutingMigrationService {
	return &RoutingMigrationService{store: s, queue: q, redis: r}
}

// Run kicks off a background batch redeploy of all active servers.
// Returns the number of servers queued.
func (m *RoutingMigrationService) Run(ctx context.Context, newMode string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	existing := m.readStatus(ctx)
	if existing.Running {
		return 0, fmt.Errorf("migration already in progress")
	}

	servers, err := m.store.GetAllActiveServers()
	if err != nil {
		return 0, fmt.Errorf("failed to load servers: %w", err)
	}
	if len(servers) == 0 {
		return 0, nil
	}

	m.writeStatus(ctx, MigrationStatus{Running: true, Total: len(servers)})
	go m.runBatches(context.Background(), servers, newMode)
	return len(servers), nil
}

func (m *RoutingMigrationService) runBatches(ctx context.Context, servers []models.Server, newMode string) {
	const batchSize = 4
	done := 0
	failed := 0

	for i := 0; i < len(servers); i += batchSize {
		end := i + batchSize
		if end > len(servers) {
			end = len(servers)
		}
		batch := servers[i:end]

		var wg sync.WaitGroup
		var mu sync.Mutex

		for _, srv := range batch {
			wg.Add(1)
			go func(s models.Server) {
				defer wg.Done()
				if err := m.redeployServer(ctx, s, newMode); err != nil {
					log.Printf("[RoutingMigration] server %s failed: %v", s.UUID, err)
					mu.Lock()
					failed++
					mu.Unlock()
				} else {
					mu.Lock()
					done++
					mu.Unlock()
				}
				m.updateProgress(ctx, done, failed, len(servers))
			}(srv)
		}
		wg.Wait()

		if end < len(servers) {
			select {
			case <-ctx.Done():
				goto done
			case <-time.After(15 * time.Second):
			}
		}
	}
done:
	m.writeStatus(ctx, MigrationStatus{Running: false, Total: len(servers), Done: done, Failed: failed})
	log.Printf("[RoutingMigration] complete: %d done, %d failed", done, failed)
}

func (m *RoutingMigrationService) redeployServer(ctx context.Context, srv models.Server, newMode string) error {
	// NodeAddress carries the node token from GetAllActiveServers
	nodeToken := srv.NodeAddress

	type dockerCfg struct {
		RAM             int     `json:"ram"`
		CPULimit        float64 `json:"cpuLimit"`
		DiskLimit       int64   `json:"diskLimit"`
		Image           string  `json:"image"`
		Command         string  `json:"command"`
		ExtraJvmFlags   string  `json:"extraJvmFlags,omitempty"`
		HostPort        int     `json:"hostPort"`
		ContainerPort   int     `json:"containerPort"`
	}
	type serverCfg struct {
		UUID            string    `json:"uuid"`
		ActiveSubServer string    `json:"activeSubServer"`
		Docker          dockerCfg `json:"docker"`
	}

	cfg := serverCfg{
		UUID:            srv.UUID,
		ActiveSubServer: srv.ActiveSubServer,
		Docker: dockerCfg{
			RAM:           srv.Memory,
			CPULimit:      srv.CPULimit,
			DiskLimit:     srv.DiskLimit,
			Image:         srv.GameImage,
			Command:       srv.StartCommand,
			ExtraJvmFlags: srv.ExtraJvmFlags,
			// hostPort=0 → node auto-allocates (ip_port mode) or skips binding (gateway mode)
			HostPort:      0,
			ContainerPort: srv.ContainerPort,
		},
	}

	if err := m.queue.SendCommand(ctx, nodeToken, "update_resources", cfg, nil); err != nil {
		return err
	}

	// Poll up to 60s for the container to settle
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
		}
		latest, err := m.store.GetServerByUUID(srv.UUID)
		if err != nil {
			continue
		}
		switch latest.Status {
		case "stopped", "online", "offline":
			return nil
		}
	}

	// Timeout — force kill
	log.Printf("[RoutingMigration] server %s timed out, sending kill", srv.UUID)
	_ = m.queue.SendCommand(ctx, nodeToken, "kill", map[string]string{"uuid": srv.UUID}, nil)
	return nil
}

func (m *RoutingMigrationService) GetStatus(ctx context.Context) MigrationStatus {
	return m.readStatus(ctx)
}

func (m *RoutingMigrationService) readStatus(ctx context.Context) MigrationStatus {
	val, err := m.redis.Get(ctx, routingMigrationKey).Result()
	if err != nil {
		return MigrationStatus{}
	}
	var s MigrationStatus
	_ = json.Unmarshal([]byte(val), &s)
	return s
}

func (m *RoutingMigrationService) writeStatus(ctx context.Context, s MigrationStatus) {
	data, _ := json.Marshal(s)
	m.redis.Set(ctx, routingMigrationKey, data, 24*time.Hour)
}

func (m *RoutingMigrationService) updateProgress(ctx context.Context, done, failed, total int) {
	s := m.readStatus(ctx)
	s.Done = done
	s.Failed = failed
	s.Total = total
	m.writeStatus(ctx, s)
}
