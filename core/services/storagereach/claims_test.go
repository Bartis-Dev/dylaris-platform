package storagereach

import (
	"context"
	"testing"
	"time"

	"dylaris-core/storage"

	"github.com/alicebob/miniredis/v2"
	"github.com/alicebob/miniredis/v2/server"
	"github.com/redis/go-redis/v9"
)

// claimingService builds a Core whose provider is rooted at its OWN directory
// unless the caller shares one, which is how a per-host volume that only looks
// shared is modelled: every Core reads its own writes back perfectly and sees
// nobody else.
func claimingService(t *testing.T, rdb *redis.Client, coreID, root string, online []string) *Service {
	t.Helper()
	return NewService(ServiceDeps{
		Redis:       rdb,
		CoreID:      coreID,
		NewProvider: sharedFactory(root),
		ConfigFor:   func() (Config, bool) { return Config{Backend: "path", Path: "/mnt/shared"}, true },
		OnlineCores: func(context.Context) ([]string, error) { return online, nil },
	})
}

func TestSelfCheck_APeerThatClaimsNoWriteDoesNotGateTheObserver(t *testing.T) {
	// The rolling upgrade. Replica 1 gets the new image; old-b still runs the
	// pre-feature binary, so it writes no fleet beacon and never will. Before
	// the write claim existed, new-b reported not-shared on both passes and
	// gated itself on the second - roughly half of every storage request 503ing
	// for the whole rollout, caused by nothing but the rollout.
	ctx := context.Background()
	deps := testDeps(t, t.TempDir(), "new-b", []string{"new-b", "old-a"})
	svc := NewService(deps)

	for pass := 1; pass <= 2; pass++ {
		res := svc.SelfCheck(ctx)
		if res.Status != StatusOK {
			t.Fatalf("pass %d status = %s, want ok: a peer that claims no beacon write is not expected in storage (missing %v)", pass, res.Status, res.MissingPeers)
		}
		if !svc.Status().Healthy() {
			t.Fatalf("gated on pass %d by an old-binary peer; a rolling upgrade would 503 its own storage routes", pass)
		}
	}

	faults, err := Faults(ctx, deps.Redis)
	if err != nil {
		t.Fatalf("Faults: %v", err)
	}
	if len(faults) != 0 {
		t.Fatalf("faults = %+v, want none - the observer's storage is fine", faults)
	}
}

func TestSelfCheck_APeerWhoseWriteFailedDoesNotGateItsHealthyObservers(t *testing.T) {
	// One broken host must not take the fleet's storage UI down with it.
	// core-b's beacon write fails, so core-b withdraws its claim and gates
	// ITSELF; core-a, on the same genuinely shared root, must stay open.
	ctx := context.Background()
	rdb := newReachTestRedis(t)
	root := t.TempDir()
	online := []string{"core-a", "core-b"}

	// Both Cores were healthy until now, so both are claiming.
	seedClaim(t, rdb, "core-a")
	seedClaim(t, rdb, "core-b")

	a := claimingService(t, rdb, "core-a", root, online)
	b := claimingService(t, rdb, "core-b", root, online)
	b.deps.NewProvider = func(Config) (storage.StorageProvider, error) {
		p := newProbeProvider(root)
		p.writeErrPrefix = "core-b"
		p.writeErr = errRO
		return p, nil
	}

	if got := b.SelfCheck(ctx).Status; got != StatusWriteDenied {
		t.Fatalf("core-b status = %s, want write-denied", got)
	}
	if b.Status().Healthy() {
		t.Fatal("core-b did not gate itself on its own failed write")
	}

	for pass := 1; pass <= 2; pass++ {
		res := a.SelfCheck(ctx)
		if res.Status != StatusOK {
			t.Fatalf("core-a pass %d status = %s (missing %v), want ok: a peer that could not write is not expected in storage", pass, res.Status, res.MissingPeers)
		}
		if !a.Status().Healthy() {
			t.Fatalf("core-a gated on pass %d by core-b's fault; one broken host would close the whole fleet's storage routes", pass)
		}
	}

	faults, err := Faults(ctx, rdb)
	if err != nil {
		t.Fatalf("Faults: %v", err)
	}
	if len(faults) != 1 || faults[0].CoreID != "core-b" {
		t.Fatalf("faults = %+v, want exactly one, for the broken core-b", faults)
	}
}

