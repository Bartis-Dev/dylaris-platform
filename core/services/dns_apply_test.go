package services

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"testing"
)

// applyDNSPlan is the half of the reconciler that CREATES records and removes
// the ones that no longer belong. Its sibling, the orphan sweep, is covered in
// dns_sweep_test.go; these are the rules for names that are still advertised.

func planFor(zone, name string, ips ...string) DNSPlan {
	return DNSPlan{Names: []PlannedName{{Name: name, Zone: zone, IPs: ips}}}
}

func TestApplyPlan_CreatesMissingAddresses(t *testing.T) {
	p := newSweepProvider()
	p.put("example.com", "*.eu.example.com", "1.1.1.1") // one already there

	written, failures, status := applyDNSPlan(context.Background(), p,
		planFor("example.com", "*.eu.example.com", "1.1.1.1", "2.2.2.2"))

	if len(failures) != 0 {
		t.Fatalf("failures = %v", failures)
	}
	// Only the missing one is created; an existing record is left alone rather
	// than deleted and re-added, which would blink the name out of DNS.
	if len(p.created) != 1 || p.created[0] != "example.com|*.eu.example.com|2.2.2.2" {
		t.Errorf("created = %v, want only the missing address", p.created)
	}
	if len(p.deleted) != 0 {
		t.Errorf("deleted = %v, want nothing", p.deleted)
	}
	if len(written.Names) != 1 {
		t.Errorf("written = %+v, want the name claimed", written.Names)
	}
	if status.RecordCount != 2 {
		t.Errorf("RecordCount = %d, want 2", status.RecordCount)
	}
}

// An edge that went away must stop answering, which is the whole point of the
// reconciler running on a timer.
func TestApplyPlan_DeletesAddressesThatAreNoLongerLive(t *testing.T) {
	p := newSweepProvider()
	p.put("example.com", "*.eu.example.com", "1.1.1.1", "9.9.9.9")

	_, failures, _ := applyDNSPlan(context.Background(), p,
		planFor("example.com", "*.eu.example.com", "1.1.1.1"))

	if len(failures) != 0 {
		t.Fatalf("failures = %v", failures)
	}
	if len(p.deleted) != 1 || p.deleted[0] != "example.com|*.eu.example.com|9.9.9.9" {
		t.Errorf("deleted = %v, want the departed edge only", p.deleted)
	}
	if len(p.created) != 0 {
		t.Errorf("created = %v, want nothing", p.created)
	}
}

// The steady state must be silent: no writes at all when reality already
// matches the plan, or every tick would churn the zone.
func TestApplyPlan_NoWritesWhenAlreadyCorrect(t *testing.T) {
	p := newSweepProvider()
	p.put("example.com", "*.eu.example.com", "1.1.1.1", "2.2.2.2")

	written, failures, _ := applyDNSPlan(context.Background(), p,
		planFor("example.com", "*.eu.example.com", "2.2.2.2", "1.1.1.1"))

	if len(p.created) != 0 || len(p.deleted) != 0 {
		t.Errorf("created %v / deleted %v, want neither", p.created, p.deleted)
	}
	if len(failures) != 0 || len(written.Names) != 1 {
		t.Errorf("failures = %v, written = %+v", failures, written.Names)
	}
}

// A listing failure must never be read as "the name has no records", or the
// next step would delete every address the name legitimately holds.
func TestApplyPlan_ListFailureWritesNothingAndDoesNotClaim(t *testing.T) {
	p := newSweepProvider()
	p.put("example.com", "*.eu.example.com", "1.1.1.1")
	p.listErr["*.eu.example.com"] = errors.New("upstream 500")

	written, failures, _ := applyDNSPlan(context.Background(), p,
		planFor("example.com", "*.eu.example.com", "2.2.2.2"))

	if len(p.created) != 0 || len(p.deleted) != 0 {
		t.Fatalf("touched the zone after a failed listing: created %v deleted %v", p.created, p.deleted)
	}
	if len(failures) != 1 {
		t.Errorf("failures = %v, want the listing error reported", failures)
	}
	// Not claimed: claiming a name the reconciler could not read would make it
	// deletable later on evidence it never had.
	if len(written.Names) != 0 {
		t.Errorf("written = %+v, want no claim", written.Names)
	}
}

