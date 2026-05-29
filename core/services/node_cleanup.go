package services

import (
	"dylaris-core/pkg/leader"
	"dylaris-core/store"
	"log"
	"time"
)

type NodeCleanupService struct {
	store  store.Store
	ttl    time.Duration
	leader leader.Election
}

func NewNodeCleanupService(s store.Store, ttl time.Duration) *NodeCleanupService {
	return &NodeCleanupService{store: s, ttl: ttl}
}

// SetLeader wires the leader-election gate so only one Core deletes per tick.
func (s *NodeCleanupService) SetLeader(l leader.Election) { s.leader = l }

func (s *NodeCleanupService) Start() {
	log.Printf("Node Cleanup Service started (TTL: %s)", s.ttl)
	ticker := time.NewTicker(5 * time.Minute)
	go func() {
		for range ticker.C {
			if s.leader != nil && !s.leader.IsLeader() {
				continue
			}
			cutoff := time.Now().Add(-s.ttl)
			count, err := s.store.DeleteStaleOfflineNodes(cutoff)
			if err != nil {
				log.Printf("Node cleanup error: %v", err)
				continue
			}
			if count > 0 {
				log.Printf("Cleaned up %d stale offline node(s)", count)
			}
		}
	}()
}
