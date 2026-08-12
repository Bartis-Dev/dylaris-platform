package services

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"dylaris-pkg/protocol"
)

// capTestService builds a WarpService backed by a fresh miniredis and the given
// fake store, for the capacity-placement tests.
func capTestService(t *testing.T, fs *fakeWarpStore) (*WarpService, *miniredis.Miniredis) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(mr.Close)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	return &WarpService{warp: fs, redis: rdb, clusterSecret: "cluster-secret"}, mr
}

func setComponentMirror(t *testing.T, mr *miniredis.Miniredis, leaderID, host string) {
	t.Helper()
	b, err := json.Marshal(protocol.GatewayStats{
		Version: protocol.GatewayStatsVersion, Component: "warp", ID: leaderID, Host: host,
	})
	if err != nil {
		t.Fatalf("marshal component mirror: %v", err)
	}
	mr.Set("dylaris:gwbw:component:warp:"+leaderID, string(b))
}

func setHostMirror(t *testing.T, mr *miniredis.Miniredis, host string, budgetMbit int, txBps uint64) {
	t.Helper()
	b, err := json.Marshal(hostAggregate{Host: host, BudgetMbit: budgetMbit, TxBps: txBps})
	if err != nil {
		t.Fatalf("marshal host mirror: %v", err)
	}
	mr.Set("dylaris:gwbw:host:"+host, string(b))
}

func setLeaderAlive(t *testing.T, mr *miniredis.Miniredis, leaderID string) {
	t.Helper()
	mr.Set("dylaris:warp:"+leaderID+":alive", "1")
}

func TestHostFreeBps(t *testing.T) {
	cases := []struct {
		name       string
		budgetMbit int
		txBps      uint64
		wantFree   int64
		wantKnown  bool
	}{
		{"headroom", 1000, 900_000_000, 100_000_000, true},
		{"unset budget is unknown", 0, 500_000_000, 0, false},
		{"negative budget is unknown", -5, 0, 0, false},
		{"over budget clamps to zero", 100, 200_000_000, 0, true},
		{"idle host full budget", 1000, 0, 1_000_000_000, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			free, known := hostFreeBps(hostAggregate{BudgetMbit: c.budgetMbit, TxBps: c.txBps})
			if free != c.wantFree || known != c.wantKnown {
				t.Fatalf("got (%d,%v), want (%d,%v)", free, known, c.wantFree, c.wantKnown)
			}
		})
	}
}

func TestLoadGatewayCapacity(t *testing.T) {
	svc, mr := capTestService(t, newFakeWarpStore())
	setComponentMirror(t, mr, "l1", "hostA")
	setHostMirror(t, mr, "hostA", 1000, 900_000_000) // free 100e6
	// l2 has no component mirror -> unknown; l3 has a host with no budget -> unknown.
	setComponentMirror(t, mr, "l3", "hostB")
	setHostMirror(t, mr, "hostB", 0, 10_000_000)

	gc := svc.loadGatewayCapacity(context.Background(), []string{"l1", "l2", "l3"})

	if got := gc.leaderHost["l1"]; got != "hostA" {
		t.Fatalf("l1 host = %q, want hostA", got)
	}
	if free, known := gc.freeBpsForLeader("l1"); !known || free != 100_000_000 {
		t.Fatalf("l1 free = (%d,%v), want (100000000,true)", free, known)
	}
	if _, known := gc.freeBpsForLeader("l2"); known {
		t.Fatal("l2 has no telemetry, must be unknown")
	}
	if _, known := gc.freeBpsForLeader("l3"); known {
		t.Fatal("l3 host has no budget, must be unknown")
	}
}

func TestLoadGatewayCapacity_EmptyInput(t *testing.T) {
	svc, _ := capTestService(t, newFakeWarpStore())
	gc := svc.loadGatewayCapacity(context.Background(), nil)
	if _, known := gc.freeBpsForLeader("anything"); known {
		t.Fatal("empty input must yield an all-unknown view")
	}
}
