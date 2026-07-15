package main

import (
	"net"
	"testing"
	"time"
)

func TestProbeBeamLANReturnsReachable(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer lis.Close()
	_, port, err := net.SplitHostPort(lis.Addr().String())
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	got := probeBeamLAN([]string{"127.0.0.1"}, port)
	want := net.JoinHostPort("127.0.0.1", port)
	if got != want {
		t.Errorf("probeBeamLAN() = %q, want %q", got, want)
	}
}

func TestProbeBeamLANEmptyPort(t *testing.T) {
	// An empty port must fail safe (no LAN match, so the connect chain falls back to the
	// relay) rather than probe a hard-coded fallback port.
	if got := probeBeamLAN([]string{"127.0.0.1"}, ""); got != "" {
		t.Errorf("probeBeamLAN(_, \"\") = %q, want \"\"", got)
	}
}

func TestProbeBeamLANParallelBudget(t *testing.T) {
	// RFC 5737 TEST-NET-1 addresses are unrouted; a dial to them stalls until the
	// per-dial 700ms timeout. Serially, three would cost ~2.1s; concurrently they
	// share one budget. A loose ceiling well under the serial worst case proves the
	// fan-out without asserting an exact duration (timing stays flake-tolerant).
	ips := []string{"192.0.2.1", "192.0.2.2", "192.0.2.3"}
	start := time.Now()
	got := probeBeamLAN(ips, "25523")
	elapsed := time.Since(start)
	if got != "" {
		t.Errorf("probeBeamLAN() = %q, want \"\" (all unreachable)", got)
	}
	if elapsed > 1500*time.Millisecond {
		t.Errorf("probeBeamLAN took %v, want < 1.5s (serial would be ~2.1s)", elapsed)
	}
}
