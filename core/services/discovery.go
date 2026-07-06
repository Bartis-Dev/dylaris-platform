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
	// flags gates BYON-only behavior (max_nodes adoption cap). nil = no gating.
	flags *FeatureFlags
}

// SetLeader wires the leader-election gate. Call once at boot.
func (s *DiscoveryService) SetLeader(l leader.Election) { s.leader = l }

// SetFeatureFlags wires the feature-flag gate. Call once at boot.
func (s *DiscoveryService) SetFeatureFlags(f *FeatureFlags) { s.flags = f }

// nodeLimitReached reports whether adopting one more node would exceed the
// tenant's effective max_nodes cap. Only enforced when BYON is enabled; a 0 cap
// (no plan/override) means unlimited. Fail-open on store errors so a transient
// glitch never silently strands a node unadopted.
func (s *DiscoveryService) nodeLimitReached(uid string) bool {
	if s.flags == nil || !s.flags.IsBYONEnabled(context.Background()) {
		return false
	}
	lim, err := EffectiveLimits(s.store, uid)
	if err != nil || lim.MaxNodes <= 0 {
		return false
	}
	cnt, err := s.store.CountNodesByOwner(uid)
	if err != nil {
		return false
	}
	return int64(cnt) >= lim.MaxNodes
}

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

		// 2. Security Check. OFF path: raw-secret compare, byte-identical. ON path
		// (feature_redis_acl): the raw secret is absent; the per-node signature is
		// verified in the existing-node branch below (a new node is created only via
		// gRPC enroll, never from a heartbeat).
		featureOn := s.flags != nil && s.flags.IsRedisACLEnabled(ctx)
		if !featureOn {
			if hb.ClusterSecret != s.clusterSecret {
				log.Printf("Unauthorized Node detected: %s (Wrong Secret)", hb.Name)
				continue
			}
		}

		// 3. Find or create Node in DB
		activeNodeTokens[hb.ID] = true

		node, err := s.store.GetNodeByToken(hb.ID)

		if err != nil {
			// Node does not exist -> Create new!

			// Server-assigned identity: on the hardened path, gRPC enroll (gated by
			// a single-use enroll token) is the ONLY node-creation path. A heartbeat
			// for an id the Core never assigned must NOT auto-register a node, or the
			// Core-minted identity would be trivially bypassable. Liveness updates for
			// already-enrolled nodes (the else branch) are unaffected. nil flags =
			// no gating = OFF path = create as before (byte-identical).
			if s.flags != nil && s.flags.IsRedisACLEnabled(ctx) {
				continue
			}

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
			} else if hb.EnrollToken != "" {
				// BYON: bind a still-unowned node to the enroll token's user, then
				// consume the token (single-use). Once the node is owned, skip.
				if created, gerr := s.store.GetNodeByToken(hb.ID); gerr == nil && created.OwnerID == nil {
					if uid, ok, terr := s.store.ResolveNodeEnrollToken(hb.EnrollToken); terr == nil && ok {
						if s.nodeLimitReached(uid) {
							log.Printf("Node %s NOT adopted: user %s is at their node limit", hb.Name, uid)
						} else if cuid, _, cok, cerr := s.store.ConsumeNodeEnrollToken(hb.EnrollToken); cerr == nil && cok {
							if serr := s.store.SetNodeOwner(created.ID, &cuid); serr != nil {
								log.Printf("node %s: bind owner failed: %v", hb.Name, serr)
							} else {
								log.Printf("Node %s enrolled to user %s (BYON)", hb.Name, cuid)
							}
						}
					}
				}
			}
			// Gateway link auto-creation is handled by Hub's link discovery loop
			// (hub:link:discovery:{nodeID} heartbeat from the Link binary)
		} else {
			if featureOn {
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