// TestSelfCheck_FakeSharedVolumeStillGatesEveryCore is the load-bearing test:
// the failure the whole feature exists to catch. Three Cores each on a
// PER-HOST volume that only looks shared - every one writes its beacon
// successfully to its own disk and therefore claims a fresh write, and no one
// can see anyone else's file. Every Core must still reach not-shared and gate.
//
// It fails immediately if the claim filter is applied too broadly (for example
// by expecting only peers already SEEN), because then no Core expects anyone
// and the fleet silently reports ok while blobs split across three hosts.
func TestSelfCheck_FakeSharedVolumeStillGatesEveryCore(t *testing.T) {
	ctx := context.Background()
	rdb := newReachTestRedis(t)
	online := []string{"core-a", "core-b", "core-c"}

	svcs := make(map[string]*Service, len(online))
	for _, id := range online {
		svcs[id] = claimingService(t, rdb, id, t.TempDir(), online)
	}

	// Three passes over the whole fleet, with nothing seeded: the claims are
	// the ones the Cores publish themselves. The first pass is the fleet
	// warming up - core-a looks before anyone has claimed - so no verdict is
	// asserted there beyond the grace rule holding.
	var last map[string]CoreResult
	for pass := 1; pass <= 3; pass++ {
		last = make(map[string]CoreResult, len(online))
		for _, id := range online {
			last[id] = svcs[id].SelfCheck(ctx)
		}
		if pass == 1 {
			for _, id := range online {
				if !svcs[id].Status().Healthy() {
					t.Fatalf("%s gated on the very first pass; the grace rule is gone", id)
				}
			}
		}
	}

	for _, id := range online {
		res := last[id]
		if res.Status != StatusNotShared {
			t.Fatalf("%s status = %s, want not-shared: every Core claims a write and none can see another, which is the fake-shared volume", id, res.Status)
		}
		if len(res.MissingPeers) != 2 {
			t.Errorf("%s MissingPeers = %v, want both peers named", id, res.MissingPeers)
		}
		if svcs[id].Status().Healthy() {
			t.Fatalf("%s is still healthy after consecutive not-shared passes; the fake-shared volume is never gated", id)
		}
	}

	faults, err := Faults(ctx, rdb)
	if err != nil {
		t.Fatalf("Faults: %v", err)
	}
	if len(faults) != 3 {
		t.Fatalf("recorded %d faults, want one per Core: %+v", len(faults), faults)
	}
}

func TestSelfCheck_AStaleClaimCountsAsNoClaim(t *testing.T) {
	// A claim written with a longer TTL (an older binary, a hand-set key) must
	// stop counting on time, or a Core that went silent long ago would keep its
	// healthy peers expecting it.
	ctx := context.Background()
	deps := testDeps(t, t.TempDir(), "core-a", []string{"core-a", "core-b"})
	if err := PublishClaim(ctx, deps.Redis, "core-b", time.Now().Add(-2*defaultClaimTTL), time.Hour); err != nil {
		t.Fatalf("PublishClaim: %v", err)
	}
	svc := NewService(deps)

	for pass := 1; pass <= 2; pass++ {
		if got := svc.SelfCheck(ctx).Status; got != StatusOK {
			t.Fatalf("pass %d status = %s, want ok - core-b's claim is older than the TTL", pass, got)
		}
	}
	if !svc.Status().Healthy() {
		t.Fatal("gated on a peer whose claim had already gone stale")
	}
}

func TestFreshClaims_RejectsMissingStaleAndCorruptClaims(t *testing.T) {
	ctx := context.Background()
	rdb := newReachTestRedis(t)
	now := time.Now()

	if err := PublishClaim(ctx, rdb, "fresh", now.Add(-time.Minute), defaultClaimTTL); err != nil {
		t.Fatalf("PublishClaim(fresh): %v", err)
	}
	// A long Redis TTL with an old timestamp: the value's own age is what has
	// to disqualify it, independently of when Redis would drop the key.
	if err := PublishClaim(ctx, rdb, "stale", now.Add(-2*defaultClaimTTL), time.Hour); err != nil {
		t.Fatalf("PublishClaim(stale): %v", err)
	}
	if err := rdb.Set(ctx, claimKey("corrupt"), "not-a-timestamp", time.Hour).Err(); err != nil {
		t.Fatalf("seed corrupt claim: %v", err)
	}

	got, err := FreshClaims(ctx, rdb, []string{"fresh", "stale", "corrupt", "absent"}, now, defaultClaimTTL)
	if err != nil {
		t.Fatalf("FreshClaims: %v", err)
	}
	if len(got) != 1 || !got["fresh"] {
		t.Fatalf("FreshClaims = %v, want only [fresh]", got)
	}
}

func TestSelfCheck_AClaimReadFailureDoesNotProduceAHealthyVerdict(t *testing.T) {
	// A Redis failure while reading claims leaves this Core unable to tell "my
	// storage is fake-shared" from "a peer stopped writing". Ruling anyway
	// would clear a real fault and un-gate a genuinely broken Core on a blip,
	// so the pass must commit nothing at all.
	ctx := context.Background()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(mr.Close)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})

	deps := testDeps(t, t.TempDir(), "core-a", []string{"core-a", "core-b"})
	deps.Redis = rdb
	seedClaim(t, rdb, "core-b")
	svc := NewService(deps)

	svc.SelfCheck(ctx)
	svc.SelfCheck(ctx)
	if svc.Status().Healthy() {
		t.Fatal("setup: expected core-a to be gated after two not-shared passes")
	}

	mr.Server().SetPreHook(func(peer *server.Peer, cmd string, args ...string) bool {
		if cmd == "MGET" {
			peer.WriteError("SIMULATED: claim read failed")
			return true
		}
		return false
	})
	t.Cleanup(func() { mr.Server().SetPreHook(nil) })

	svc.SelfCheck(ctx)

	if svc.Status().Healthy() {
		t.Fatal("a failed claim read un-gated a Core that is still on a fake-shared volume")
	}
	if status, _ := svc.Status().Snapshot(); status != StatusNotShared {
		t.Fatalf("LocalStatus = %s, want the standing not-shared verdict", status)
	}

	mr.Server().SetPreHook(nil)
	faults, err := Faults(ctx, rdb)
	if err != nil {
		t.Fatalf("Faults: %v", err)
	}
	if len(faults) != 1 || faults[0].CoreID != "core-a" || faults[0].Status != StatusNotShared {
		t.Fatalf("faults = %+v, want core-a's not-shared fault still standing", faults)
	}
}

