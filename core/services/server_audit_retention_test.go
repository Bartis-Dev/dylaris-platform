package services

import (
	"context"
	"errors"
	"testing"
	"time"

	"dylaris-core/store"
)

// auditRetentionFakeStore embeds store.Store (nil) so it satisfies the full
// interface at compile time; only the methods ServerAuditRetentionService
// touches are overridden.
type auditRetentionFakeStore struct {
	store.Store

	settings map[string]string

	purgeCutoffSeen time.Time
	purgeCalled     bool
	purgeCount      int
	purgeErr        error
}

func (f *auditRetentionFakeStore) GetSetting(key string) (string, error) {
	v, ok := f.settings[key]
	if !ok {
		return "", errors.New("not found")
	}
	return v, nil
}

func (f *auditRetentionFakeStore) PurgeServerAuditOlderThan(cutoff time.Time) (int, error) {
	f.purgeCalled = true
	f.purgeCutoffSeen = cutoff
	return f.purgeCount, f.purgeErr
}

func TestServerAuditRetention_ZeroDays_SkipsSweep(t *testing.T) {
	fs := &auditRetentionFakeStore{settings: map[string]string{"audit.server_retention_days": "0"}}
	svc := NewServerAuditRetentionService(fs)

	svc.runOnce(context.Background())

	if fs.purgeCalled {
		t.Fatal("expected no purge when retention is 0 (unlimited)")
	}
}

func TestServerAuditRetention_UnsetDays_SkipsSweep(t *testing.T) {
	fs := &auditRetentionFakeStore{settings: map[string]string{}}
	svc := NewServerAuditRetentionService(fs)

	svc.runOnce(context.Background())

	if fs.purgeCalled {
		t.Fatal("expected no purge when the setting is unset")
	}
}

func TestServerAuditRetention_NegativeDays_SkipsSweep(t *testing.T) {
	fs := &auditRetentionFakeStore{settings: map[string]string{"audit.server_retention_days": "-5"}}
	svc := NewServerAuditRetentionService(fs)

	svc.runOnce(context.Background())

	if fs.purgeCalled {
		t.Fatal("expected no purge for a negative retention setting")
	}
}

func TestServerAuditRetention_NotLeader_SkipsSweep(t *testing.T) {
	fs := &auditRetentionFakeStore{settings: map[string]string{"audit.server_retention_days": "30"}}
	svc := NewServerAuditRetentionService(fs)
	svc.SetLeader(fakeElection(false))

	svc.runOnce(context.Background())

	if fs.purgeCalled {
		t.Fatal("expected no purge on a non-leader instance")
	}
}

func TestServerAuditRetention_PurgesWithConfiguredCutoff(t *testing.T) {
	fs := &auditRetentionFakeStore{
		settings:   map[string]string{"audit.server_retention_days": "14"},
		purgeCount: 42,
	}
	svc := NewServerAuditRetentionService(fs)

	want := time.Now().Add(-14 * 24 * time.Hour)
	svc.runOnce(context.Background())

	if !fs.purgeCalled {
		t.Fatal("expected a purge call for a positive retention setting")
	}
	if fs.purgeCutoffSeen.Before(want.Add(-time.Second)) || fs.purgeCutoffSeen.After(want.Add(time.Second)) {
		t.Errorf("cutoff = %v, want ~14 days ago (~%v)", fs.purgeCutoffSeen, want)
	}
}

func TestServerAuditRetention_PurgeError_DoesNotPanic(t *testing.T) {
	fs := &auditRetentionFakeStore{
		settings: map[string]string{"audit.server_retention_days": "7"},
		purgeErr: errors.New("db down"),
	}
	svc := NewServerAuditRetentionService(fs)

	svc.runOnce(context.Background()) // must simply log and return, not panic
}
