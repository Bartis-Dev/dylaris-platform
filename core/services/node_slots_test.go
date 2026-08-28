package services

import (
	"fmt"
	"testing"
)

// slotFake answers the three counters independently, which is the point: the
// two pending kinds live in different tables and a gate that reads one is blind
// to the other.
type slotFake struct {
	nodes, tokens, keys int
	failOn              string
}

func (f *slotFake) CountNodesByOwner(string) (int, error) {
	if f.failOn == "nodes" {
		return 0, fmt.Errorf("boom")
	}
	return f.nodes, nil
}
func (f *slotFake) CountPendingNodeEnrollTokens(string) (int, error) {
	if f.failOn == "tokens" {
		return 0, fmt.Errorf("boom")
	}
	return f.tokens, nil
}
func (f *slotFake) CountNodeWarpKeysByOwner(string) (int, error) {
	if f.failOn == "keys" {
		return 0, fmt.Errorf("boom")
	}
	return f.keys, nil
}

// The rule this decides: one number, whoever asks.
//
// There are two self-service routes to a node and they store their pending
// identity in different tables. Before this, the enroll mint counted
// nodes+tokens, the warp mint counted nodes+keys and the over-limit sweep
// counted nodes+keys, so no two of the three agreed.
func TestNodeSlotsUsedCountsBothPendingKinds(t *testing.T) {
	cases := []struct {
		name string
		f    slotFake
		want int64
	}{
		{"nothing held", slotFake{}, 0},
		{"live nodes only", slotFake{nodes: 2}, 2},
		{"a pending enroll token is a machine mid-setup", slotFake{tokens: 1}, 1},
		{"an unredeemed warp key is too", slotFake{keys: 1}, 1},
		{"the mix the old gates could not see", slotFake{nodes: 2, tokens: 2, keys: 2}, 6},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := NodeSlotsUsed(&c.f, "u1")
			if err != nil {
				t.Fatalf("NodeSlotsUsed: %v", err)
			}
			if got != c.want {
				t.Errorf("NodeSlotsUsed() = %d, want %d", got, c.want)
			}
		})
	}
}

// A counter that silently reports 0 on a failed query would open the cap wide
// exactly when the database is unhappy. Every caller here treats an error as
// "do not decide", so the error has to travel.
func TestNodeSlotsUsedSurfacesEveryQueryError(t *testing.T) {
	for _, which := range []string{"nodes", "tokens", "keys"} {
		t.Run(which, func(t *testing.T) {
			if _, err := NodeSlotsUsed(&slotFake{nodes: 9, tokens: 9, keys: 9, failOn: which}, "u1"); err == nil {
				t.Errorf("a failing %s count returned no error; the cap would be decided on a wrong number", which)
			}
		})
	}
}

// The defect, stated as the sequence that produced it. A two-node tenant walks
// both mint doors; each door only ever saw its own side.
func TestTheTwoMintGatesShareOneAllowance(t *testing.T) {
	const maxNodes = int64(2)
	f := &slotFake{}

	mint := func(kind string) bool {
		used, err := NodeSlotsUsed(f, "u1")
		if err != nil {
			t.Fatal(err)
		}
		if used >= maxNodes {
			return false
		}
		switch kind {
		case "token":
			f.tokens++
		case "key":
			f.keys++
		}
		return true
	}

	if !mint("token") || !mint("key") {
		t.Fatal("the first two slots must be grantable; the cap is 2")
	}
	if mint("token") {
		t.Error("a third node identity was granted on a cap of 2 (an enroll token after a warp key)")
	}
	if mint("key") {
		t.Error("a third node identity was granted on a cap of 2 (a warp key after an enroll token)")
	}

	// And the sweep must now agree with the doors rather than punish what they
	// allowed: redeeming both leaves exactly the cap, not double it.
	f.tokens, f.keys, f.nodes = 0, 0, 2
	used, _ := NodeSlotsUsed(f, "u1")
	if used > maxNodes {
		t.Errorf("after redeeming both slots the sweep sees %d against a cap of %d, and would cut the tenant off", used, maxNodes)
	}
}
