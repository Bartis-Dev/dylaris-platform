package main

import (
	"net"
	"testing"
)

func mustCIDR(t *testing.T, s string) *net.IPNet {
	t.Helper()
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		t.Fatalf("bad CIDR %q: %v", s, err)
	}
	return n
}

func TestCidrsOverlap(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"10.0.0.0/26", "10.0.0.0/26", true},
		{"10.0.0.0/26", "10.0.0.32/27", true}, // b inside a
		{"10.0.0.0/25", "10.0.0.0/26", true},  // b inside a (wider a)
		{"10.0.0.0/26", "10.0.0.64/26", false},
		{"10.0.0.0/26", "172.16.0.0/26", false},
	}
	for _, c := range cases {
		if got := cidrsOverlap(mustCIDR(t, c.a), mustCIDR(t, c.b)); got != c.want {
			t.Errorf("cidrsOverlap(%s,%s) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestParseCIDRsSkipsJunk(t *testing.T) {
	got := parseCIDRs([]string{"10.0.0.0/26", "", "  ", "not-a-cidr", "fd00::/64", "192.168.5.0/24"})
	if len(got) != 2 {
		t.Fatalf("parseCIDRs kept %d nets, want 2 (%v)", len(got), got)
	}
	if got[0].String() != "10.0.0.0/26" || got[1].String() != "192.168.5.0/24" {
		t.Fatalf("parseCIDRs = %v, want [10.0.0.0/26 192.168.5.0/24]", got)
	}
}

func TestCapacityForPrefix(t *testing.T) {
	cases := []struct {
		prefix int
		want   int
	}{
		{26, 60}, // 64 - 4
		{25, 124},
		{24, 252},
	}
	for _, c := range cases {
		if got := capacityForPrefix(c.prefix); got != c.want {
			t.Errorf("capacityForPrefix(%d) = %d, want %d", c.prefix, got, c.want)
		}
	}
}

func TestNextPrefixLen(t *testing.T) {
	if p, ok := nextPrefixLen(26); !ok || p != 25 {
		t.Errorf("nextPrefixLen(26) = %d,%v want 25,true", p, ok)
	}
	if p, ok := nextPrefixLen(25); !ok || p != 24 {
		t.Errorf("nextPrefixLen(25) = %d,%v want 24,true", p, ok)
	}
	if _, ok := nextPrefixLen(24); ok {
		t.Errorf("nextPrefixLen(24) ok = true, want false (already at max block)")
	}
}

func TestServerIPInSubnetStableAndDistinct(t *testing.T) {
	sn := mustCIDR(t, "10.0.0.0/26")
	// node is .2; slot 1 -> .3, slot 60 -> .62.
	if ip := nodeIPInSubnet(sn); ip.String() != "10.0.0.2" {
		t.Fatalf("nodeIPInSubnet = %s, want 10.0.0.2", ip)
	}
	if ip := serverIPInSubnet(sn, 1); ip.String() != "10.0.0.3" {
		t.Fatalf("serverIPInSubnet(slot 1) = %s, want 10.0.0.3", ip)
	}
	if ip := serverIPInSubnet(sn, 60); ip.String() != "10.0.0.62" {
		t.Fatalf("serverIPInSubnet(slot 60) = %s, want 10.0.0.62", ip)
	}
	// Same slot in a shifted subnet (enlarge case) remaps deterministically.
	big := mustCIDR(t, "10.0.1.0/25")
	if ip := serverIPInSubnet(big, 1); ip.String() != "10.0.1.3" {
		t.Fatalf("serverIPInSubnet in /25 slot 1 = %s, want 10.0.1.3", ip)
	}
}

func TestNextFreeSubnetAvoidsOverlaps(t *testing.T) {
	t.Run("empty used -> first block", func(t *testing.T) {
		got, err := nextFreeSubnet(nil, 26)
		if err != nil {
			t.Fatalf("nextFreeSubnet: %v", err)
		}
		if got.String() != "10.0.0.0/26" {
			t.Fatalf("got %s, want 10.0.0.0/26", got)
		}
	})
	t.Run("skips used blocks in order", func(t *testing.T) {
		used := []*net.IPNet{mustCIDR(t, "10.0.0.0/26"), mustCIDR(t, "10.0.0.64/26")}
		got, err := nextFreeSubnet(used, 26)
		if err != nil {
			t.Fatalf("nextFreeSubnet: %v", err)
		}
		if got.String() != "10.0.0.128/26" {
			t.Fatalf("got %s, want 10.0.0.128/26", got)
		}
	})
	t.Run("skips a wide used block", func(t *testing.T) {
		// A used /24 covering 10.0.0.0-10.0.0.255 pushes the next /26 to 10.0.1.0.
		used := []*net.IPNet{mustCIDR(t, "10.0.0.0/24")}
		got, err := nextFreeSubnet(used, 26)
		if err != nil {
			t.Fatalf("nextFreeSubnet: %v", err)
		}
		if got.String() != "10.0.1.0/26" {
			t.Fatalf("got %s, want 10.0.1.0/26", got)
		}
	})
}
