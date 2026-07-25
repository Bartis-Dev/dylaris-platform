package storagereach

import (
	"context"
	"errors"
	"testing"
	"time"
)

var errRO = errors.New("read-only file system")

func beaconOpts(coreID string, peers []string, now time.Time) BeaconOptions {
	return BeaconOptions{
		CoreID: coreID, Fingerprint: "fp-1", Participants: peers,
		MaxAge: 300 * time.Second,
		Now:    func() time.Time { return now },
	}
}

func TestRefreshBeacon_WritesItsOwnAndSeesAPeer(t *testing.T) {
	root := t.TempDir()
	peers := []string{"core-a", "core-b"}
	now := time.Unix(1_000_000, 0)

	// Unlike a round, two Cores on their OWN schedules still see each other,
	// because the beacon path is stable rather than round-scoped. This is the
	// whole reason the beacon exists.
	a := RefreshBeacon(context.Background(), newProbeProvider(root), beaconOpts("core-a", peers, now))
	if !a.Wrote {
		t.Fatalf("core-a did not write its beacon: %+v", a)
	}
	if len(a.SeenPeers) != 0 {
		t.Fatalf("core-a SeenPeers = %v, want empty before core-b wrote", a.SeenPeers)
	}

	b := RefreshBeacon(context.Background(), newProbeProvider(root), beaconOpts("core-b", peers, now.Add(time.Second)))
	if len(b.SeenPeers) != 1 || b.SeenPeers[0] != "core-a" {
		t.Fatalf("core-b SeenPeers = %v, want [core-a]", b.SeenPeers)
	}
	if len(b.CrossWroteTo) != 1 || b.CrossWroteTo[0] != "core-a" {
		t.Fatalf("core-b CrossWroteTo = %v, want [core-a]", b.CrossWroteTo)
	}

	// A LATER refresh by core-a now sees core-b, with no coordinator and no
	// shared round id.
	a2 := RefreshBeacon(context.Background(), newProbeProvider(root), beaconOpts("core-a", peers, now.Add(2*time.Second)))
	if len(a2.SeenPeers) != 1 || a2.SeenPeers[0] != "core-b" {
		t.Fatalf("core-a SeenPeers = %v on the second pass, want [core-b]", a2.SeenPeers)
	}
}

func TestRefreshBeacon_IgnoresAStaleBeacon(t *testing.T) {
	// A Core that died leaves its beacon behind. Counting it would let a dead
	// instance vouch for a share nobody is using.
	root := t.TempDir()
	peers := []string{"core-a", "core-b"}
	old := time.Unix(1_000_000, 0)

	RefreshBeacon(context.Background(), newProbeProvider(root), beaconOpts("core-b", peers, old))

	rep := RefreshBeacon(context.Background(), newProbeProvider(root),
		beaconOpts("core-a", peers, old.Add(400*time.Second)))

	if len(rep.SeenPeers) != 0 {
		t.Fatalf("SeenPeers = %v, want empty - core-b's beacon is 400s old with a 300s MaxAge", rep.SeenPeers)
	}
}

func TestRefreshBeacon_ReportsAPeerOnADifferentBackend(t *testing.T) {
	root := t.TempDir()
	peers := []string{"core-a", "core-b"}
	now := time.Unix(1_000_000, 0)

	stale := beaconOpts("core-b", peers, now)
	stale.Fingerprint = "fp-OTHER"
	RefreshBeacon(context.Background(), newProbeProvider(root), stale)

	rep := RefreshBeacon(context.Background(), newProbeProvider(root), beaconOpts("core-a", peers, now))

	if len(rep.SeenPeers) != 0 {
		t.Fatalf("SeenPeers = %v, want empty", rep.SeenPeers)
	}
	if len(rep.MismatchedPeers) != 1 || rep.MismatchedPeers[0] != "core-b" {
		t.Fatalf("MismatchedPeers = %v, want [core-b]", rep.MismatchedPeers)
	}
}

func TestRefreshBeacon_OverwritesItsOwnBeaconRatherThanAccumulating(t *testing.T) {
	// The beacon is refreshed every 120s forever; it must be ONE file per
	// Core, not one per tick.
	root := t.TempDir()
	prov := newProbeProvider(root)
	peers := []string{"core-a"}
	now := time.Unix(1_000_000, 0)

	for i := 0; i < 3; i++ {
		RefreshBeacon(context.Background(), prov, beaconOpts("core-a", peers, now.Add(time.Duration(i)*time.Minute)))
	}

	infos, err := prov.ListFiles(context.Background(), FleetDir)
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	beacons := 0
	for _, fi := range infos {
		if fi.Name == "core-a" {
			beacons++
		}
	}
	if beacons != 1 {
		t.Fatalf("found %d beacons for core-a after 3 refreshes, want 1", beacons)
	}
}

func TestRefreshBeacon_WriteDeniedIsReported(t *testing.T) {
	prov := newProbeProvider(t.TempDir())
	prov.writeErrPrefix = "core-a"
	prov.writeErr = errRO

	rep := RefreshBeacon(context.Background(), prov, beaconOpts("core-a", []string{"core-a", "core-b"}, time.Unix(1, 0)))

	if rep.Wrote {
		t.Fatal("Wrote = true despite a failing write")
	}
	if rep.WriteErr == "" {
		t.Error("WriteErr is empty; the operator is told nothing")
	}
}
