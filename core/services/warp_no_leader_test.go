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
