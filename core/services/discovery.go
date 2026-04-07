package services

import (
	"context"
	"crypto/sha256"
	"dylaris-core/models"
	"dylaris-core/store"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"dylaris-hub/pkg/hub"

	"github.com/redis/go-redis/v9"
)

type DiscoveryService struct {
	store         store.Store
	redis         *redis.Client
	clusterSecret string
	hubSvc        *hub.HubService // optional: for gateway link auto-creation
}

// Payload that the Node writes to Redis
type NodeHeartbeat struct {
	ID            string  `json:"id"`            // Unique ID (e.g. hostname)
	Name          string  `json:"name"`          // Display name
	IP            string  `json:"ip"`            // IP for display
	ClusterSecret string  `json:"clusterSecret"` // For validation
	Tags          string  `json:"tags"`
	IPs           NodeIPs `json:"ips"`
	CPUUsage      float64 `json:"cpuUsage"`
	RAMFree       int64   `json:"ramFree"`
	RAMTotal      uint64  `json:"ramTotal"`
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

// SetHubService sets the Hub service reference for gateway link auto-creation.
// Called after HubBridge is initialized.
func (s *DiscoveryService) SetHubService(hubSvc *hub.HubService) {
	s.hubSvc = hubSvc
}

func (s *DiscoveryService) Start() {
	log.Println("Discovery Service started (Auto-Discovery active)")

	// Loop: Check every 5 seconds
	ticker := time.NewTicker(5 * time.Second)
	go func() {
		for range ticker.C {
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
				LinkEnabled:   true,
				LinkInstances: 1,
			}
			if createErr := s.store.CreateNode(newNode); createErr != nil {
				log.Printf("Failed to create node '%s': %v", hb.Name, createErr)
				continue
			}

			// Auto-create gateway link for this node
			s.ensureGatewayLink(newNode)
		} else {
			// Node exists -> Status Update + refresh last_seen_at
			s.store.SetNodeLastSeen(node.ID)
			if node.Status != "online" {
				log.Printf("Node is back online: %s", node.Name)
				s.store.SetNodeStatus(node.ID, "online")
				s.ensureGatewayLink(node)
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

// DeriveLinkToken computes a deterministic link token from nodeID + clusterSecret.
// This must match the same derivation in the Link and Node binaries.
func DeriveLinkToken(nodeID, clusterSecret string) string {
	h := sha256.New()
	h.Write([]byte(nodeID + clusterSecret))
	return hex.EncodeToString(h.Sum(nil))
}

// ensureGatewayLink creates or updates a gateway link for a node via Hub's GORM DB.
func (s *DiscoveryService) ensureGatewayLink(node *models.Node) {
	if !node.LinkEnabled || s.hubSvc == nil {
		return
	}
	token := DeriveLinkToken(node.Token, s.clusterSecret)
	nodeIDStr := fmt.Sprint(node.ID)
	wantedName := fmt.Sprintf("Node: %s", node.Name)

	// Try to find existing link by node_id first
	existing, err := s.hubSvc.GetLinkByNodeID(nodeIDStr)
	if err != nil {
		// Not found by node_id — check if a link with this token already exists
		// (links created before node_id tracking was added will be found here)
		var byToken hub.Link
		if tokenErr := s.hubSvc.DB.Where("token = ?", token).First(&byToken).Error; tokenErr == nil {
			// Link exists but lacks node_id — backfill it
			updates := map[string]interface{}{"node_id": nodeIDStr, "name": wantedName}
			s.hubSvc.DB.Model(&byToken).Updates(updates)
			log.Printf("Backfilled node_id for gateway_link of node '%s'", node.Name)
			return
		}
		// No link at all — create one
		link := hub.Link{
			Name:     wantedName,
			Token:    token,
			Enabled:  true,
			IsSystem: true,
			NodeID:   nodeIDStr,
		}
		if createErr := s.hubSvc.DB.Create(&link).Error; createErr != nil {
			log.Printf("Failed to auto-create gateway_link for node '%s': %v", node.Name, createErr)
		} else {
			log.Printf("Auto-created gateway_link for node '%s' (token: %s...)", node.Name, token[:8])
		}
	} else {
		// Update token/name if changed (e.g. cluster secret rotation or node rename)
		updates := map[string]interface{}{}
		if existing.Token != token {
			updates["token"] = token
		}
		if existing.Name != wantedName {
			updates["name"] = wantedName
		}
		if len(updates) > 0 {
			s.hubSvc.DB.Model(existing).Updates(updates)
			log.Printf("Updated gateway_link for node '%s'", node.Name)
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
