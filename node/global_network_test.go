package main

import (
	"net"
	"testing"
)

func TestWg0RoutedSubnets(t *testing.T) {
	// /proc/net/route stores Destination + Mask as little-endian hex.
	// wg0 -> 10.0.0.0/16: Destination=0000000A, Mask=0000FFFF
	// wg0 -> 10.77.0.0/16: Destination=00004D0A, Mask=0000FFFF
	table := "Iface\tDestination\tGateway\tFlags\tRefCnt\tUse\tMetric\tMask\tMTU\tWindow\tIRTT\n" +
		"wg0\t0000000A\t00000000\t0001\t0\t0\t0\t0000FFFF\t0\t0\t0\n" +
		"wg0\t00004D0A\t00000000\t0001\t0\t0\t0\t0000FFFF\t0\t0\t0\n" +
		"eth0\t00000000\t0100000A\t0003\t0\t0\t0\t00000000\t0\t0\t0\n"
	nets := wg0RoutedSubnets(table)
	if len(nets) != 2 {
		t.Fatalf("wg0RoutedSubnets len = %d, want 2 (%v)", len(nets), nets)
	}
	if nets[0].String() != "10.0.0.0/16" || nets[1].String() != "10.77.0.0/16" {
		t.Fatalf("wg0RoutedSubnets = %v, want [10.0.0.0/16 10.77.0.0/16]", nets)
	}
}

func TestWg0RoutedSubnetsIgnoresGarbage(t *testing.T) {
	if got := wg0RoutedSubnets(""); len(got) != 0 {
		t.Fatalf("empty table -> %v, want none", got)
	}
	if got := wg0RoutedSubnets("wg0 short line\nnonsense"); len(got) != 0 {
		t.Fatalf("garbage -> %v, want none", got)
	}
}

// A full-tunnel default route (0.0.0.0/0 via wg0) must be skipped: treating it as
// a reserved subnet would make every candidate overlap and collapse avoidance.
func TestWg0RoutedSubnetsSkipsDefaultRoute(t *testing.T) {
	table := "Iface\tDestination\tGateway\tFlags\tRefCnt\tUse\tMetric\tMask\tMTU\tWindow\tIRTT\n" +
		"wg0\t00000000\t00000000\t0001\t0\t0\t0\t00000000\t0\t0\t0\n" +
		"wg0\t0000000A\t00000000\t0001\t0\t0\t0\t0000FFFF\t0\t0\t0\n"
	nets := wg0RoutedSubnets(table)
	if len(nets) != 1 || nets[0].String() != "10.0.0.0/16" {
		t.Fatalf("wg0RoutedSubnets = %v, want just [10.0.0.0/16] (default route skipped)", nets)
	}
}

// On a host-net node the chosen local subnet must avoid every Docker subnet, the
// wg0 tunnel ranges, AND all of 10/8 (warp's home) - so nextFreeSubnet lands in
// 172.16/12.
func TestReservedSubnetsHostNetPushesOutOf10Slash8(t *testing.T) {
	docker := []*net.IPNet{mustCIDR(t, "172.17.0.0/16")}
	table := "Iface\tDestination\tGateway\tFlags\tRefCnt\tUse\tMetric\tMask\tMTU\tWindow\tIRTT\n" +
		"wg0\t0000000A\t00000000\t0001\t0\t0\t0\t0000FFFF\t0\t0\t0\n"
	used := reservedSubnets(docker, table, true)

	free, err := nextFreeSubnet(used, 24)
	if err != nil {
		t.Fatalf("nextFreeSubnet: %v", err)
	}
	// Must be a private /24 that overlaps nothing reserved, and NOT inside 10/8.
	if ten := mustCIDR(t, "10.0.0.0/8"); cidrsOverlap(free, ten) {
		t.Fatalf("host-net pick %s is inside 10/8 (warp home)", free)
	}
	for _, u := range used {
		if cidrsOverlap(free, u) {
			t.Fatalf("pick %s overlaps reserved %s", free, u)
		}
	}
}

// Off a host-net node, 10/8 is NOT reserved wholesale - only the specific wg0
// ranges and Docker subnets are avoided.
func TestReservedSubnetsNonHostNetKeeps10Slash8(t *testing.T) {
	used := reservedSubnets(nil, "", false)
	if len(used) != 0 {
		t.Fatalf("non-host-net with no docker nets / no routes -> %v, want none reserved", used)
	}
}
