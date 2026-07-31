package services

import (
	"context"
	"errors"
	"testing"
	"time"
)

// The orphan sweep is the one path that DELETES. Every test here is about a way
// it could remove a record it does not own, or fail to give one back.

// sweepProvider records deletions and can be made to fail on demand.
type sweepProvider struct {
	records  map[string][]DNSRecord // "zone|name" -> records
	deleted  []string               // "zone|name|ip", in order
	listErr  map[string]error       // keyed by name
	deleteAt map[string]error       // keyed by "name|ip"
}

func newSweepProvider() *sweepProvider {
	return &sweepProvider{
		records:  map[string][]DNSRecord{},
		listErr:  map[string]error{},
		deleteAt: map[string]error{},
	}
}

func (p *sweepProvider) put(zone, name string, ips ...string) {
	recs := make([]DNSRecord, 0, len(ips))
	for _, ip := range ips {
		recs = append(recs, DNSRecord{Name: name, IP: ip})
	}
	p.records[zone+"|"+name] = recs
}

func (p *sweepProvider) ListA(_ context.Context, zone, name string) ([]DNSRecord, error) {
	if err := p.listErr[name]; err != nil {
		return nil, err
	}
	return p.records[zone+"|"+name], nil
}

func (p *sweepProvider) CreateA(_ context.Context, _, _, _ string) error { return nil }

func (p *sweepProvider) DeleteA(_ context.Context, zone, name, ip string) error {
	if err := p.deleteAt[name+"|"+ip]; err != nil {
		return err
	}
	p.deleted = append(p.deleted, zone+"|"+name+"|"+ip)
	return nil
}

func (p *sweepProvider) Zones(_ context.Context) ([]string, error) { return nil, nil }

// fakeOwnership is the registry in memory.
type fakeOwnership struct {
	owned    map[string]OwnedName
	loadErr  error
	saveErr  error
	saved    map[string]OwnedName
	saveCall int
}

func (f *fakeOwnership) load(context.Context) (map[string]OwnedName, error) {
	if f.loadErr != nil {
		return nil, f.loadErr
	}
	out := make(map[string]OwnedName, len(f.owned))
	for k, v := range f.owned {
		out[k] = v
	}
	return out, nil
}

func (f *fakeOwnership) save(_ context.Context, owned map[string]OwnedName) error {
	f.saveCall++
	f.saved = owned
	return f.saveErr
}

func staleOwned(zone string, names ...string) map[string]OwnedName {
	old := time.Now().UTC().Add(-2 * time.Hour)
	out := map[string]OwnedName{}
	for _, n := range names {
		out[n] = OwnedName{Zone: zone, LastAdvertised: old}
	}
	return out
}

// The governing property: a name the reconciler never recorded creating cannot
// be deleted, however it looks in the zone. This is what keeps a released zone's
// website and mail records safe.
func TestSweep_NeverTouchesUnownedNames(t *testing.T) {
	p := newSweepProvider()
	p.put("example.com", "www.example.com", "1.2.3.4")  // the hoster's website
	p.put("example.com", "*.eu.example.com", "9.9.9.9") // ours, and stale
	reg := &fakeOwnership{owned: staleOwned("example.com", "*.eu.example.com")}

	failures := sweepOrphansWith(context.Background(), p, reg, DNSPlan{}, time.Now().UTC(), time.Minute)

	if len(failures) != 0 {
		t.Fatalf("failures = %v", failures)
	}
	if len(p.deleted) != 1 || p.deleted[0] != "example.com|*.eu.example.com|9.9.9.9" {
		t.Fatalf("deleted = %v, want only the owned name", p.deleted)
	}
}

// A name still advertised is not an orphan, however long it has been owned.
func TestSweep_KeepsAdvertisedNames(t *testing.T) {
	p := newSweepProvider()
	p.put("example.com", "*.eu.example.com", "9.9.9.9")
	reg := &fakeOwnership{owned: staleOwned("example.com", "*.eu.example.com")}

	written := DNSPlan{Names: []PlannedName{{Name: "*.eu.example.com", Zone: "example.com"}}}
	sweepOrphansWith(context.Background(), p, reg, written, time.Now().UTC(), time.Minute)

	if len(p.deleted) != 0 {
		t.Fatalf("deleted %v while the name was still advertised", p.deleted)
	}
}

// Inside the grace period nothing goes, which is what stops a rolling edge
// restart from taking a live region out of DNS.
func TestSweep_RespectsGracePeriod(t *testing.T) {
	p := newSweepProvider()
	p.put("example.com", "*.eu.example.com", "9.9.9.9")
	now := time.Now().UTC()
	reg := &fakeOwnership{owned: map[string]OwnedName{
		"*.eu.example.com": {Zone: "example.com", LastAdvertised: now.Add(-time.Minute)},
	}}

	sweepOrphansWith(context.Background(), p, reg, DNSPlan{}, now, 15*time.Minute)

	if len(p.deleted) != 0 {
		t.Fatalf("deleted %v inside the grace period", p.deleted)
	}
	// The claim must survive, or the next pass would have nothing to expire.
	if _, ok := reg.saved["*.eu.example.com"]; !ok {
		t.Error("claim dropped while still inside the grace period")
	}
}

