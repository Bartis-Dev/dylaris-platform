package services

import (
	"dylaris-pkg/protocol"
	"testing"
	"time"
)

func TestAggregateByHost(t *testing.T) {
	latest := map[string]protocol.GatewayStats{
		"edge-eu-1": {Component: "edge", ID: "edge-eu-1", Host: "web-eu-1", RxBps: 1000, TxBps: 2000, CapMbit: 10000},
		"beam-eu-1": {Component: "beam", ID: "beam-eu-1", Host: "web-eu-1", RxBps: 500, TxBps: 700, CapMbit: 10000},
		"warp-eu-1": {Component: "warp", ID: "warp-eu-1", Host: "web-eu-2", RxBps: 300, TxBps: 400, CapMbit: 1000},
		"nohost":    {Component: "edge", ID: "x", Host: "", RxBps: 9, TxBps: 9, CapMbit: 5},
	}
	got := aggregateByHost(latest)

	if len(got) != 2 {
		t.Fatalf("hosts = %d, want 2 (empty host dropped): %+v", len(got), got)
	}
	h1 := got["web-eu-1"]
	if h1.RxBps != 1500 || h1.TxBps != 2700 {
		t.Errorf("web-eu-1 sum = rx %d tx %d, want 1500/2700", h1.RxBps, h1.TxBps)
	}
	if h1.BudgetMbit != 10000 {
		t.Errorf("web-eu-1 budget = %d, want 10000 (max of co-located caps)", h1.BudgetMbit)
	}
	if h1.CapMismatch {
		t.Error("web-eu-1 caps agree (10000/10000), CapMismatch should be false")
	}
}

func TestAggregateByHostCapMismatch(t *testing.T) {
	latest := map[string]protocol.GatewayStats{
		"a": {Host: "h", RxBps: 1, CapMbit: 10000},
		"b": {Host: "h", RxBps: 1, CapMbit: 1000}, // disagrees
		"c": {Host: "h", RxBps: 1, CapMbit: 0},    // unset: ignored for budget + mismatch
	}
	got := aggregateByHost(latest)["h"]
	if got.BudgetMbit != 10000 {
		t.Errorf("budget = %d, want 10000 (max)", got.BudgetMbit)
	}
	if !got.CapMismatch {
		t.Error("differing non-zero caps must set CapMismatch")
	}
}

func TestOnlyThroughputCarryingComponentsReachTheBandwidthView(t *testing.T) {
	// The splice and the link publish to the same telemetry stream but carry no
	// throughput of their own: the splice shares a host and a network namespace
	// with an edge that already reports every byte, and the link ships without a
	// system monitor. Letting them through would put a component into the
	// bandwidth view showing a permanent 0 bps, which a reader cannot tell from
	// an outage - and would add rows of zeros to the history table.
	for _, c := range []string{"edge", "warp", "beam"} {
		if !carriesThroughput(c) {
			t.Errorf("%s reports throughput and was excluded from the bandwidth view", c)
		}
	}
	for _, c := range []string{"splice", "link", "", "something-new"} {
		if carriesThroughput(c) {
			t.Errorf("%q was let into the bandwidth view and reports no throughput", c)
		}
	}
}

func TestSnapshotDropsAComponentThatStoppedPublishing(t *testing.T) {
	// The same staleness bound persistOnce applies. Without it a dead edge keeps
	// contributing its last reading to every average, so an outage reads as a
	// quiet period - which is the opposite of what happened.
	s := NewGatewayBandwidthConsumerService(nil, nil, "core-1")
	s.latest["edge:live"] = seenStat{
		gs:   protocol.GatewayStats{Component: "edge", ID: "live"},
		seen: time.Now(),
	}
	s.latest["edge:gone"] = seenStat{
		gs:   protocol.GatewayStats{Component: "edge", ID: "gone"},
		seen: time.Now().Add(-2 * gwbwStaleAfter),
	}

	got := s.Snapshot()
	if len(got) != 1 || got[0].ID != "live" {
		t.Fatalf("snapshot = %+v, want only the live component", got)
	}
}

func TestSnapshotIsACopyRatherThanTheLiveMap(t *testing.T) {
	// The consumer goroutines write into latest while the collector reads. A
	// snapshot that shared the map would be a data race on every sample.
	s := NewGatewayBandwidthConsumerService(nil, nil, "core-1")
	s.latest["edge:a"] = seenStat{gs: protocol.GatewayStats{Component: "edge", ID: "a"}, seen: time.Now()}
	got := s.Snapshot()
	got[0].ID = "mutated"
	if s.latest["edge:a"].gs.ID != "a" {
		t.Fatal("mutating the snapshot changed the consumer's own state")
	}
}
