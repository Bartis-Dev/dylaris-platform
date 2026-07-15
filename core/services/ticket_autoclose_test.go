package services

import (
	"context"
	"errors"
	"testing"
	"time"

	"dylaris-core/models"
	"dylaris-core/store"
)

// ticketAutoCloseFakeStore embeds store.Store (nil) so it satisfies the full
// interface at compile time; only the methods TicketAutoCloseService touches
// are overridden. Any other call would panic - the tests never make one.
type ticketAutoCloseFakeStore struct {
	store.Store

	settings map[string]string

	resolvedIDs    []int
	listErr        error
	listCutoffSeen time.Time

	closeErrFor map[int]error
	closedIDs   []int

	auditEvents []models.TicketAuditEvent
}

func (f *ticketAutoCloseFakeStore) GetSetting(key string) (string, error) {
	v, ok := f.settings[key]
	if !ok {
		return "", errors.New("not found")
	}
	return v, nil
}

func (f *ticketAutoCloseFakeStore) ListResolvedTicketsOlderThan(cutoff time.Time) ([]int, error) {
	f.listCutoffSeen = cutoff
	return f.resolvedIDs, f.listErr
}

func (f *ticketAutoCloseFakeStore) UpdateTicketStatus(id int, status string) error {
	if err := f.closeErrFor[id]; err != nil {
		return err
	}
	f.closedIDs = append(f.closedIDs, id)
	return nil
}

func (f *ticketAutoCloseFakeStore) InsertTicketAudit(ev *models.TicketAuditEvent) error {
	f.auditEvents = append(f.auditEvents, *ev)
	return nil
}

type fakeElection bool

func (f fakeElection) IsLeader() bool { return bool(f) }

func TestTicketAutoClose_DisabledByDefault_NoOp(t *testing.T) {
	fs := &ticketAutoCloseFakeStore{settings: map[string]string{}}
	svc := NewTicketAutoCloseService(fs)

	svc.runOnce(context.Background())

	if len(fs.closedIDs) != 0 {
		t.Fatalf("expected no closes when the feature is off by default, got %v", fs.closedIDs)
	}
}

func TestTicketAutoClose_ExplicitlyDisabled_NoOp(t *testing.T) {
	fs := &ticketAutoCloseFakeStore{settings: map[string]string{"tickets.auto_close_enabled": "false"}}
	svc := NewTicketAutoCloseService(fs)

	svc.runOnce(context.Background())

	if len(fs.closedIDs) != 0 {
		t.Fatalf("expected no closes when disabled, got %v", fs.closedIDs)
	}
}

func TestTicketAutoClose_NotLeader_NoOp(t *testing.T) {
	fs := &ticketAutoCloseFakeStore{
		settings:    map[string]string{"tickets.auto_close_enabled": "true"},
		resolvedIDs: []int{1, 2},
	}
	svc := NewTicketAutoCloseService(fs)
	svc.SetLeader(fakeElection(false))

	svc.runOnce(context.Background())

	if len(fs.closedIDs) != 0 {
		t.Fatalf("expected no closes on a non-leader instance, got %v", fs.closedIDs)
	}
}

func TestTicketAutoClose_UsesConfiguredDaysForCutoff(t *testing.T) {
	fs := &ticketAutoCloseFakeStore{
		settings: map[string]string{
			"tickets.auto_close_enabled":             "true",
			"tickets.auto_close_days_after_resolved": "3",
		},
	}
	svc := NewTicketAutoCloseService(fs)

	before := time.Now().Add(-3 * 24 * time.Hour)
	svc.runOnce(context.Background())
	after := time.Now().Add(-3 * 24 * time.Hour)

	if fs.listCutoffSeen.Before(before.Add(-time.Second)) || fs.listCutoffSeen.After(after.Add(time.Second)) {
		t.Errorf("cutoff = %v, want ~3 days ago (between %v and %v)", fs.listCutoffSeen, before, after)
	}
}

