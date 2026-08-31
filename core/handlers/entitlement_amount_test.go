package handlers

import (
	"testing"

	"dylaris-core/store"
)

// grantAmountStore records what SetUserLimitOverrides was asked to write, which
// is the whole point: the bug this guards against is not a wrong return value,
// it is a write that carries away fields nobody mentioned.
type grantAmountStore struct {
	store.Store
	billing *store.UserBilling
	wrote   struct {
		called                              bool
		maxNodes, maxLinks                  *int64
		trafficEdge, trafficRelay, combined *int64
	}
}

func (f *grantAmountStore) GetUserBilling(string) (*store.UserBilling, error) {
	return f.billing, nil
}

func (f *grantAmountStore) SetUserLimitOverrides(_ string, maxNodes, maxLinks, edge, relay, combined *int64) error {
	f.wrote.called = true
	f.wrote.maxNodes, f.wrote.maxLinks = maxNodes, maxLinks
	f.wrote.trafficEdge, f.wrote.trafficRelay, f.wrote.combined = edge, relay, combined
	return nil
}

func amt(v int64) *int64 { return &v }

// The rule: granting a quantity writes THAT kind's cap and nothing else.
//
// A grant used to hand out access with no quantity at all, and an absent limit
// is no limit - so a granted tenant could enroll without bound until their first
// purchase pushed a real cap, at which point they were retroactively over it and
// on the 72-hour clock, from a screen that never showed them a number.
func TestApplyGrantAmountWritesOnlyTheGrantedKind(t *testing.T) {
	t.Run("byon sets the node cap", func(t *testing.T) {
		fs := &grantAmountStore{billing: &store.UserBilling{MaxLinks: amt(5)}}
		h := &EntitlementHandler{state: &AppState{Store: fs}}
		if err := h.applyGrantAmount("u1", "byon", amt(2)); err != nil {
			t.Fatal(err)
		}
		if fs.wrote.maxNodes == nil || *fs.wrote.maxNodes != 2 {
			t.Errorf("maxNodes = %v, want 2", fs.wrote.maxNodes)
		}
		// The other kind's cap is untouched, not overwritten with the amount.
		if fs.wrote.maxLinks == nil || *fs.wrote.maxLinks != 5 {
			t.Errorf("maxLinks = %v, want the stored 5 - granting BYON changed a route-only cap", fs.wrote.maxLinks)
		}
	})

	t.Run("route_only sets the link cap", func(t *testing.T) {
		fs := &grantAmountStore{billing: &store.UserBilling{MaxNodes: amt(3)}}
		h := &EntitlementHandler{state: &AppState{Store: fs}}
		if err := h.applyGrantAmount("u1", "route_only", amt(1)); err != nil {
			t.Fatal(err)
		}
		if fs.wrote.maxLinks == nil || *fs.wrote.maxLinks != 1 {
			t.Errorf("maxLinks = %v, want 1", fs.wrote.maxLinks)
		}
		if fs.wrote.maxNodes == nil || *fs.wrote.maxNodes != 3 {
			t.Errorf("maxNodes = %v, want the stored 3", fs.wrote.maxNodes)
		}
	})

	t.Run("both sets both", func(t *testing.T) {
		fs := &grantAmountStore{billing: &store.UserBilling{}}
		h := &EntitlementHandler{state: &AppState{Store: fs}}
		if err := h.applyGrantAmount("u1", "both", amt(4)); err != nil {
			t.Fatal(err)
		}
		if fs.wrote.maxNodes == nil || *fs.wrote.maxNodes != 4 || fs.wrote.maxLinks == nil || *fs.wrote.maxLinks != 4 {
			t.Errorf("nodes=%v links=%v, want both 4", fs.wrote.maxNodes, fs.wrote.maxLinks)
		}
	})
}

// The read-modify-write this depends on. The store's setter takes every override
// at once, so passing nil for the ones not being changed would ERASE them -
// turning "give them two nodes" into "and take away the traffic allowance they
// bought".
func TestApplyGrantAmountPreservesTheOtherOverrides(t *testing.T) {
	fs := &grantAmountStore{billing: &store.UserBilling{
		TrafficEdgeGB:     amt(100),
		TrafficRelayGB:    amt(200),
		TrafficCombinedGB: amt(300),
	}}
	h := &EntitlementHandler{state: &AppState{Store: fs}}
	if err := h.applyGrantAmount("u1", "byon", amt(1)); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		got  *int64
		want int64
	}{
		{"trafficEdge", fs.wrote.trafficEdge, 100},
		{"trafficRelay", fs.wrote.trafficRelay, 200},
		{"trafficCombined", fs.wrote.combined, 300},
	} {
		if c.got == nil || *c.got != c.want {
			t.Errorf("%s = %v, want the stored %d - granting a node quantity erased it", c.name, c.got, c.want)
		}
	}
}

// A cap of 0 is a real answer ("they may hold none") and has to reach the store
// as 0 rather than being read as "nothing to write" - the exact confusion the
// pointer convention exists to remove.
func TestApplyGrantAmountWritesACapOfNone(t *testing.T) {
	fs := &grantAmountStore{billing: &store.UserBilling{}}
	h := &EntitlementHandler{state: &AppState{Store: fs}}
	if err := h.applyGrantAmount("u1", "byon", amt(0)); err != nil {
		t.Fatal(err)
	}
	if !fs.wrote.called {
		t.Fatal("a cap of none wrote nothing at all")
	}
	if fs.wrote.maxNodes == nil || *fs.wrote.maxNodes != 0 {
		t.Errorf("maxNodes = %v, want a cap of 0", fs.wrote.maxNodes)
	}
}
