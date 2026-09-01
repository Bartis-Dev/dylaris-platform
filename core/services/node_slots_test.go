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

// The rule this decides: one number, whoever asks, and its unit is a MACHINE.
//
// There are two credentials behind one machine and they live in different
// tables. Before this, the enroll mint counted nodes+tokens, the warp mint
// counted nodes+keys and the over-limit sweep counted nodes+keys, so no two of
// the three agreed. Summing all three then made every gate agree on a number
// that counted one machine twice.
func TestNodeSlotsCountsMachinesNotCredentials(t *testing.T) {
	cases := []struct {
		name string
		f    slotFake
		want int64
	}{
		{"nothing held", slotFake{}, 0},
		{"live nodes only", slotFake{nodes: 2}, 2},
		{"a pending enroll token is a machine mid-setup", slotFake{tokens: 1}, 1},
		{"an unredeemed warp key is too", slotFake{keys: 1}, 1},
		{"both halves of ONE machine are one machine", slotFake{tokens: 1, keys: 1}, 1},
		{"two machines, one of them only half set up", slotFake{tokens: 1, keys: 2}, 2},
		{"two enrolled and two being set up", slotFake{nodes: 2, tokens: 2, keys: 2}, 4},
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

// The two mint doors share one allowance, and neither door refuses the second
// half of a machine the other already let through.
//
// Both properties at once, because fixing either alone breaks the other: doors
// blind to each other hand out more slots than the cap, and doors that count
// each credential refuse the machine they just started.
func TestTheTwoMintGatesShareOneAllowance(t *testing.T) {
	const maxNodes = int64(2)
	f := &slotFake{}

	// Each door asks what the tenant would hold AFTER its own mint.
	mint := func(kind string) bool {
		s, err := CountNodeSlots(f, "u1")
		if err != nil {
			t.Fatal(err)
		}
		after := s.UsedWithEnrollToken()
		if kind == "key" {
			after = s.UsedWithWarpKey()
		}
		if Exceeds(ptr(maxNodes), after) {
			return false
		}
		if kind == "token" {
			f.tokens++
		} else {
			f.keys++
		}
		return true
	}

	if !mint("key") || !mint("token") {
		t.Fatal("the first machine was refused its own two halves")
	}
	if !mint("key") || !mint("token") {
		t.Fatal("the second machine was refused; the cap is 2")
	}
	if mint("key") {
		t.Error("a third machine was granted on a cap of 2")
	}

	// And the sweep must now agree with the doors rather than punish what they
	// allowed: redeeming both leaves exactly the cap, not double it.
	f.tokens, f.keys, f.nodes = 0, 0, 2
	used, _ := NodeSlotsUsed(f, "u1")
	if used > maxNodes {
		t.Errorf("after redeeming both slots the sweep sees %d against a cap of %d, and would cut the tenant off", used, maxNodes)
	}
}

// The sequence the panel actually runs, on the cap every BYON tenant has.
//
// A BYON machine needs BOTH halves - a warp key to reach the overlay and an
// enroll token to become a node - and the panel mints them back to back for one
// machine the user named once. Counted as two identities they fill a cap of 1
// between them, so the second call always failed and the FIRST machine was
// impossible to add.
func TestTheFirstMachineFitsInACapOfOne(t *testing.T) {
	const maxNodes = int64(1)
	f := &slotFake{}

	if Exceeds(ptr(maxNodes), mustSlots(t, f).UsedWithWarpKey()) {
		t.Fatal("the overlay key of the first machine was refused")
	}
	f.keys++
	if Exceeds(ptr(maxNodes), mustSlots(t, f).UsedWithEnrollToken()) {
		t.Fatal("the enroll token of the SAME machine was refused; no machine can ever be added")
	}
	f.tokens++

	// And a second machine still does not fit.
	if !Exceeds(ptr(maxNodes), mustSlots(t, f).UsedWithWarpKey()) {
		t.Error("a second machine was allowed on a cap of 1")
	}
}

func mustSlots(t *testing.T, f *slotFake) NodeSlots {
	t.Helper()
	s, err := CountNodeSlots(f, "u1")
	if err != nil {
		t.Fatal(err)
	}
	return s
}
