package services

import (
	"context"
	"errors"
	"testing"

	"dylaris-core/store"
)

// A region with no enabled leader used to enroll SUCCESSFULLY and answer with an
// empty endpoint list. The peer got an overlay address and a region, so both
// sides read healthy, while the client refused its own configuration with
// "enroll response carried no leader endpoint" and retried every five seconds
// forever. Nothing in Core's log mentioned it.
func TestEnroll_NoEnabledLeader_Refused(t *testing.T) {
	svc, fs, _ := enrollTestService(t)
	fs.leaders = nil

	key := store.WarpAPIKey{ID: 1, Policy: "general", MaxConns: 5, OnNewConn: "block"}
	if _, err := svc.Enroll(context.Background(), key, "pubA", nil); !errors.Is(err, ErrNoWarpLeader) {
		t.Fatalf("enroll error = %v, want ErrNoWarpLeader", err)
	}
}

// A disabled leader is not an endpoint either: the row exists, but nothing may
// be handed out from it.
func TestEnroll_AllLeadersDisabled_Refused(t *testing.T) {
	svc, fs, _ := enrollTestService(t)
	for i := range fs.leaders {
		fs.leaders[i].Enabled = false
	}

	key := store.WarpAPIKey{ID: 1, Policy: "general", MaxConns: 5, OnNewConn: "block"}
	if _, err := svc.Enroll(context.Background(), key, "pubA", nil); !errors.Is(err, ErrNoWarpLeader) {
		t.Fatalf("enroll error = %v, want ErrNoWarpLeader", err)
	}
}

// The refusal must stay narrow. A leader that is merely DOWN still has an
// address, and assignRegion degrades to such a region on purpose - the tunnel
// comes back when the leader does. Widening this to liveness would refuse every
// peer during a leader restart.
func TestEnroll_LeaderDownButPresent_StillEnrolls(t *testing.T) {
	svc, _, _ := enrollTestService(t)
	// Nothing writes a liveness heartbeat in this test, so the seeded leader is
	// enabled and not alive - exactly the case that must keep working.
	key := store.WarpAPIKey{ID: 1, Policy: "general", MaxConns: 5, OnNewConn: "block"}
	resp, err := svc.Enroll(context.Background(), key, "pubA", nil)
	if err != nil {
		t.Fatalf("enroll: %v", err)
	}
	if len(resp.Endpoints) != 1 || resp.Endpoints[0] != "vpn.example.com:25599" {
		t.Fatalf("endpoints = %v, want the one seeded endpoint", resp.Endpoints)
	}
}

// The idempotent path rebuilds the config from the stored peer and shares
// buildResult, so it has to refuse for the same reason. It is also the path a
// running client takes on every re-enroll, which is where a leader removed
// underneath it would surface.
func TestReenroll_NoEnabledLeader_Refused(t *testing.T) {
	svc, fs, _ := enrollTestService(t)
	key := store.WarpAPIKey{ID: 1, Policy: "general", MaxConns: 5, OnNewConn: "block"}
	if _, err := svc.Enroll(context.Background(), key, "pubA", nil); err != nil {
		t.Fatalf("first enroll: %v", err)
	}

	fs.leaders = nil
	if _, err := svc.Enroll(context.Background(), key, "pubA", nil); !errors.Is(err, ErrNoWarpLeader) {
		t.Fatalf("re-enroll error = %v, want ErrNoWarpLeader", err)
	}
}

