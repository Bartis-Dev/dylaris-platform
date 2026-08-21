package services

import (
	"context"
	"dylaris-core/models"
	"dylaris-core/store"
	"encoding/json"
	"log"
	"time"

	"github.com/redis/go-redis/v9"
)

// SFTPSyncService publishes SFTP auth data and per-node server lists to Redis.
//
// Keys written (both refreshed every 60s, both with a 5min TTL):
//
//	sftp:auth:{username}                      = bcrypt hash of the panel password
//	sftp:node:{nodeToken}:user:{username}     = JSON [{uuid,name}]
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

// sftpNodeServersKey is the per-node, per-user server list the node's SFTP
// server reads to resolve which server a virtual path targets.
//
// Keyed by the node's TOKEN, never its NAME. Both start out equal - enrollment
// sets nodes.name and nodes.token to the same Core-minted identity - but only
// the token is stable. The panel's node-adoption form has a "Node Name" field
// next to its "Display Name" one (PATCH /nodes/{id}/config -> SetNodeConfig),
// so an admin typing a friendly name there renames the row.
//
// Keying by the name made that rename break SFTP on the node, silently and in
// two ways at once. The node reads this key under the identity Core ASSIGNED it
// (redisacl_bootstrap.go adopts res.AssignedId as nodeID), which is the token;
// and its Redis ACL grants exactly "%R~sftp:node:<token>:*", so even a node that
// somehow knew the new name would get NOPERM on it. The session still
// authenticates - sftp:auth:* is keyed by username and unaffected - so the user
// logs in successfully and sees an EMPTY root, with nothing in any log to say
// why. The token is what every other node-scoped key in the system already uses.
//
// Takes the whole node rather than a string so the choice of field lives here,
// where the reasoning is, instead of at a call site that can pass either one.
func sftpNodeServersKey(node models.Node, username string) string {
	return "sftp:node:" + node.Token + ":user:" + username
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
	// Drop auth keys for users that no longer exist (deleted or renamed). The
	// TTL above already bounds this at 5 minutes; the prune is what closes the
	// gap between a deletion in the panel and the moment those credentials stop
	// opening an SFTP session. (An earlier version of this comment claimed the
	// keys carried no TTL and that the prune was the only thing standing between
	// a deleted user and permanent SFTP access - it is not, and reading it that
	// way makes the 5-minute window look like a bug rather than the floor.)
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
			pipe.Set(ctx, sftpNodeServersKey(node, username), data, 5*time.Minute)
		}
		if _, err := pipe.Exec(ctx); err != nil {
			log.Printf("SFTPSync: failed to write node %s keys: %v", node.Name, err)
		}
	}
}