// The ownership rule that the sweep depends on: a name is claimed only when
// every write for it succeeded.
func TestApplyPlan_PartialFailureDoesNotClaimTheName(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*sweepProvider)
		plan  DNSPlan
	}{
		{
			name: "a failed create",
			setup: func(p *sweepProvider) {
				p.createErr["*.eu.example.com|2.2.2.2"] = errors.New("rate limited")
			},
			plan: planFor("example.com", "*.eu.example.com", "2.2.2.2"),
		},
		{
			name: "a failed delete",
			setup: func(p *sweepProvider) {
				p.put("example.com", "*.eu.example.com", "1.1.1.1", "9.9.9.9")
				p.deleteAt["*.eu.example.com|9.9.9.9"] = errors.New("rate limited")
			},
			plan: planFor("example.com", "*.eu.example.com", "1.1.1.1"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newSweepProvider()
			tt.setup(p)
			written, failures, _ := applyDNSPlan(context.Background(), p, tt.plan)
			if len(failures) == 0 {
				t.Error("no failure reported")
			}
			if len(written.Names) != 0 {
				t.Errorf("written = %+v, want the name left unclaimed", written.Names)
			}
		})
	}
}

// One name failing must not stop the others - a rate limit on one region would
// otherwise stall every other region's records behind it.
func TestApplyPlan_OneBadNameDoesNotBlockTheRest(t *testing.T) {
	p := newSweepProvider()
	p.listErr["*.eu.example.com"] = errors.New("boom")

	plan := DNSPlan{Names: []PlannedName{
		{Name: "*.eu.example.com", Zone: "example.com", IPs: []string{"1.1.1.1"}},
		{Name: "*.us.example.com", Zone: "example.com", IPs: []string{"3.3.3.3"}},
	}}
	written, failures, status := applyDNSPlan(context.Background(), p, plan)

	if len(p.created) != 1 || p.created[0] != "example.com|*.us.example.com|3.3.3.3" {
		t.Errorf("created = %v, want the healthy name still written", p.created)
	}
	if len(written.Names) != 1 || written.Names[0].Name != "*.us.example.com" {
		t.Errorf("written = %+v, want only the healthy name claimed", written.Names)
	}
	if len(failures) != 1 {
		t.Errorf("failures = %v", failures)
	}
	// Status counts what was PLANNED, so the panel shows the intended state
	// rather than silently under-reporting a name that failed this pass.
	sort.Strings(status.ManagedNames)
	if !reflect.DeepEqual(status.ManagedNames, []string{"*.eu.example.com", "*.us.example.com"}) {
		t.Errorf("ManagedNames = %v, want both", status.ManagedNames)
	}
}

// A name outside every managed zone is reported and never written - guessing a
// zone would mean writing into one the operator never released.
func TestApplyPlan_UnroutableIsReportedNotWritten(t *testing.T) {
	p := newSweepProvider()
	plan := DNSPlan{Unroutable: []UnroutableName{{Name: "*.eu.elsewhere.net", Origin: DNSOriginEdge}}}

	written, failures, _ := applyDNSPlan(context.Background(), p, plan)

	if len(p.created) != 0 || len(p.deleted) != 0 {
		t.Fatalf("wrote something for an unroutable name: %v %v", p.created, p.deleted)
	}
	if len(failures) != 1 {
		t.Fatalf("failures = %v, want it reported", failures)
	}
	if len(written.Names) != 0 {
		t.Errorf("written = %+v", written.Names)
	}
}

// Several names in different zones are handled independently, which is what
// multi-zone support rests on.
func TestApplyPlan_HandlesSeveralZones(t *testing.T) {
	p := newSweepProvider()
	plan := DNSPlan{Names: []PlannedName{
		{Name: "*.eu.brand-a.com", Zone: "brand-a.com", IPs: []string{"1.1.1.1"}},
		{Name: "*.eu.brand-b.net", Zone: "brand-b.net", IPs: []string{"1.1.1.1"}},
	}}
	written, failures, _ := applyDNSPlan(context.Background(), p, plan)

	if len(failures) != 0 || len(written.Names) != 2 {
		t.Fatalf("failures = %v, written = %+v", failures, written.Names)
	}
	sort.Strings(p.created)
	want := []string{"brand-a.com|*.eu.brand-a.com|1.1.1.1", "brand-b.net|*.eu.brand-b.net|1.1.1.1"}
	if !reflect.DeepEqual(p.created, want) {
		t.Errorf("created = %v, want %v", p.created, want)
	}
}
