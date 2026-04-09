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

func (s *SFTPSyncService) sync() {
	ctx := context.Background()

	// 1. Publish auth hashes for all users (no TTL — hash only changes on password change)
	users, err := s.store.ListUsers()
	if err != nil {
		log.Printf("SFTPSync: failed to list users: %v", err)
		return
	}
	pipe := s.redis.Pipeline()
	for _, u := range users {
		if u.Password != "" {
			pipe.Set(ctx, "sftp:auth:"+u.Username, u.Password, 0)
		}
	}
	if _, err := pipe.Exec(ctx); err != nil {
		log.Printf("SFTPSync: failed to write auth keys: %v", err)
	}

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
