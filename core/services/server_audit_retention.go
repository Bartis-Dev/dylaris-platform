package services

import (
	"context"
	"dylaris-core/pkg/leader"
	"dylaris-core/store"
	"fmt"
	"log"
	"time"
)

// ServerAuditRetentionService runs a daily sweep that deletes server_audit_events
// older than the configured retention. Honors the audit.server_retention_days
// setting; a value of 0 means "keep forever" and skips the sweep.
//
// Leader-gated like all other periodic services so multi-Core doesn't fan out
// the DELETE.
type ServerAuditRetentionService struct {
	store    store.Store
	interval time.Duration
	leader   leader.Election
}

func NewServerAuditRetentionService(s store.Store) *ServerAuditRetentionService {
	return &ServerAuditRetentionService{store: s, interval: 24 * time.Hour}
}

func (s *ServerAuditRetentionService) SetLeader(l leader.Election) { s.leader = l }
func (s *ServerAuditRetentionService) SetInterval(d time.Duration) { s.interval = d }

func (s *ServerAuditRetentionService) Start(ctx context.Context) {
	log.Printf("Server audit retention service started (interval: %s)", s.interval)
	s.runOnce(ctx)
	ticker := time.NewTicker(s.interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.runOnce(ctx)
			}
		}
	}()
}

func (s *ServerAuditRetentionService) runOnce(_ context.Context) {
	if s.leader != nil && !s.leader.IsLeader() {
		return
	}
	daysStr, _ := s.store.GetSetting("audit.server_retention_days")
	var days int
	fmt.Sscanf(daysStr, "%d", &days)
	if days <= 0 {
		// 0 = unlimited retention; nothing to do.
		return
	}
	cutoff := time.Now().Add(-time.Duration(days) * 24 * time.Hour)
	deleted, err := s.store.PurgeServerAuditOlderThan(cutoff)
	if err != nil {
		log.Printf("server-audit-retention: purge error: %v", err)
		return
	}
	if deleted > 0 {
		log.Printf("server-audit-retention: purged %d events older than %d days", deleted, days)
	}
}