// Without the registry there is no proof of ownership, so nothing may go.
func TestSweep_UnreadableRegistryDeletesNothing(t *testing.T) {
	p := newSweepProvider()
	p.put("example.com", "*.eu.example.com", "9.9.9.9")
	reg := &fakeOwnership{loadErr: errors.New("redis down")}

	failures := sweepOrphansWith(context.Background(), p, reg, DNSPlan{}, time.Now().UTC(), time.Minute)

	if len(p.deleted) != 0 {
		t.Fatalf("deleted %v with an unreadable registry", p.deleted)
	}
	if len(failures) != 0 {
		t.Errorf("failures = %v, want none - creates already happened", failures)
	}
	if reg.saveCall != 0 {
		t.Error("saved a registry it could not read first")
	}
}

// A listing failure must not be read as "the name has no records left".
func TestSweep_ListFailureDeletesNothing(t *testing.T) {
	p := newSweepProvider()
	p.put("example.com", "*.eu.example.com", "9.9.9.9")
	p.listErr["*.eu.example.com"] = errors.New("upstream 500")
	reg := &fakeOwnership{owned: staleOwned("example.com", "*.eu.example.com")}

	failures := sweepOrphansWith(context.Background(), p, reg, DNSPlan{}, time.Now().UTC(), time.Minute)

	if len(p.deleted) != 0 {
		t.Fatalf("deleted %v after a failed listing", p.deleted)
	}
	if len(failures) != 1 {
		t.Fatalf("failures = %v, want the listing error reported", failures)
	}
	// The claim must survive so the next pass retries it.
	if _, ok := reg.saved["*.eu.example.com"]; !ok {
		t.Error("claim dropped even though nothing was deleted")
	}
}

// A partial delete keeps the claim, so the remaining record is retried rather
// than left behind with nothing remembering it is ours.
func TestSweep_PartialDeleteKeepsTheClaim(t *testing.T) {
	p := newSweepProvider()
	p.put("example.com", "*.eu.example.com", "1.1.1.1", "2.2.2.2")
	p.deleteAt["*.eu.example.com|2.2.2.2"] = errors.New("rate limited")
	reg := &fakeOwnership{owned: staleOwned("example.com", "*.eu.example.com")}

	failures := sweepOrphansWith(context.Background(), p, reg, DNSPlan{}, time.Now().UTC(), time.Minute)

	if len(p.deleted) != 1 || p.deleted[0] != "example.com|*.eu.example.com|1.1.1.1" {
		t.Fatalf("deleted = %v, want only the one that succeeded", p.deleted)
	}
	if len(failures) != 1 {
		t.Errorf("failures = %v, want the delete error reported", failures)
	}
	if _, ok := reg.saved["*.eu.example.com"]; !ok {
		t.Fatal("claim dropped after a PARTIAL delete - the leftover record is now unowned")
	}
}

// A fully removed orphan gives its claim back, so the registry does not grow
// without bound.
func TestSweep_FullDeleteDropsTheClaim(t *testing.T) {
	p := newSweepProvider()
	p.put("example.com", "*.eu.example.com", "1.1.1.1", "2.2.2.2")
	reg := &fakeOwnership{owned: staleOwned("example.com", "*.eu.example.com")}

	sweepOrphansWith(context.Background(), p, reg, DNSPlan{}, time.Now().UTC(), time.Minute)

	if len(p.deleted) != 2 {
		t.Fatalf("deleted = %v, want both records", p.deleted)
	}
	if _, ok := reg.saved["*.eu.example.com"]; ok {
		t.Error("claim kept after every record was removed")
	}
}

// Names written this pass are claimed, which is what makes them deletable later
// - and only those, so a name whose write FAILED is never claimed.
func TestSweep_ClaimsWhatWasWritten(t *testing.T) {
	p := newSweepProvider()
	reg := &fakeOwnership{owned: map[string]OwnedName{}}
	now := time.Now().UTC()

	written := DNSPlan{Names: []PlannedName{
		{Name: "*.eu.example.com", Zone: "example.com"},
		{Name: "beam-eu.example.com", Zone: "example.com"},
	}}
	sweepOrphansWith(context.Background(), p, reg, written, now, time.Minute)

	if len(reg.saved) != 2 {
		t.Fatalf("saved = %v, want both written names claimed", reg.saved)
	}
	for name, entry := range reg.saved {
		if entry.Zone != "example.com" {
			t.Errorf("%s zone = %q", name, entry.Zone)
		}
		if !entry.LastAdvertised.Equal(now) {
			t.Errorf("%s timestamp = %v, want now", name, entry.LastAdvertised)
		}
	}
}

// An orphan in a zone that is no longer managed still has its recorded zone, so
// it can be cleaned up rather than stranded forever.
func TestSweep_UsesTheRecordedZone(t *testing.T) {
	p := newSweepProvider()
	p.put("old.example", "*.eu.old.example", "9.9.9.9")
	reg := &fakeOwnership{owned: staleOwned("old.example", "*.eu.old.example")}

	sweepOrphansWith(context.Background(), p, reg, DNSPlan{}, time.Now().UTC(), time.Minute)

	if len(p.deleted) != 1 || p.deleted[0] != "old.example|*.eu.old.example|9.9.9.9" {
		t.Fatalf("deleted = %v, want the orphan removed from its recorded zone", p.deleted)
	}
}
