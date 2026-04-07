package services

import (
	"dylaris-core/store"
	"log"
	"time"
)

type NodeCleanupService struct {
	store store.Store
	ttl   time.Duration
}

func NewNodeCleanupService(s store.Store, ttl time.Duration) *NodeCleanupService {
	return &NodeCleanupService{store: s, ttl: ttl}
}

func (s *NodeCleanupService) Start() {
	log.Printf("Node Cleanup Service started (TTL: %s)", s.ttl)
	ticker := time.NewTicker(5 * time.Minute)
	go func() {
		for range ticker.C {
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