func TestTicketAutoClose_DaysUnset_DefaultsToSeven(t *testing.T) {
	fs := &ticketAutoCloseFakeStore{
		settings: map[string]string{"tickets.auto_close_enabled": "true"},
	}
	svc := NewTicketAutoCloseService(fs)

	want := time.Now().Add(-7 * 24 * time.Hour)
	svc.runOnce(context.Background())

	if fs.listCutoffSeen.Before(want.Add(-time.Second)) || fs.listCutoffSeen.After(want.Add(time.Second)) {
		t.Errorf("cutoff = %v, want ~7 days ago (safety default) got days-unset", fs.listCutoffSeen)
	}
}

func TestTicketAutoClose_DaysBelowOne_FallsBackToSeven(t *testing.T) {
	fs := &ticketAutoCloseFakeStore{
		settings: map[string]string{
			"tickets.auto_close_enabled":             "true",
			"tickets.auto_close_days_after_resolved": "0",
		},
	}
	svc := NewTicketAutoCloseService(fs)

	want := time.Now().Add(-7 * 24 * time.Hour)
	svc.runOnce(context.Background())

	if fs.listCutoffSeen.Before(want.Add(-time.Second)) || fs.listCutoffSeen.After(want.Add(time.Second)) {
		t.Errorf("cutoff = %v, want ~7 days ago (days<1 must fall back to 7)", fs.listCutoffSeen)
	}
}

func TestTicketAutoClose_ClosesEachResolvedTicketAndRecordsAudit(t *testing.T) {
	fs := &ticketAutoCloseFakeStore{
		settings: map[string]string{
			"tickets.auto_close_enabled":             "true",
			"tickets.auto_close_days_after_resolved": "5",
		},
		resolvedIDs: []int{10, 11, 12},
	}
	svc := NewTicketAutoCloseService(fs)

	svc.runOnce(context.Background())

	if len(fs.closedIDs) != 3 {
		t.Fatalf("closedIDs = %v, want all 3 tickets closed", fs.closedIDs)
	}
	if len(fs.auditEvents) != 3 {
		t.Fatalf("auditEvents = %+v, want 3 audit rows", fs.auditEvents)
	}
	for _, ev := range fs.auditEvents {
		if ev.EventType != "auto_closed" {
			t.Errorf("audit event type = %q, want auto_closed", ev.EventType)
		}
		if ev.Metadata["days_after_resolved"] != 5 {
			t.Errorf("audit metadata = %+v, want days_after_resolved=5", ev.Metadata)
		}
	}
}

func TestTicketAutoClose_CloseError_SkipsAuditButContinuesOthers(t *testing.T) {
	fs := &ticketAutoCloseFakeStore{
		settings:    map[string]string{"tickets.auto_close_enabled": "true"},
		resolvedIDs: []int{20, 21},
		closeErrFor: map[int]error{20: errors.New("db down")},
	}
	svc := NewTicketAutoCloseService(fs)

	svc.runOnce(context.Background())

	if len(fs.closedIDs) != 1 || fs.closedIDs[0] != 21 {
		t.Fatalf("closedIDs = %v, want only ticket 21 (20 failed to close)", fs.closedIDs)
	}
	if len(fs.auditEvents) != 1 || fs.auditEvents[0].TicketID != 21 {
		t.Fatalf("auditEvents = %+v, want only an entry for ticket 21", fs.auditEvents)
	}
}

func TestTicketAutoClose_ListError_NoCloses(t *testing.T) {
	fs := &ticketAutoCloseFakeStore{
		settings: map[string]string{"tickets.auto_close_enabled": "true"},
		listErr:  errors.New("db down"),
	}
	svc := NewTicketAutoCloseService(fs)

	svc.runOnce(context.Background())

	if len(fs.closedIDs) != 0 {
		t.Fatalf("expected no closes when listing resolved tickets fails, got %v", fs.closedIDs)
	}
}
