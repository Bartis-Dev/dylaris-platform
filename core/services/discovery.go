package services

import (
	"context"
	"dylaris-core/models"
	"dylaris-core/pkg/leader"
	"dylaris-core/services/redisacl"
	"dylaris-core/store"
	"encoding/json"
	"fmt"
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
	// PortRange is the node's effective MC host-port range ("25600-25699") and
	// PortRangeNotice is set only when the node fell back to its default because
	// PORT_RANGE was unset or unparseable. Surfaced so an admin sees the ports
	// the host firewall must open, and sees a typo instead of a silent default.
	PortRange       string `json:"portRange,omitempty"`
	PortRangeNotice string `json:"portRangeNotice,omitempty"`
	// SharedStorage is non-empty when the node found one of its storage paths
	// mounted into another node too. That topology cannot work - node identity
	// lives in the first storage path - and it silently destroys a server on the
	// next migration, so it is carried all the way to the panel.
	SharedStorage []models.SharedStorageConflict `json:"sharedStorage,omitempty"`
	// EnrollToken is the per-user BYON enroll token (NODE_ENROLL_TOKEN). When a
	// NEW node presents a valid one, it is bound to that user (owner_id). Empty
	// for platform nodes.
	EnrollToken string `json:"enrollToken,omitempty"`
	// Timestamp (unix seconds) + Sig replace the raw-secret compare on the
	// hardened path: Sig = HMAC(perNodeSecret, heartbeat-domain|token|ts).
	Timestamp int64  `json:"timestamp"`
	Sig       string `json:"sig,omitempty"`
}

