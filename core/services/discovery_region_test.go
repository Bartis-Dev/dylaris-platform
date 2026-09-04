package services

import (
	"bytes"
	"errors"
	"log"
	"strings"
	"testing"

	"dylaris-core/models"
	"dylaris-core/store"
)

// regionFakeStore embeds store.Store (nil) so it satisfies the full interface
// while implementing only the two calls this path makes; anything else panics
// loudly rather than passing quietly.
type regionFakeStore struct {
	store.Store
	known   map[string]bool
	written []string
}

func (f *regionFakeStore) GetRegion(id string) (*models.Region, error) {
	if f.known[id] {
		return &models.Region{ID: id}, nil
	}
	return nil, errors.New("no such region")
}

func (f *regionFakeStore) SetNodeRegion(id int, region string) error {
	f.written = append(f.written, region)
	return nil
}

func newRegionDiscovery(known ...string) (*DiscoveryService, *regionFakeStore) {
	f := &regionFakeStore{known: map[string]bool{}}
	for _, k := range known {
		f.known[k] = true
	}
	return &DiscoveryService{store: f, badRegionReported: map[int]string{}}, f
}

// captureLog redirects the standard logger for one test. logErrf writes there
// as well as to the error stream, and the stream is nil in tests, so this is
// where a report is observable.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev, flags := log.Writer(), log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(prev)
		log.SetFlags(flags)
	})
	return &buf
}

// The fact this whole guard exists for: nodes.region is a join key, so a
// DYLARIS_REGION typo must not reach the column. Before this, it was written
// through unchecked and the beam relay filter, the rebalancer, the staff
// visibility filter and the region-delete guard all then answered about a
// region no row describes.
func TestAnUnknownHeartbeatRegionIsNotWritten(t *testing.T) {
	d, f := newRegionDiscovery("eu")
	captureLog(t)

	d.applyHeartbeatRegion(&models.Node{ID: 1, Name: "n1", Region: "default"}, "eu-cental")

	if len(f.written) != 0 {
		t.Fatalf("an unknown region reached the column: %v", f.written)
	}
}

// Refusing over casing alone would be its own bug: the operator named a region
// that plainly exists. Region ids are canonically lowercase (CreateRegion
// lowercases, the id regex forbids the rest), so the reported value is
// normalised to that form rather than rejected.
func TestAKnownRegionIsNormalisedRatherThanRefused(t *testing.T) {
	d, f := newRegionDiscovery("eu")
	captureLog(t)

	d.applyHeartbeatRegion(&models.Node{ID: 1, Name: "n1", Region: "default"}, "  EU  ")

	if len(f.written) != 1 || f.written[0] != "eu" {
		t.Fatalf("written = %v, want exactly [eu]", f.written)
	}
}

func TestARegionThatAlreadyMatchesIsNotRewritten(t *testing.T) {
	d, f := newRegionDiscovery("eu")
	captureLog(t)

	d.applyHeartbeatRegion(&models.Node{ID: 1, Name: "n1", Region: "eu"}, "EU")

	if len(f.written) != 0 {
		t.Fatalf("rewrote a region that was already correct: %v", f.written)
	}
}

func TestAnEmptyHeartbeatRegionIsIgnored(t *testing.T) {
	d, f := newRegionDiscovery("eu")
	captureLog(t)

	d.applyHeartbeatRegion(&models.Node{ID: 1, Name: "n1", Region: "eu"}, "   ")

	if len(f.written) != 0 {
		t.Fatalf("whitespace was treated as a region: %v", f.written)
	}
}

// The dedupe is not tidiness. scanNodes runs every 5 seconds and the error
// stream is capped at 500 entries and trims itself, so one misconfigured node
// reporting every tick would evict every other error in about forty minutes.
func TestAnUnknownRegionIsReportedOncePerDistinctValue(t *testing.T) {
	d, _ := newRegionDiscovery("eu")
	buf := captureLog(t)
	node := &models.Node{ID: 1, Name: "n1", Region: "default"}

	for i := 0; i < 5; i++ {
		d.applyHeartbeatRegion(node, "eu-cental")
	}
	if got := strings.Count(buf.String(), "eu-cental"); got != 1 {
		t.Fatalf("reported %d times, want 1:\n%s", got, buf.String())
	}

	// A DIFFERENT bad value is a different mistake and is reported again.
	d.applyHeartbeatRegion(node, "eu-centrl")
	if got := strings.Count(buf.String(), "eu-centrl"); got != 1 {
		t.Fatalf("a second distinct bad value was swallowed:\n%s", buf.String())
	}
}

// Once the operator fixes it, the node must be able to be reported again if it
// later regresses - otherwise the first typo silences the node forever.
func TestFixingTheRegionClearsTheReportedValue(t *testing.T) {
	d, _ := newRegionDiscovery("eu")
	captureLog(t)
	node := &models.Node{ID: 1, Name: "n1", Region: "default"}

	d.applyHeartbeatRegion(node, "eu-cental")
	d.applyHeartbeatRegion(node, "eu")
	if _, still := d.badRegionReported[node.ID]; still {
		t.Fatal("a node that now reports a valid region is still marked as bad")
	}
}
