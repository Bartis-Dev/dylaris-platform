package services

import (
	"testing"

	"dylaris-core/store"
)

type fakeLimitStore struct {
	billing *store.UserBilling
	planID  *int
	plans   map[int]*store.Plan
	def     *store.Plan
}

func (f *fakeLimitStore) GetUserBilling(string) (*store.UserBilling, error) { return f.billing, nil }
func (f *fakeLimitStore) GetUserPlanID(string) (*int, error)                { return f.planID, nil }
func (f *fakeLimitStore) GetPlan(id int) (*store.Plan, error) {
	if p, ok := f.plans[id]; ok {
		return p, nil
	}
	return nil, store.ErrWarpLimitReached // any error -> resolver falls back to default
}
func (f *fakeLimitStore) GetDefaultPlan() (*store.Plan, error) { return f.def, nil }

func ptr(v int64) *int64 { return &v }
func iptr(v int) *int    { return &v }

func TestEffectiveLimits_NoPlanNoOverride_AllUnlimited(t *testing.T) {
	lim, err := EffectiveLimits(&fakeLimitStore{}, "u1")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if lim != (Limits{}) {
		t.Fatalf("expected all-zero (unlimited), got %+v", lim)
	}
}

func TestEffectiveLimits_DefaultPlanUsed(t *testing.T) {
	st := &fakeLimitStore{def: &store.Plan{MaxNodes: 3, R2QuotaGB: 50, TrafficCombinedGB: 100}}
	lim, _ := EffectiveLimits(st, "u1")
	if lim.MaxNodes != 3 || lim.R2QuotaGB != 50 || lim.TrafficCombinedGB != 100 {
		t.Fatalf("default plan not applied: %+v", lim)
	}
}

func TestEffectiveLimits_AssignedPlanBeatsDefault(t *testing.T) {
	st := &fakeLimitStore{
		planID: iptr(7),
		plans:  map[int]*store.Plan{7: {MaxNodes: 10}},
		def:    &store.Plan{MaxNodes: 3},
	}
	lim, _ := EffectiveLimits(st, "u1")
	if lim.MaxNodes != 10 {
		t.Fatalf("assigned plan should win over default: %+v", lim)
	}
}

func TestEffectiveLimits_DeletedPlanFallsBackToDefault(t *testing.T) {
	st := &fakeLimitStore{
		planID: iptr(99), // not in plans -> GetPlan errors
		plans:  map[int]*store.Plan{},
		def:    &store.Plan{MaxNodes: 3},
	}
	lim, _ := EffectiveLimits(st, "u1")
	if lim.MaxNodes != 3 {
		t.Fatalf("deleted plan should fall back to default: %+v", lim)
	}
}

func TestEffectiveLimits_UserOverrideWins(t *testing.T) {
	st := &fakeLimitStore{
		def:     &store.Plan{MaxNodes: 3, TrafficCombinedGB: 100},
		billing: &store.UserBilling{MaxNodes: ptr(20), TrafficCombinedGB: ptr(0)},
	}
	lim, _ := EffectiveLimits(st, "u1")
	if lim.MaxNodes != 20 {
		t.Fatalf("override should win: %+v", lim)
	}
	if lim.TrafficCombinedGB != 0 {
		t.Fatalf("explicit 0 override (unlimited) should win over plan 100: %+v", lim)
	}
}
