package services

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"dylaris-core/store"
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

func TestOrderLeadersByFreeCapacity(t *testing.T) {
	cases := []struct {
		name  string
		cands []leaderCandidate
		want  []string
	}{
		{
			name: "freest alive leader first",
			cands: []leaderCandidate{
				{endpoint: "e1", id: "l1", alive: true, freeBps: 100_000_000, known: true},
				{endpoint: "e2", id: "l2", alive: true, freeBps: 900_000_000, known: true},
			},
			want: []string{"e2", "e1"},
		},
		{
			name: "known capacity ranks ahead of unknown",
			cands: []leaderCandidate{
				{endpoint: "e3", id: "l3", alive: true},
				{endpoint: "e1", id: "l1", alive: true, freeBps: 100_000_000, known: true},
			},
			want: []string{"e1", "e3"},
		},
		{
			// Locks the known-before-unknown gate: a known leader with ZERO free
			// headroom must still outrank an unknown leader that has a LOWER id.
			// Without the known gate both freeBps==0 would tie and fall to id order,
			// which would wrongly place the lower-id unknown first.
			name: "known zero-headroom ranks ahead of lower-id unknown",
			cands: []leaderCandidate{
				{endpoint: "eUnknown", id: "a-unknown", alive: true},
				{endpoint: "eKnown", id: "z-known", alive: true, freeBps: 0, known: true},
			},
			want: []string{"eKnown", "eUnknown"},
		},
		{
			name: "dead leader sorts last even with capacity",
			cands: []leaderCandidate{
				{endpoint: "e4", id: "l4", alive: false, freeBps: 999_000_000, known: true},
				{endpoint: "e1", id: "l1", alive: true},
			},
			want: []string{"e1", "e4"},
		},
		{
			name: "no telemetry reproduces alive-first-then-id",
			cands: []leaderCandidate{
				{endpoint: "e2", id: "l2", alive: true},
				{endpoint: "e1", id: "l1", alive: true},
				{endpoint: "e0", id: "l0", alive: false},
			},
			want: []string{"e1", "e2", "e0"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := orderLeadersByFreeCapacity(c.cands)
			if len(got) != len(c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("got %v, want %v", got, c.want)
				}
			}
		})
	}
}

