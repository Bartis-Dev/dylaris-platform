package services

import (
	"testing"

	"dylaris-core/store"
)

// The rule these decide: a tenant's caps are the per-user overrides and nothing
// else. Plans used to sit underneath as a baseline; they are gone, because the
// hosted store never sold one (it pushes a node COUNT) and self-hosters were
// never meant to hand out tariffs of their own.
//
// The convention these pin is the platform's: nil is no cap, 0 is a real "none",
// n is the cap. It used to be that 0 meant UNLIMITED here - and 0 is also what
// an account that bought nothing carries, so the one number meaning "none" was
// the number that switched the check off. The pointer removes the ambiguity
// rather than documenting it.

type fakeLimitStore struct {
	billing *store.UserBilling
}

func (f *fakeLimitStore) GetUserBilling(string) (*store.UserBilling, error) { return f.billing, nil }

func ptr(v int64) *int64 { return &v }

func TestEffectiveLimits_NoOverrideIsNoCap(t *testing.T) {
	lim, err := EffectiveLimits(&fakeLimitStore{}, "u1")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if lim != (Limits{}) {
		t.Fatalf("expected every cap nil (no limit), got %+v", lim)
	}
}

// A billing row that exists but sets no caps is the same as no row: an account
// can have a status and a traffic ceiling without any limit override.
func TestEffectiveLimits_BillingRowWithoutCapsIsNoCap(t *testing.T) {
	st := &fakeLimitStore{billing: &store.UserBilling{Status: "active"}}
	lim, err := EffectiveLimits(st, "u1")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if lim != (Limits{}) {
		t.Fatalf("expected every cap nil (no limit), got %+v", lim)
	}
}

func TestEffectiveLimits_OverridesApplied(t *testing.T) {
	st := &fakeLimitStore{billing: &store.UserBilling{
		MaxNodes:          ptr(3),
		MaxLinks:          ptr(2),
		R2QuotaGB:         ptr(50),
		TrafficEdgeGB:     ptr(10),
		TrafficRelayGB:    ptr(20),
		TrafficCombinedGB: ptr(100),
	}}
	lim, err := EffectiveLimits(st, "u1")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	for _, c := range []struct {
		name string
		got  *int64
		want int64
	}{
		{"MaxNodes", lim.MaxNodes, 3},
		{"MaxLinks", lim.MaxLinks, 2},
		{"R2QuotaGB", lim.R2QuotaGB, 50},
		{"TrafficEdgeGB", lim.TrafficEdgeGB, 10},
		{"TrafficRelayGB", lim.TrafficRelayGB, 20},
		{"TrafficCombinedGB", lim.TrafficCombinedGB, 100},
	} {
		if c.got == nil || *c.got != c.want {
			t.Errorf("%s = %v, want %d", c.name, c.got, c.want)
		}
	}
}

// The case the old convention got backwards. A stored 0 is a cap of none, and it
// survives the resolve as a cap rather than becoming "no cap".
func TestEffectiveLimits_ExplicitZeroIsACapOfNone(t *testing.T) {
	st := &fakeLimitStore{billing: &store.UserBilling{MaxNodes: ptr(0), TrafficCombinedGB: ptr(0)}}
	lim, err := EffectiveLimits(st, "u1")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if lim.MaxNodes == nil || *lim.MaxNodes != 0 {
		t.Errorf("MaxNodes = %v, want a cap of 0 - a zero that becomes nil is the defect this convention removes", lim.MaxNodes)
	}
	if !AtOrOver(lim.MaxNodes, 0) {
		t.Error("a cap of none did not block the first node")
	}
	if lim.TrafficCombinedGB == nil || *lim.TrafficCombinedGB != 0 {
		t.Errorf("TrafficCombinedGB = %v, want a cap of 0", lim.TrafficCombinedGB)
	}
}

// Fields the caller did not set must not be invented from the ones it did.
func TestEffectiveLimits_UnsetFieldsStayNil(t *testing.T) {
	st := &fakeLimitStore{billing: &store.UserBilling{MaxNodes: ptr(5)}}
	lim, err := EffectiveLimits(st, "u1")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if lim.MaxNodes == nil || *lim.MaxNodes != 5 {
		t.Fatalf("MaxNodes = %v, want 5", lim.MaxNodes)
	}
	if lim.MaxLinks != nil || lim.R2QuotaGB != nil || lim.TrafficCombinedGB != nil {
		t.Fatalf("unset fields leaked a value: %+v", lim)
	}
}

// The three helpers are where the nil check lives, so no caller has to remember
// it. Their whole job is that a 0 cap behaves like any other number.
func TestLimitHelpers(t *testing.T) {
	cases := []struct {
		name              string
		cap               *int64
		used              int64
		exceeds, atOrOver bool
	}{
		{"no cap never blocks", nil, 1_000_000, false, false},
		{"under the cap", ptr(5), 3, false, false},
		{"at the cap: room for none more", ptr(5), 5, false, true},
		{"over the cap", ptr(5), 6, true, true},
		{"a cap of none blocks the first", ptr(0), 0, false, true},
		{"a cap of none is exceeded by one", ptr(0), 1, true, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Exceeds(c.cap, c.used); got != c.exceeds {
				t.Errorf("Exceeds = %v, want %v", got, c.exceeds)
			}
			if got := AtOrOver(c.cap, c.used); got != c.atOrOver {
				t.Errorf("AtOrOver = %v, want %v", got, c.atOrOver)
			}
		})
	}
	if LimitOr(nil, -1) != -1 {
		t.Error("LimitOr did not fall back for a nil cap")
	}
	if LimitOr(ptr(0), -1) != 0 {
		t.Error("LimitOr returned the fallback for a real cap of 0")
	}
}
