package services

import (
	"dylaris-pkg/protocol"
	"testing"
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
