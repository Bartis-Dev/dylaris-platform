package services

import (
	"context"
	"strconv"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

// The name of this test is the invariant, so it has to actually check it.
//
// It used to assert only that the result was a valid key and that two calls
// agreed - both of which stay true if the salt changes, if the HKDF arguments
// are reordered, or if the KDF is swapped outright. Every one of those silently
// re-keys every region: clients pin the leader's public key, so tunnels build
// and never authenticate, and nothing names the derivation as the cause.
//
// The vectors are the ones pinned in the gateway's own
// pkg/warpkey/warpkey_test.go, whose comment says they "stand in for Core, which
// reimplements the same construction in another repo and has no way to be
// compiled against this one". They only stand in for Core if Core asserts them.
func TestDeriveLeaderPublicKey_MatchesGatewayDerivation(t *testing.T) {
	cases := []struct{ secret, region, wantPub string }{
		{"cluster-secret", "leader-01", "VOHaju2R/VZjJycyWx41LGYpiRdtNDowsCsaKrO+pTo="},
		{"dylaris-test-secret", "eu-central", "kGfkAg9XyAUdfmFh3n/5wdIgBSlhOp9EAiCK3qOEqB4="},
	}
	for _, c := range cases {
		pub, err := DeriveLeaderPublicKey(c.secret, c.region)
		if err != nil {
			t.Fatalf("derive(%q, %q): %v", c.secret, c.region, err)
		}
		if _, err := wgtypes.ParseKey(pub); err != nil {
			t.Fatalf("derived pub not a valid key: %v", err)
		}
		if pub != c.wantPub {
			t.Errorf("derive(%q, %q) = %q, want %q - Core and the gateway no longer derive the same leader key, "+
				"so every client's pinned pubkey is wrong and every region is effectively re-keyed",
				c.secret, c.region, pub, c.wantPub)
		}
	}
}

func TestNextFreeIP_SkipsLeaderAndTaken(t *testing.T) {
	ip, err := NextFreeIP("10.0.99.0/24", map[string]bool{
		"10.0.99.5": true, "10.0.99.6": true,
	})
	if err != nil {
		t.Fatalf("alloc: %v", err)
	}
	if ip != "10.0.99.2" {
		t.Fatalf("got %s, want 10.0.99.2 (.1 reserved, .5/.6 taken)", ip)
	}
}

func TestNextFreeIP_Exhausted(t *testing.T) {
	taken := map[string]bool{}
	for i := 2; i <= 254; i++ {
		taken["10.0.99."+strconv.Itoa(i)] = true
	}
	if _, err := NextFreeIP("10.0.99.0/24", taken); err == nil {
		t.Fatal("expected exhaustion error")
	}
}

func TestWarpRegionSubnetKeyFormat(t *testing.T) {
	// Frozen cross-repo contract: the gateway warp leader reads this exact key.
	if got := warpRegionSubnetKey("eu-central"); got != "dylaris:warp:region:eu-central:subnet" {
		t.Fatalf("key contract drift: %q", got)
	}
}

func TestPublishRegionSubnetWritesKey(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(mr.Close)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})

	if err := PublishRegionSubnet(context.Background(), rdb, "eu-central", "10.0.99.0/24"); err != nil {
		t.Fatalf("publish: %v", err)
	}
	got, err := mr.Get("dylaris:warp:region:eu-central:subnet")
	if err != nil || got != "10.0.99.0/24" {
		t.Fatalf("mirror = %q, %v; want 10.0.99.0/24", got, err)
	}
}