// HeartbeatStoragePath is one storage path reported by the node.
type HeartbeatStoragePath struct {
	Path        string `json:"path"`
	TotalBytes  int64  `json:"total_bytes"`
	FreeBytes   int64  `json:"free_bytes"`
	UsedBytes   int64  `json:"used_bytes"`
	ServerCount int    `json:"server_count"`
	// ServerUUIDs are the top-level directories the node found on this path.
	// It is what lets Core attribute each server's PROMISED disk limit to the
	// path it actually lives on - free space alone cannot show commitment.
	ServerUUIDs []string `json:"server_uuids"`
	// QuotaEnforceable reports whether a per-server disk limit can actually be
	// enforced here (project quotas need xfs/ext4). Surfaced so a limit that
	// silently does nothing on NFS/CIFS is visible instead of assumed.
	QuotaEnforceable bool `json:"quota_enforceable"`
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

// LoadHeartbeat reads ONE node's heartbeat by token. Prefer it over
// LoadHeartbeats when a single node is wanted: the plural form does a KEYS scan
// over the whole fleet.
func LoadHeartbeat(ctx context.Context, r *redis.Client, nodeToken string) *NodeHeartbeat {
	if r == nil || nodeToken == "" {
		return nil
	}
	val, err := r.Get(ctx, "dylaris:discovery:"+nodeToken).Result()
	if err != nil || val == "" {
		return nil
	}
	var hb NodeHeartbeat
	if json.Unmarshal([]byte(val), &hb) != nil {
		return nil
	}
	return &hb
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

// checkCPUTopologyChange compares the node's currently reported CPU topology
// against the last-seen signature (kept in Redis). On a real hardware change it
// resets every CPU pinning on the node (server cpusets + node cpuset). The first
// observation just records the signature (no reset). Runs only on the leader
// because scanNodes is leader-gated.
func (s *DiscoveryService) checkCPUTopologyChange(ctx context.Context, node *models.Node) {
	raw, err := s.redis.Get(ctx, fmt.Sprintf("dylaris:node:%s:cpu", node.Token)).Result()
	if err != nil {
		return // node has not reported a topology
	}
	var topo CPUTopology
	if err := json.Unmarshal([]byte(raw), &topo); err != nil {
		return
	}
	sig := TopologySignature(&topo)
	if sig == "" {
		return
	}
	sigKey := fmt.Sprintf("dylaris:node:%s:cpu:sig", node.Token)
	prev, _ := s.redis.Get(ctx, sigKey).Result()
	if prev == sig {
		return
	}
	if prev != "" {
		resetN, rerr := s.store.ResetServerCPUPinningByNode(node.ID)
		if rerr != nil {
			log.Printf("CPU topology changed on %s but pinning reset failed: %v", node.Name, rerr)
			return // keep the old signature so the reset is retried next scan
		}
		if cerr := s.store.UpdateNodeCpusetCpus(node.ID, ""); cerr != nil {
			log.Printf("CPU topology changed on %s: node cpuset reset failed: %v", node.Name, cerr)
		}
		log.Printf("Node %s CPU topology changed -> reset %d server pinning(s) + node cpuset", node.Name, resetN)
	}
	s.redis.Set(ctx, sigKey, sig, 0)
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

		// 2. Security Check. Redis ACL is mandatory: the raw cluster secret is never
		// on the wire. A new node is created only via the gRPC enroll path (never from
		// a heartbeat), and an existing node's per-node HMAC signature is verified in
		// the existing-node branch below.

		// 3. Find or create Node in DB
		activeNodeTokens[hb.ID] = true

		node, err := s.store.GetNodeByToken(hb.ID)

		if err != nil {
			// Node does not exist -> Create new!

			// Server-assigned identity: gRPC enroll (gated by a single-use enroll
			// token) is the ONLY node-creation path. A heartbeat for an id the Core
			// never assigned must NOT auto-register a node, or the Core-minted identity
			// would be trivially bypassable. Liveness updates for already-enrolled
			// nodes (the else branch) are unaffected.
			continue
		} else {
			now := time.Now().Unix()
			secret, ok, lerr := redisacl.LoadNodeSecret(s.store, s.clusterSecret, node.ID)
			if lerr != nil || !ok || hb.Sig == "" ||
				hb.Timestamp < now-30 || hb.Timestamp > now+30 ||
				!redisacl.VerifyHeartbeatSig(secret, node.Token, hb.Timestamp, hb.Sig) {
				log.Printf("Heartbeat rejected for %s: bad or stale signature", node.Name)
				// Undo the active-mark from step above so a rejected heartbeat
				// cannot keep a node online.
				delete(activeNodeTokens, hb.ID)
				continue
			}
			// Node exists -> Status Update + refresh last_seen_at
			s.store.SetNodeLastSeen(node.ID)
			if node.Status != "online" {
				log.Printf("Node is back online: %s", node.Name)
				s.store.SetNodeStatus(node.ID, "online")
			}

			// Name + tags are config fields: only let the heartbeat env drive them
			// while the node hasn't been adopted by an admin. Once configured=true
			// the panel-set values win and the env is ignored (DB precedence).
			if !node.Configured {
				if hb.Name != "" && node.Name != hb.Name {
					log.Printf("Node Name updated: %s → %s", node.Name, hb.Name)
					s.store.SetNodeName(node.ID, hb.Name)
				}

				if node.Tags != hb.Tags && hb.Tags != "" {
					log.Printf("Node Tags updated for %s: %s", node.Name, hb.Tags)
					s.store.SetNodeTags(node.ID, hb.Tags)
				}
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

			// Region — only update when the heartbeat carries one AND the node has
			// not been adopted by an admin. Configured nodes (and nodes that don't
			// broadcast DYLARIS_REGION) keep whatever the admin set.
			if !node.Configured && hb.Region != "" && hb.Region != node.Region {
				log.Printf("Node region updated for %s: %q → %q", node.Name, node.Region, hb.Region)
				s.store.SetNodeRegion(node.ID, hb.Region)
			}

			// Hardware-change guard: if the node's host CPU layout changed since we
			// last saw it (e.g. a CPU swap, detected on the node's restart re-scan),
			// reset every CPU pinning on this node so no server / node cpuset
			// references cores that no longer exist.
			s.checkCPUTopologyChange(ctx, node)
		}
	}

	// 4. Offline Check
	//
	// A fleet that could not be read is not an empty fleet. Skipping the sweep
	// is already the safe outcome (nothing gets marked offline on a bad read),
	// but it was indistinguishable from a healthy round in which every node
	// answered - so an operator watching nodes flip had no way to tell the two
	// apart. Say which one happened.
	dbNodes, err := s.store.ListNodes()
	if err != nil {
		log.Printf("Discovery: offline check skipped, could not list nodes: %v", err)
		return
	}
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
