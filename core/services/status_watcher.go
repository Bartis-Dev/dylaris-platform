package services

import (
	"context"
	"dylaris-core/pkg/leader"
	"dylaris-core/store"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

type StatusWatcherService struct {
	store  store.Store
	redis  *redis.Client
	leader leader.Election
}

func NewStatusWatcherService(s store.Store, r *redis.Client) *StatusWatcherService {
	return &StatusWatcherService{store: s, redis: r}
}

// SetLeader wires the leader-election gate. Status scan + DB writes only
// run on the elected Core; followers idle through each tick.
func (s *StatusWatcherService) SetLeader(l leader.Election) { s.leader = l }

func (s *StatusWatcherService) Start() {
	log.Println("Status Watcher Service started")
	ticker := time.NewTicker(5 * time.Second)
	go func() {
		for range ticker.C {
			if s.leader != nil && !s.leader.IsLeader() {
				continue
			}
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

	// Sync host ports: Redis → DB
	s.syncPortsFromRedis(ctx)
}

// syncPortsFromRedis reads port allocations written by Nodes and updates DB host_port.
// Key format: dylaris:node:{nodeID}:port:{serverUUID} → port number
func (s *StatusWatcherService) syncPortsFromRedis(ctx context.Context) {
	var cursor uint64
	for {
		keys, next, err := s.redis.Scan(ctx, cursor, "dylaris:node:*:port:*", 100).Result()
		if err != nil {
			return
		}
		for _, key := range keys {
			// Parse: dylaris:node:{nodeID}:port:{uuid}
			parts := strings.SplitN(key, ":port:", 2)
			if len(parts) != 2 {
				continue
			}
			serverUUID := parts[1]

			portStr, err := s.redis.Get(ctx, key).Result()
			if err != nil {
				continue
			}
			redisPort, err := strconv.Atoi(portStr)
			if err != nil || redisPort <= 0 {
				continue
			}

			srv, err := s.store.GetServerByUUID(serverUUID)
			if err != nil {
				continue
			}

			if srv.HostPort != redisPort {
				containerPort := srv.ContainerPort
				if containerPort == 0 {
					containerPort = 25565
				}
				s.store.UpdateServerPorts(srv.ID, redisPort, containerPort)
			}
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
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