func TestSelfCheck_PublishesItsOwnClaimAfterASuccessfulWriteAndWithdrawsItAfterAFailure(t *testing.T) {
	// The claim is tied to the beacon WRITE, never to the verdict: on a
	// fake-shared volume every Core writes fine and reports not-shared, so
	// withdrawing on a failing verdict would make the whole fleet stop
	// expecting each other and un-gate itself on the next pass.
	ctx := context.Background()
	deps := testDeps(t, t.TempDir(), "core-a", []string{"core-a", "core-b"})
	seedClaim(t, deps.Redis, "core-b")
	svc := NewService(deps)

	if got := svc.SelfCheck(ctx).Status; got != StatusNotShared {
		t.Fatalf("setup: status = %s, want not-shared", got)
	}
	claims, err := FreshClaims(ctx, deps.Redis, []string{"core-a"}, time.Now(), defaultClaimTTL)
	if err != nil {
		t.Fatalf("FreshClaims: %v", err)
	}
	if !claims["core-a"] {
		t.Fatal("core-a published no claim after writing its beacon; its peers would stop expecting it and a fake-shared volume would go undetected")
	}

	deps.NewProvider = func(Config) (storage.StorageProvider, error) {
		return nil, errRO
	}
	svc.deps = deps
	if got := svc.SelfCheck(ctx).Status; got != StatusUnreachable {
		t.Fatalf("status = %s, want unreachable", got)
	}
	claims, err = FreshClaims(ctx, deps.Redis, []string{"core-a"}, time.Now(), defaultClaimTTL)
	if err != nil {
		t.Fatalf("FreshClaims: %v", err)
	}
	if claims["core-a"] {
		t.Fatal("core-a still claims a beacon write it could not make; its peers would gate themselves on its fault")
	}
}

func TestRefreshBeacon_AListFailureIsUnreachableNotNotShared(t *testing.T) {
	// The observer could not LOOK, so it has no evidence about peers. Reading
	// the empty SeenPeers as not-shared would convict this Core of a
	// fake-shared volume on a transient backend failure, and two in a row
	// would gate it.
	prov := newProbeProvider(t.TempDir())
	prov.listErr = errRO

	rep := RefreshBeacon(context.Background(), prov, beaconOpts("core-a", []string{"core-a", "core-b"}, time.Unix(1_000_000, 0)))

	if !rep.Wrote {
		t.Fatal("setup: the beacon write itself should have succeeded")
	}
	if rep.Reachable {
		t.Fatal("Reachable = true although the fleet listing failed; Aggregate would call this not-shared")
	}
	got := Aggregate([]string{"core-a", "core-b"}, map[string]Report{"core-a": rep}, "fp-1", true)
	if st := statusOf(t, got, "core-a").Status; st != StatusUnreachable {
		t.Fatalf("status = %s, want unreachable", st)
	}
}

func TestSelfCheck_APeerOnADifferentBackendIsNotAMissingPeer(t *testing.T) {
	// core-b's beacon is right there on the same root, it just carries a
	// different fingerprint. The storage is demonstrably shared with it, so
	// calling core-a not-shared blames the wrong Core (and gates it) for a
	// config problem that is core-b's.
	ctx := context.Background()
	rdb := newReachTestRedis(t)
	root := t.TempDir()
	online := []string{"core-a", "core-b"}
	seedClaim(t, rdb, "core-b")

	other := beaconOpts("core-b", online, time.Now())
	other.Fingerprint = "fp-OTHER"
	if rep := RefreshBeacon(ctx, newProbeProvider(root), other); !rep.Wrote {
		t.Fatalf("setup: core-b did not write its beacon: %+v", rep)
	}

	a := claimingService(t, rdb, "core-a", root, online)
	for pass := 1; pass <= 2; pass++ {
		if got := a.SelfCheck(ctx).Status; got != StatusOK {
			t.Fatalf("core-a pass %d status = %s, want ok - core-b's beacon was readable, so the storage IS shared", pass, got)
		}
	}
	if !a.Status().Healthy() {
		t.Fatal("core-a gated on a peer that is merely pointed at a differently-named backend")
	}
}
