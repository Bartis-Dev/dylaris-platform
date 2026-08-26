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
// The trap worth keeping in view while reading these: zero means UNLIMITED here,
// and an account that bought nothing also carries zero. That ambiguity is why
// callers must gate on EffectiveEntitlement first and treat this only as the
// ceiling behind it.

type fakeLimitStore struct {
	billing *store.UserBilling
}

func (f *fakeLimitStore) GetUserBilling(string) (*store.UserBilling, error) { return f.billing, nil }

func ptr(v int64) *int64 { return &v }

func TestEffectiveLimits_NoOverrideIsUnlimited(t *testing.T) {
	lim, err := EffectiveLimits(&fakeLimitStore{}, "u1")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if lim != (Limits{}) {
		t.Fatalf("expected all-zero (unlimited), got %+v", lim)
	}
}

// A billing row that exists but sets no caps is the same as no row: an account
// can have a status and a traffic ceiling without any limit override.
func TestEffectiveLimits_BillingRowWithoutCapsIsUnlimited(t *testing.T) {
	st := &fakeLimitStore{billing: &store.UserBilling{Status: "active"}}
	lim, err := EffectiveLimits(st, "u1")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if lim != (Limits{}) {
		t.Fatalf("expected all-zero (unlimited), got %+v", lim)
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
	want := Limits{MaxNodes: 3, MaxLinks: 2, R2QuotaGB: 50, TrafficEdgeGB: 10, TrafficRelayGB: 20, TrafficCombinedGB: 100}
	if lim != want {
		t.Fatalf("got %+v, want %+v", lim, want)
	}
}

// An explicitly stored 0 still means unlimited HERE. Kept as its own case
// because it is the ambiguity the entitlement gate exists to cover: the same
// zero means "bought nothing" one layer up.
func TestEffectiveLimits_ExplicitZeroIsUnlimited(t *testing.T) {
	st := &fakeLimitStore{billing: &store.UserBilling{MaxNodes: ptr(0), TrafficCombinedGB: ptr(0)}}
	lim, err := EffectiveLimits(st, "u1")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if lim.MaxNodes != 0 || lim.TrafficCombinedGB != 0 {
		t.Fatalf("expected zeroes to pass through, got %+v", lim)
	}
}

// Fields the caller did not set must not be invented from the ones it did.
func TestEffectiveLimits_UnsetFieldsStayZero(t *testing.T) {
	st := &fakeLimitStore{billing: &store.UserBilling{MaxNodes: ptr(5)}}
	lim, err := EffectiveLimits(st, "u1")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if lim.MaxNodes != 5 {
		t.Fatalf("MaxNodes = %d, want 5", lim.MaxNodes)
	}
	if lim.MaxLinks != 0 || lim.R2QuotaGB != 0 || lim.TrafficCombinedGB != 0 {
		t.Fatalf("unset fields leaked a value: %+v", lim)
	}
}
