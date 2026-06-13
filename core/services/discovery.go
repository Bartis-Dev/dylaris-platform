package services

import (
	"context"
	"dylaris-core/models"
	"dylaris-core/pkg/leader"
	"dylaris-core/store"
	"encoding/json"
	"log"
	"time"

	"github.com/redis/go-redis/v9"
)

type DiscoveryService struct {
	store         store.Store
	redis         *redis.Client
	clusterSecret string
	// leader is set by main.go after construction. nil = run unconditionally
	// (single-Core dev mode); non-nil = only run when this Core holds the
	// global lease. See pkg/leader.
	leader leader.Election
}

// SetLeader wires the leader-election gate. Call once at boot.
func (s *DiscoveryService) SetLeader(l leader.Election) { s.leader = l }

// Payload that the Node writes to Redis
type NodeHeartbeat struct {
	ID            string                 `json:"id"`            // Unique ID (e.g. hostname)
	Name          string                 `json:"name"`          // Display name
	IP            string                 `json:"ip"`            // IP for display
	ClusterSecret string                 `json:"clusterSecret"` // For validation
	Tags          string                 `json:"tags"`
	Region        string                 `json:"region"` // DYLARIS_REGION env, e.g. "eu-central"
	IPs           NodeIPs                `json:"ips"`
	CPUUsage      float64                `json:"cpuUsage"`
	RAMFree       int64                  `json:"ramFree"`
	RAMTotal      uint64                 `json:"ramTotal"`
	TotalCPU      float64                `json:"totalCpu"`
	Storage       []HeartbeatStoragePath `json:"storage"`
}

// HeartbeatStoragePath is one storage path reported by the node.
type HeartbeatStoragePath struct {
	Path        string `json:"path"`
	TotalBytes  int64  `json:"total_bytes"`
	FreeBytes   int64  `json:"free_bytes"`
	UsedBytes   int64  `json:"used_bytes"`
	ServerCount int    `json:"server_count"`
}

// LoadHeartbeats reads every node heartbeat currently in Redis and
// returns a map keyed by node token (= heartbeat ID). Used by callers
// that need to inspect more than one node at a time (scheduler, infra
// overview, capacity sync).
func LoadHeartbeats(ctx context.Context, r *redis.Client) map[string]*NodeHeartbeat {
	out := map[string]*NodeHeartbeat{}
	if r == nil {
		return out
	}
	keys, err := r.Keys(ctx, "dylaris:discovery:*").Result()
	if err != nil {
		return out
	}
	for _, key := range keys {
		val, err := r.Get(ctx, key).Result()
		if err != nil {
			continue
		}
		var hb NodeHeartbeat
		if err := json.Unmarshal([]byte(val), &hb); err != nil {
			continue
		}
		out[hb.ID] = &hb
	}
	return out
}

type NodeIPs struct {
	Public  string   `json:"public"`
	Private []string `json:"private"`
}

func NewDiscoveryService(s store.Store, r *redis.Client, secret string) *DiscoveryService {
	return &DiscoveryService{
		store:         s,
		redis:         r,
		clusterSecret: secret,
	}
}

func (s *DiscoveryService) Start() {
	log.Println("Discovery Service started (Auto-Discovery active)")

	ticker := time.NewTicker(5 * time.Second)
	go func() {
		for range ticker.C {
			// Leader-gate: only the elected Core scans + writes node rows.
			// Followers idle through each tick. nil leader = run anyway
			// (covers tests + single-instance dev where Redis-leader isn't wired).
			if s.leader != nil && !s.leader.IsLeader() {
				continue
			}
			s.scanNodes()
		}
	}()
}

