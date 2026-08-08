package services

import (
	"context"
	"errors"
	"testing"
	"time"

	"dylaris-core/store"
)

// ticketAuditRetentionFakeStore embeds store.Store (nil) so it satisfies the
// full interface at compile time; only the two methods
// TicketAuditRetentionService touches are overridden.
type ticketAuditRetentionFakeStore struct {
	store.Store

	settings map[string]string

	purgeCutoffSeen time.Time
	purgeCalled     bool
	purgeCount      int
	purgeErr        error
}

func (f *ticketAuditRetentionFakeStore) GetSetting(key string) (string, error) {
	v, ok := f.settings[key]
	if !ok {
		return "", errors.New("not found")
	}
	return v, nil
}

func (f *ticketAuditRetentionFakeStore) PurgeTicketAuditOlderThan(cutoff time.Time) (int, error) {
	f.purgeCalled = true
	f.purgeCutoffSeen = cutoff
	return f.purgeCount, f.purgeErr
}

// The setting existed, was validated and was stored for the whole life of the
// Ticket module with no consumer, so this is the test that would have failed:
// a saved horizon has to actually delete something.
func TestTicketAuditRetention_PurgesWithConfiguredCutoff(t *testing.T) {
	fs := &ticketAuditRetentionFakeStore{
		settings:   map[string]string{"tickets.audit_retention_days": "30"},
		purgeCount: 7,
	}
	svc := NewTicketAuditRetentionService(fs)

	want := time.Now().Add(-30 * 24 * time.Hour)
	svc.runOnce(context.Background())

	if !fs.purgeCalled {
		t.Fatal("expected a purge call for a positive retention setting")
	}
	if fs.purgeCutoffSeen.Before(want.Add(-time.Second)) || fs.purgeCutoffSeen.After(want.Add(time.Second)) {
		t.Errorf("cutoff = %v, want ~30 days ago (~%v)", fs.purgeCutoffSeen, want)
	}
}

func TestTicketAuditRetention_ZeroDays_SkipsSweep(t *testing.T) {
	fs := &ticketAuditRetentionFakeStore{settings: map[string]string{"tickets.audit_retention_days": "0"}}
	svc := NewTicketAuditRetentionService(fs)

	svc.runOnce(context.Background())

	if fs.purgeCalled {
		t.Fatal("expected no purge when retention is 0 (keep forever)")
	}
}

// The sweep deletes, so it must not act on a horizon nobody saved. The Ticket
// Settings card prefills a suggestion; a prefill is not a policy.
func TestTicketAuditRetention_UnsetDays_SkipsSweep(t *testing.T) {
	fs := &ticketAuditRetentionFakeStore{settings: map[string]string{}}
	svc := NewTicketAuditRetentionService(fs)

	svc.runOnce(context.Background())

	if fs.purgeCalled {
		t.Fatal("expected no purge when the setting is unset")
	}
}

func TestTicketAuditRetention_GarbageDays_SkipsSweep(t *testing.T) {
	for _, v := range []string{"-5", "", "forever", "30d"} {
		fs := &ticketAuditRetentionFakeStore{settings: map[string]string{"tickets.audit_retention_days": v}}
		svc := NewTicketAuditRetentionService(fs)

		svc.runOnce(context.Background())

		if fs.purgeCalled {
			t.Fatalf("value %q: expected no purge for an unusable retention setting", v)
		}
	}
}

func TestTicketAuditRetention_NotLeader_SkipsSweep(t *testing.T) {
	fs := &ticketAuditRetentionFakeStore{settings: map[string]string{"tickets.audit_retention_days": "30"}}
	svc := NewTicketAuditRetentionService(fs)
	svc.SetLeader(fakeElection(false))

	svc.runOnce(context.Background())

	if fs.purgeCalled {
		t.Fatal("expected no purge on a non-leader instance")
	}
}

func TestTicketAuditRetention_PurgeError_DoesNotPanic(t *testing.T) {
	fs := &ticketAuditRetentionFakeStore{
		settings: map[string]string{"tickets.audit_retention_days": "7"},
		purgeErr: errors.New("db down"),
	}
	svc := NewTicketAuditRetentionService(fs)

	svc.runOnce(context.Background()) // must simply log and return, not panic
}
