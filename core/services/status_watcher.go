package services

import (
	"context"
	"dylaris-core/store"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

type StatusWatcherService struct {
	store store.Store
	redis *redis.Client
}

func NewStatusWatcherService(s store.Store, r *redis.Client) *StatusWatcherService {
	return &StatusWatcherService{store: s, redis: r}
}

func (s *StatusWatcherService) Start() {
	log.Println("Status Watcher Service started")
	ticker := time.NewTicker(5 * time.Second)
	go func() {
		for range ticker.C {
			s.scan()
		}
	}()
}

func (s *StatusWatcherService) scan() {
	ctx := context.Background()
	keys, err := s.redis.Keys(ctx, "dylaris:server:*:status").Result()
	if err != nil {
		return
	}

	for _, key := range keys {
		// Key format: dylaris:server:<uuid>:status
		parts := strings.Split(key, ":")
		if len(parts) != 4 {
			continue
		}
		uuid := parts[2]

		newStatus, err := s.redis.Get(ctx, key).Result()
		if err != nil {
			continue
		}

		// Find server by UUID and update status
		srv, err := s.store.GetServerByUUID(uuid)
		if err != nil {
			continue
		}

		if srv.Status != newStatus {
			log.Printf("Status update for %s: %s -> %s", uuid, srv.Status, newStatus)
			s.store.UpdateServerStatus(srv.ID, newStatus)
		}

		// Delete key after processing
		s.redis.Del(ctx, key)
	}

	// Publish desired states to Redis so nodes can reconcile
	s.publishDesiredStates(ctx)
}

// publishDesiredStates syncs desired_state from DB to Redis for node reconciliation.
func (s *StatusWatcherService) publishDesiredStates(ctx context.Context) {
	servers, err := s.store.ListServers("")
	if err != nil {
		return
	}

	pipe := s.redis.Pipeline()
	for _, srv := range servers {
		key := fmt.Sprintf("dylaris:server:%s:desired_state", srv.UUID)
		pipe.Set(ctx, key, srv.DesiredState, 60*time.Second)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		log.Printf("Failed to publish desired states: %v", err)
	}
}