func (s *DiscoveryService) scanNodes() {
	ctx := context.Background()

	// 1. Find all Discovery Keys: dylaris:discovery:*
	keys, err := s.redis.Keys(ctx, "dylaris:discovery:*").Result()
	if err != nil {
		log.Printf("Discovery Redis Error: %v", err)
		return
	}

	activeNodeTokens := make(map[string]bool)

	for _, key := range keys {
		val, err := s.redis.Get(ctx, key).Result()
		if err != nil {
			continue
		}

		var hb NodeHeartbeat
		if err := json.Unmarshal([]byte(val), &hb); err != nil {
			continue
		}

		// 2. Security Check
		if hb.ClusterSecret != s.clusterSecret {
			log.Printf("Unauthorized Node detected: %s (Wrong Secret)", hb.Name)
			continue
		}

		// 3. Find or create Node in DB
		activeNodeTokens[hb.ID] = true

		node, err := s.store.GetNodeByToken(hb.ID)

		if err != nil {
			// Node does not exist -> Create new!

			// Reject duplicate node names
			if existing, nameErr := s.store.GetNodeByName(hb.Name); nameErr == nil && existing.Token != hb.ID {
				log.Printf("Rejected Node '%s': name already in use by node token '%s'", hb.Name, existing.Token)
				continue
			}

			log.Printf("New Node Discovered: %s (%s)", hb.Name, hb.IP)

			tags := hb.Tags
			if tags == "" {
				tags = "auto-discovered"
			}

			address := hb.IP
			if address == "" || address == "auto" {
				address = "127.0.0.1"
			}

			newNode := &models.Node{
				Name:          hb.Name,
				Address:       address,
				Token:         hb.ID,
				Status:        "online",
				IsLocal:       false,
				Tags:          tags,
				Region:        hb.Region,
				LinkEnabled:   true,
				LinkInstances: 1,
			}
			if createErr := s.store.CreateNode(newNode); createErr != nil {
				log.Printf("Failed to create node '%s': %v", hb.Name, createErr)
			}
			// Gateway link auto-creation is handled by Hub's link discovery loop
			// (hub:link:discovery:{nodeID} heartbeat from the Link binary)
		} else {
			// Node exists -> Status Update + refresh last_seen_at
			s.store.SetNodeLastSeen(node.ID)
			if node.Status != "online" {
				log.Printf("Node is back online: %s", node.Name)
				s.store.SetNodeStatus(node.ID, "online")
			}

			if hb.Name != "" && node.Name != hb.Name {
				log.Printf("Node Name updated: %s → %s", node.Name, hb.Name)
				s.store.SetNodeName(node.ID, hb.Name)
			}

			if node.Tags != hb.Tags && hb.Tags != "" {
				log.Printf("Node Tags updated for %s: %s", node.Name, hb.Tags)
				s.store.SetNodeTags(node.ID, hb.Tags)
			}

			if hb.IP != "auto" && hb.IP != "" && node.Address != hb.IP {
				log.Printf("Node IP updated for %s: %s -> %s", node.Name, node.Address, hb.IP)
				s.store.SetNodeAddress(node.ID, hb.IP)
			}

			if hb.IPs.Public != "" || len(hb.IPs.Private) > 0 {
				if hb.IPs.Public != node.PublicIP || !slicesEqual(hb.IPs.Private, node.PrivateIPs) {
					s.store.SetNodeIPs(node.ID, hb.IPs.Public, hb.IPs.Private)
				}
			}

			// Cache physical capacity for the scheduler. Older nodes may
			// not yet report total_cpu — we still update RAM in that case.
			totalRAMMB := int64(hb.RAMTotal / (1024 * 1024))
			if totalRAMMB != node.TotalRAMMB || (hb.TotalCPU > 0 && hb.TotalCPU != node.TotalCPU) {
				cpu := hb.TotalCPU
				if cpu == 0 {
					cpu = node.TotalCPU // keep last known
				}
				s.store.UpdateNodeCapacity(node.ID, cpu, totalRAMMB)
			}

			// Region — only update when the heartbeat actually carries one.
			// Nodes that don't broadcast DYLARIS_REGION keep whatever the admin set.
			if hb.Region != "" && hb.Region != node.Region {
				log.Printf("Node region updated for %s: %q → %q", node.Name, node.Region, hb.Region)
				s.store.SetNodeRegion(node.ID, hb.Region)
			}
		}
	}

	// 4. Offline Check
	dbNodes, _ := s.store.ListNodes()
	for _, dbNode := range dbNodes {
		if _, isActive := activeNodeTokens[dbNode.Token]; !isActive {
			if dbNode.Status == "online" {
				log.Printf("Node went offline: %s", dbNode.Name)
				s.store.SetNodeStatus(dbNode.ID, "offline")
			}
		}
	}
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