// A region with NO leader must not win an assignment just because every real
// region's leader happens to be down.
//
// This is the fresh-install shape: Core seeds a default region, an operator
// runs a leader in a different one, and both are enabled. A leader that is down
// still has an address and works again when it returns; a region with no leader
// row has no address and never will - so a fallback that treats them alike
// parks a peer on the one option that cannot recover, and now that enroll
// refuses such a region outright, parks it there permanently.
func TestAssignRegion_PrefersARegionThatHasALeader(t *testing.T) {
	svc, fs, _ := enrollTestService(t)
	// A SECOND region, distinct from the fixture's "leader-01", enabled, with no
	// leader row of its own and no peers - so a least-loaded tiebreak prefers it
	// and only the has-a-leader test can rule it out.
	fs.regions = append(fs.regions, store.WarpRegion{Region: "seeded-empty", Subnet: "10.0.98.0/24", Enabled: true})
	// And a peer already on the region that HAS the leader, so the empty one is
	// strictly the least loaded. Without that the ranking is a tie and the old
	// code would pick the right region by slice order - a test that passes for
	// a reason the fix has nothing to do with.
	fs.peers["existing"] = store.WarpPeer{Pubkey: "existing", Region: "leader-01", WGIP: "10.0.99.2"}
	// Nothing writes a liveness key in this fixture, so NO region has a live
	// leader: the degradation path is the one under test.

	key := store.WarpAPIKey{ID: 1, Policy: "general", MaxConns: 5, OnNewConn: "block"}
	region, err := svc.assignRegion(context.Background(), key)
	if err != nil {
		t.Fatalf("assignRegion: %v", err)
	}
	if region == "seeded-empty" {
		t.Fatal("assigned to the region with no leader; nothing there can ever answer an enroll")
	}
	if region != "leader-01" {
		t.Errorf("assignRegion() = %q, want the region that has a leader", region)
	}
}

// The same rule, on the branch that skipped it: a key that NAMES a leaderless
// region.
//
// TestAssignRegion_PrefersARegionThatHasALeader covers the placement path. Ten
// lines above it, the stated-preference branch returned the key's region on
// "enabled" alone - so the one check placement applies deliberately was the one
// an explicit preference bypassed. A key gets its region straight from the
// create request with no leader check, and a region outlives its leaders, so the
// pair is easy to reach: production had an enabled region left over from a
// hand-test, with a subnet, zero leaders and zero peers, beside the real one. A
// key naming it would have enrolled "successfully" onto an overlay nothing
// programs.
func TestAssignRegion_IgnoresAStatedPreferenceForALeaderlessRegion(t *testing.T) {
	svc, fs, _ := enrollTestService(t)
	fs.regions = append(fs.regions, store.WarpRegion{Region: "seeded-empty", Subnet: "10.0.98.0/24", Enabled: true})

	key := store.WarpAPIKey{ID: 1, Policy: "general", MaxConns: 5, OnNewConn: "block", Region: "seeded-empty"}
	region, err := svc.assignRegion(context.Background(), key)
	if err != nil {
		t.Fatalf("assignRegion: %v", err)
	}
	if region == "seeded-empty" {
		t.Fatal("honoured a preference for a region with no leader; the peer would sit on an overlay nothing programs")
	}
	if region != "leader-01" {
		t.Errorf("assignRegion() = %q, want the region that has a leader", region)
	}
}

// A stated preference for a region that DOES have a leader is still honoured,
// including while that leader is down - the fixture writes no liveness key, so
// nothing here is alive. Refusing a preference during a restart would move
// peers off their pinned region for no reason.
func TestAssignRegion_HonoursAPreferenceWhoseLeaderIsMerelyDown(t *testing.T) {
	svc, _, _ := enrollTestService(t)

	key := store.WarpAPIKey{ID: 1, Policy: "general", MaxConns: 5, OnNewConn: "block", Region: "leader-01"}
	region, err := svc.assignRegion(context.Background(), key)
	if err != nil {
		t.Fatalf("assignRegion: %v", err)
	}
	if region != "leader-01" {
		t.Errorf("assignRegion() = %q, want the preferred region: its leader has a row and will answer again", region)
	}
}

// When NOTHING has a leader the historical behaviour stands: still assign, and
// let Enroll refuse with ErrNoWarpLeader naming the region. Failing here would
// answer "no region available", which sends an operator to the wrong screen.
func TestAssignRegion_NoRegionHasALeader_StillAssigns(t *testing.T) {
	svc, fs, _ := enrollTestService(t)
	fs.leaders = nil

	key := store.WarpAPIKey{ID: 1, Policy: "general", MaxConns: 5, OnNewConn: "block"}
	region, err := svc.assignRegion(context.Background(), key)
	if err != nil {
		t.Fatalf("assignRegion: %v", err)
	}
	if region != "leader-01" {
		t.Errorf("assignRegion() = %q, want the only enabled region", region)
	}
}
