package services

import (
	"context"
	"dylaris-core/store"
	"encoding/json"
	"log"
	"time"

	"github.com/redis/go-redis/v9"
)

// SFTPSyncService publishes SFTP auth data and per-node server lists to Redis.
//
// Keys written:
//
//	sftp:auth:{username}                     = bcrypt_hash (no TTL)
//	sftp:node:{nodeName}:user:{username}     = JSON [{uuid,name}] (TTL 5min)
type SFTPSyncService struct {
	store store.Store
	redis *redis.Client
}

func NewSFTPSyncService(s store.Store, r *redis.Client) *SFTPSyncService {
	return &SFTPSyncService{store: s, redis: r}
}

func (s *SFTPSyncService) Start() {
	log.Println("SFTP Sync Service started")
	s.sync()
	go func() {
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			s.sync()
		}
	}()
}

type sftpServerEntry struct {
	UUID string `json:"uuid"`
	Name string `json:"name"`
}

// pruneStaleAuthKeys removes any sftp:auth:* key whose user is no longer in
// the valid set. SCAN keeps it O(batch) instead of blocking Redis with KEYS.
func (s *SFTPSyncService) pruneStaleAuthKeys(ctx context.Context, valid map[string]bool) {
	var cursor uint64
	for {
		keys, next, err := s.redis.Scan(ctx, cursor, "sftp:auth:*", 100).Result()
		if err != nil {
			return
		}
		for _, k := range keys {
			if !valid[k] {
				s.redis.Del(ctx, k)
			}
		}
		cursor = next
		if cursor == 0 {
			return
		}
	}
}

func (s *SFTPSyncService) sync() {
	ctx := context.Background()

	// 1. Publish auth hashes for all users. Refreshed every tick (60s) with a
	// 5min TTL so the hashes self-expire if this sync ever stops, bounding the
	// exposure window if Redis read access leaks.
	users, err := s.store.ListUsers()
	if err != nil {
		log.Printf("SFTPSync: failed to list users: %v", err)
		return
	}
	pipe := s.redis.Pipeline()
	valid := make(map[string]bool, len(users))
	for _, u := range users {
		if u.Password != "" {
			key := "sftp:auth:" + u.Username
			pipe.Set(ctx, key, u.Password, 5*time.Minute)
			valid[key] = true
		}
	}
	if _, err := pipe.Exec(ctx); err != nil {
		log.Printf("SFTPSync: failed to write auth keys: %v", err)
	}
	// Drop auth keys for users that no longer exist (deleted or renamed); the
	// keys carry no TTL, so without this stale credentials would authenticate
	// over SFTP forever.
	s.pruneStaleAuthKeys(ctx, valid)

	// 2. Publish per-node, per-user server lists
	nodes, err := s.store.ListNodes()
	if err != nil {
		log.Printf("SFTPSync: failed to list nodes: %v", err)
		return
	}

	for _, node := range nodes {
		accesses, err := s.store.GetSFTPAccessByNode(node.ID)
		if err != nil {
			continue
		}

		// Group by username
		byUser := make(map[string][]sftpServerEntry)
		for _, a := range accesses {
			byUser[a.Username] = append(byUser[a.Username], sftpServerEntry{UUID: a.ServerUUID, Name: a.ServerName})
		}

		pipe := s.redis.Pipeline()
		for username, servers := range byUser {
			data, err := json.Marshal(servers)
			if err != nil {
				continue
			}
			key := "sftp:node:" + node.Name + ":user:" + username
			pipe.Set(ctx, key, data, 5*time.Minute)
		}
		if _, err := pipe.Exec(ctx); err != nil {
			log.Printf("SFTPSync: failed to write node %s keys: %v", node.Name, err)
		}
	}
}
