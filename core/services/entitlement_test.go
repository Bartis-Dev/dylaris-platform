package services

import (
	"testing"
	"time"

	"dylaris-core/store"
)

// entFakeStore serves the four lookups EffectiveEntitlement makes.
type entFakeStore struct {
	billing *store.UserBilling
	planID  *int
	plan    *store.Plan
	def     *store.Plan
}

func (f *entFakeStore) GetUserBilling(string) (*store.UserBilling, error) { return f.billing, nil }
func (f *entFakeStore) GetUserPlanID(string) (*int, error)                { return f.planID, nil }
func (f *entFakeStore) GetPlan(int) (*store.Plan, error)                  { return f.plan, nil }
func (f *entFakeStore) GetDefaultPlan() (*store.Plan, error)              { return f.def, nil }

func ptrInt(i int) *int              { return &i }
func ptrTime(t time.Time) *time.Time { return &t }

var entNow = time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)

func TestEffectiveEntitlement(t *testing.T) {
	tests := []struct {
		name          string
		fake          *entFakeStore
		wantByon      bool
		wantRouteOnly bool
		wantSource    string
	}{
		{
			// Today's behaviour for every self-host install that never defined
			// plans. Denying here would lock them out on upgrade.
			name:          "no plan and no billing row is unlimited",
			fake:          &entFakeStore{},
			wantByon:      true,
			wantRouteOnly: true,
			wantSource:    EntitlementSourceUnlimited,
		},
		{
			name:          "plan kind both grants both",
			fake:          &entFakeStore{planID: ptrInt(1), plan: &store.Plan{ID: 1, Kind: EntitlementBoth}},
			wantByon:      true,
			wantRouteOnly: true,
			wantSource:    EntitlementSourcePlan,
		},
		{
			name:          "plan kind byon grants only byon",
			fake:          &entFakeStore{planID: ptrInt(1), plan: &store.Plan{ID: 1, Kind: EntitlementByon}},
			wantByon:      true,
			wantRouteOnly: false,
			wantSource:    EntitlementSourcePlan,
		},
		{
			name:          "plan kind route_only grants only routes",
			fake:          &entFakeStore{planID: ptrInt(1), plan: &store.Plan{ID: 1, Kind: EntitlementRouteOnly}},
			wantByon:      false,
			wantRouteOnly: true,
			wantSource:    EntitlementSourcePlan,
		},
		{
			// Fail CLOSED on a value nobody recognises. A typo in a plan row must
			// not silently hand out everything.
			name:          "unknown plan kind grants nothing",
			fake:          &entFakeStore{planID: ptrInt(1), plan: &store.Plan{ID: 1, Kind: "premium"}},
			wantByon:      false,
			wantRouteOnly: false,
			wantSource:    EntitlementSourceNone,
		},
		{
			name: "active grant alone grants its kind",
			fake: &entFakeStore{
				planID: ptrInt(1), plan: &store.Plan{ID: 1, Kind: "premium"}, // grants nothing
				billing: &store.UserBilling{
					Status:                     "active",
					ManualEntitlement:          EntitlementByon,
					ManualEntitlementExpiresAt: ptrTime(entNow.Add(14 * 24 * time.Hour)),
				},
			},
			wantByon:      true,
			wantRouteOnly: false,
			wantSource:    EntitlementSourceGrant,
		},
		{
			// The point of the whole design: a grant and a subscription add up
			// rather than fight, so "14 days now, subscribe later" works.
			name: "grant and plan are a union",
			fake: &entFakeStore{
				planID: ptrInt(1), plan: &store.Plan{ID: 1, Kind: EntitlementRouteOnly},
				billing: &store.UserBilling{
					Status:                     "active",
					ManualEntitlement:          EntitlementByon,
					ManualEntitlementExpiresAt: ptrTime(entNow.Add(time.Hour)),
				},
			},
			wantByon:      true,
			wantRouteOnly: true,
			wantSource:    EntitlementSourceBoth,
		},
		{
			name: "expired grant contributes nothing",
			fake: &entFakeStore{
				planID: ptrInt(1), plan: &store.Plan{ID: 1, Kind: "premium"},
				billing: &store.UserBilling{
					Status:                     "active",
					ManualEntitlement:          EntitlementBoth,
					ManualEntitlementExpiresAt: ptrTime(entNow.Add(-time.Second)),
				},
			},
			wantByon:      false,
			wantRouteOnly: false,
			wantSource:    EntitlementSourceNone,
		},
		{
			// A kind with no expiry is a malformed row, not an unlimited grant.
			name: "grant with no expiry contributes nothing",
			fake: &entFakeStore{
				planID: ptrInt(1), plan: &store.Plan{ID: 1, Kind: "premium"},
				billing: &store.UserBilling{
					Status:            "active",
					ManualEntitlement: EntitlementBoth,
				},
			},
			wantByon:      false,
			wantRouteOnly: false,
			wantSource:    EntitlementSourceNone,
		},
		{
			// Suspension is a hard stop and outranks everything, including a live
			// grant. Restoring access is a status change, not a grant.
			name: "suspended overrides an active grant",
			fake: &entFakeStore{
				planID: ptrInt(1), plan: &store.Plan{ID: 1, Kind: EntitlementBoth},
				billing: &store.UserBilling{
					Status:                     "suspended",
					ManualEntitlement:          EntitlementBoth,
					ManualEntitlementExpiresAt: ptrTime(entNow.Add(time.Hour)),
				},
			},
			wantByon:      false,
			wantRouteOnly: false,
			wantSource:    EntitlementSourceSuspended,
		},
		{
			// past_due is what the grace window is for; cutting access there would
			// defeat it.
			name: "past_due still grants",
			fake: &entFakeStore{
				planID: ptrInt(1), plan: &store.Plan{ID: 1, Kind: EntitlementBoth},
				billing: &store.UserBilling{Status: "past_due"},
			},
			wantByon:      true,
			wantRouteOnly: true,
			wantSource:    EntitlementSourcePlan,
		},
		{
			name: "a deleted plan falls back to the default plan",
			fake: &entFakeStore{
				planID: ptrInt(99), plan: nil,
				def: &store.Plan{ID: 1, Kind: EntitlementRouteOnly},
			},
			wantByon:      false,
			wantRouteOnly: true,
			wantSource:    EntitlementSourcePlan,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := EffectiveEntitlement(tt.fake, "u1", entNow)
			if err != nil {
				t.Fatalf("EffectiveEntitlement: %v", err)
			}
			if got.Byon != tt.wantByon {
				t.Errorf("Byon = %v, want %v", got.Byon, tt.wantByon)
			}
			if got.RouteOnly != tt.wantRouteOnly {
				t.Errorf("RouteOnly = %v, want %v", got.RouteOnly, tt.wantRouteOnly)
			}
			if got.Source != tt.wantSource {
				t.Errorf("Source = %q, want %q", got.Source, tt.wantSource)
			}
		})
	}
}

