package services

import (
	"strconv"
	"testing"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

func TestDeriveLeaderPublicKey_MatchesGatewayDerivation(t *testing.T) {
	pub, err := DeriveLeaderPublicKey("cluster-secret", "leader-01")
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	if _, err := wgtypes.ParseKey(pub); err != nil {
		t.Fatalf("derived pub not a valid key: %v", err)
	}
	pub2, _ := DeriveLeaderPublicKey("cluster-secret", "leader-01")
	if pub != pub2 {
		t.Fatal("derivation must be deterministic")
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