func TestRegionEndpoints_OrdersByFreeCapacity(t *testing.T) {
	fs := &fakeWarpStore{
		peers: map[string]store.WarpPeer{},
		byKey: map[int][]store.WarpPeer{},
		regions: []store.WarpRegion{
			{Region: "eu", Subnet: "10.0.99.0/24", Enabled: true},
		},
		leaders: []store.WarpLeader{
			{LeaderID: "l1", Region: "eu", Endpoint: "e1", Enabled: true},
			{LeaderID: "l2", Region: "eu", Endpoint: "e2", Enabled: true},
			{LeaderID: "l3", Region: "eu", Endpoint: "e3", Enabled: true},
		},
	}
	svc, mr := capTestService(t, fs)
	setLeaderAlive(t, mr, "l1")
	setLeaderAlive(t, mr, "l2")
	setLeaderAlive(t, mr, "l3") // l3 alive but no telemetry -> unknown, sorts last
	setComponentMirror(t, mr, "l1", "hostA")
	setComponentMirror(t, mr, "l2", "hostB")
	setHostMirror(t, mr, "hostA", 1000, 900_000_000) // free 100e6
	setHostMirror(t, mr, "hostB", 1000, 100_000_000) // free 900e6

	got := svc.regionEndpoints(context.Background(), "eu")
	want := []string{"e2", "e1", "e3"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestRegionEndpoints_NoTelemetry_IDOrder(t *testing.T) {
	fs := &fakeWarpStore{
		peers: map[string]store.WarpPeer{},
		byKey: map[int][]store.WarpPeer{},
		regions: []store.WarpRegion{
			{Region: "eu", Subnet: "10.0.99.0/24", Enabled: true},
		},
		leaders: []store.WarpLeader{
			{LeaderID: "l2", Region: "eu", Endpoint: "e2", Enabled: true},
			{LeaderID: "l1", Region: "eu", Endpoint: "e1", Enabled: true},
		},
	}
	svc, mr := capTestService(t, fs)
	setLeaderAlive(t, mr, "l1")
	setLeaderAlive(t, mr, "l2")

	got := svc.regionEndpoints(context.Background(), "eu")
	want := []string{"e1", "e2"} // alive-first-then-id, unchanged from pre-F1
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestRegionEndpoints_NoLeaders_ReturnsEmptyNotNil(t *testing.T) {
	fs := &fakeWarpStore{
		peers:   map[string]store.WarpPeer{},
		byKey:   map[int][]store.WarpPeer{},
		regions: []store.WarpRegion{{Region: "eu", Subnet: "10.0.99.0/24", Enabled: true}},
		leaders: nil, // region exists but has no leaders
	}
	svc, _ := capTestService(t, fs)
	got := svc.regionEndpoints(context.Background(), "eu")
	// Pre-F1 returned a non-nil empty slice here; EnrollResult.Endpoints has no
	// omitempty, so this must stay [] (not null) on the wire.
	if got == nil {
		t.Fatal("regionEndpoints for a region with no leaders must return non-nil empty, got nil")
	}
	if len(got) != 0 {
		t.Fatalf("expected empty, got %v", got)
	}
}

func TestRegionFreeBps_DistinctHosts(t *testing.T) {
	gc := gatewayCapacity{
		leaderHost: map[string]string{"l1": "hostA", "l2": "hostA", "l3": "hostB"},
		hostFree:   map[string]int64{"hostA": 100_000_000, "hostB": 900_000_000},
	}
	// l1 and l2 share hostA: its 100e6 counts once, plus hostB's 900e6.
	free, known := regionFreeBps(gc, []string{"l1", "l2", "l3"})
	if !known || free != 1_000_000_000 {
		t.Fatalf("got (%d,%v), want (1000000000,true)", free, known)
	}
	if _, known := regionFreeBps(gc, []string{"unknown-leader"}); known {
		t.Fatal("region with no telemetry must be unknown")
	}
}

func TestPickFreestRegion(t *testing.T) {
	cases := []struct {
		name  string
		cands []regionCandidate
		want  string
	}{
		{
			name: "most free capacity wins",
			cands: []regionCandidate{
				{region: "eu", freeBps: 800_000_000, known: true, peerCount: 0},
				{region: "us", freeBps: 950_000_000, known: true, peerCount: 5},
			},
			want: "us",
		},
		{
			name: "known outranks unknown",
			cands: []regionCandidate{
				{region: "eu", known: false, peerCount: 0},
				{region: "us", freeBps: 1, known: true, peerCount: 9},
			},
			want: "us",
		},
		{
			name: "no telemetry falls back to fewest peers",
			cands: []regionCandidate{
				{region: "us", known: false, peerCount: 5},
				{region: "eu", known: false, peerCount: 0},
			},
			want: "eu",
		},
		{
			name: "equal peers ties by region name",
			cands: []regionCandidate{
				{region: "us", known: false, peerCount: 3},
				{region: "eu", known: false, peerCount: 3},
			},
			want: "eu",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := pickFreestRegion(c.cands); got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}

// twoRegionStore seeds enabled regions eu + us, one leader each, and n peers in
// "us" so peer-count alone would pick eu (0 peers).
func twoRegionStore(usPeers int) *fakeWarpStore {
	fs := &fakeWarpStore{
		peers: map[string]store.WarpPeer{},
		byKey: map[int][]store.WarpPeer{},
		regions: []store.WarpRegion{
			{Region: "eu", Subnet: "10.0.1.0/24", Enabled: true},
			{Region: "us", Subnet: "10.0.2.0/24", Enabled: true},
		},
		leaders: []store.WarpLeader{
			{LeaderID: "eu1", Region: "eu", Endpoint: "eu:1", Enabled: true},
			{LeaderID: "us1", Region: "us", Endpoint: "us:1", Enabled: true},
		},
	}
	for i := 0; i < usPeers; i++ {
		pk := "uspeer-" + strconv.Itoa(i)
		fs.peers[pk] = store.WarpPeer{Pubkey: pk, Region: "us"}
	}
	return fs
}

func TestAssignRegion_PrefersFreestRegion(t *testing.T) {
	svc, mr := capTestService(t, twoRegionStore(5))
	setLeaderAlive(t, mr, "eu1")
	setLeaderAlive(t, mr, "us1")
	setComponentMirror(t, mr, "eu1", "hostEU")
	setComponentMirror(t, mr, "us1", "hostUS")
	setHostMirror(t, mr, "hostEU", 1000, 200_000_000) // free 800e6
	setHostMirror(t, mr, "hostUS", 1000, 50_000_000)  // free 950e6

	got, err := svc.assignRegion(context.Background(), store.WarpAPIKey{ID: 1})
	if err != nil {
		t.Fatalf("assignRegion: %v", err)
	}
	if got != "us" {
		t.Fatalf("got %q, want us (freest despite more peers)", got)
	}
}

func TestAssignRegion_PinnedKeyWins(t *testing.T) {
	svc, mr := capTestService(t, twoRegionStore(5))
	setLeaderAlive(t, mr, "eu1")
	setLeaderAlive(t, mr, "us1")
	setComponentMirror(t, mr, "us1", "hostUS")
	setHostMirror(t, mr, "hostUS", 1000, 0) // us wildly free, but the pin must win

	got, err := svc.assignRegion(context.Background(), store.WarpAPIKey{ID: 1, Region: "eu"})
	if err != nil {
		t.Fatalf("assignRegion: %v", err)
	}
	if got != "eu" {
		t.Fatalf("got %q, want eu (pinned)", got)
	}
}

func TestAssignRegion_NoTelemetry_FewestPeers(t *testing.T) {
	svc, mr := capTestService(t, twoRegionStore(5))
	setLeaderAlive(t, mr, "eu1")
	setLeaderAlive(t, mr, "us1")
	// no mirrors set -> both regions unknown

	got, err := svc.assignRegion(context.Background(), store.WarpAPIKey{ID: 1})
	if err != nil {
		t.Fatalf("assignRegion: %v", err)
	}
	if got != "eu" {
		t.Fatalf("got %q, want eu (fewest peers, pre-F1 behaviour)", got)
	}
}