// TestEffectiveEntitlement_ExpiredGrantIsNotReported: an expired grant must not
// appear in the response at all, or the admin UI would show "granted until
// <past date>" as if it were live.
func TestEffectiveEntitlement_ExpiredGrantIsNotReported(t *testing.T) {
	fake := &entFakeStore{
		billing: &store.UserBilling{
			Status:                     "active",
			ManualEntitlement:          EntitlementByon,
			ManualEntitlementExpiresAt: ptrTime(entNow.Add(-24 * time.Hour)),
		},
	}
	got, err := EffectiveEntitlement(fake, "u1", entNow)
	if err != nil {
		t.Fatalf("EffectiveEntitlement: %v", err)
	}
	if got.GrantKind != "" || got.GrantExpiresAt != nil {
		t.Errorf("expired grant reported as %q until %v, want it absent", got.GrantKind, got.GrantExpiresAt)
	}
}

// TestEffectiveEntitlement_GrantBoundary: the expiry is exclusive, so the exact
// instant it lapses is already lapsed. Pinned because an off-by-one here means a
// grant that outlives its own date.
func TestEffectiveEntitlement_GrantBoundary(t *testing.T) {
	mk := func(exp time.Time) *entFakeStore {
		return &entFakeStore{
			planID: ptrInt(1), plan: &store.Plan{ID: 1, Kind: "premium"},
			billing: &store.UserBilling{
				Status:                     "active",
				ManualEntitlement:          EntitlementByon,
				ManualEntitlementExpiresAt: ptrTime(exp),
			},
		}
	}
	if got, _ := EffectiveEntitlement(mk(entNow), "u1", entNow); got.Byon {
		t.Error("a grant expiring exactly now still granted")
	}
	if got, _ := EffectiveEntitlement(mk(entNow.Add(time.Nanosecond)), "u1", entNow); !got.Byon {
		t.Error("a grant expiring a moment from now did not grant")
	}
}
