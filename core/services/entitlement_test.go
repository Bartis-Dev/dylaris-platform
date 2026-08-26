package services

import (
	"testing"
	"time"

	"dylaris-core/store"
)

// entFakeStore serves the lookups EffectiveEntitlement makes. The plan lookups
// remain on the interface for now but are no longer consulted: entitlement comes
// from what the store pushed, plus manual grants.
type entFakeStore struct {
	billing *store.UserBilling
}

func (f *entFakeStore) GetUserBilling(string) (*store.UserBilling, error) { return f.billing, nil }

func ptrInt64(i int64) *int64        { return &i }
func ptrTime(t time.Time) *time.Time { return &t }

var entNow = time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)

// The rule these decide: a hosted tenant may use exactly what they bought, and
// a self-host install may use everything.
//
// The bug they pin is the one that made this function worth rewriting. It used
// to answer "unlimited" whenever no plan was configured, unconditionally. On a
// hosted install an account that has bought nothing configures nothing, so every
// freshly registered user resolved to unlimited and could mint node enroll
// tokens, warp keys and link kits. The one state meaning "paid for nothing" was
// read as "entitled to everything".
func TestEffectiveEntitlement_Hosted(t *testing.T) {
	tests := []struct {
		name          string
		fake          *entFakeStore
		wantByon      bool
		wantRouteOnly bool
		wantSource    string
	}{
		{
			// The headline case. A registered account that never touched the
			// store has no billing row at all.
			name:          "nothing bought grants nothing",
			fake:          &entFakeStore{},
			wantByon:      false,
			wantRouteOnly: false,
			wantSource:    EntitlementSourceNone,
		},
		{
			// A cancellation pushes zeroes rather than deleting the row. A bare
			// numeric cap check reads 0 as "no limit"; here it must read as
			// "nothing".
			name: "an explicit zero grants nothing",
			fake: &entFakeStore{billing: &store.UserBilling{
				Status: "active", MaxNodes: ptrInt64(0), MaxLinks: ptrInt64(0),
			}},
			wantByon:      false,
			wantRouteOnly: false,
			wantSource:    EntitlementSourceNone,
		},
		{
			name: "a purchased node grants byon only",
			fake: &entFakeStore{billing: &store.UserBilling{
				Status: "active", MaxNodes: ptrInt64(1),
			}},
			wantByon:      true,
			wantRouteOnly: false,
			wantSource:    EntitlementSourcePlan,
		},
		{
			name: "a purchased route-only location grants routes only",
			fake: &entFakeStore{billing: &store.UserBilling{
				Status: "active", MaxLinks: ptrInt64(1),
			}},
			wantByon:      false,
			wantRouteOnly: true,
			wantSource:    EntitlementSourcePlan,
		},
		{
			name: "both purchased grants both",
			fake: &entFakeStore{billing: &store.UserBilling{
				Status: "active", MaxNodes: ptrInt64(1), MaxLinks: ptrInt64(1),
			}},
			wantByon:      true,
			wantRouteOnly: true,
			wantSource:    EntitlementSourcePlan,
		},
		{
			// Suspension is a hard stop whatever they hold.
			name: "suspended grants nothing even with a purchase",
			fake: &entFakeStore{billing: &store.UserBilling{
				Status: "suspended", MaxNodes: ptrInt64(5), MaxLinks: ptrInt64(5),
			}},
			wantByon:      false,
			wantRouteOnly: false,
			wantSource:    EntitlementSourceSuspended,
		},
		{
			// past_due is what the grace window is for; cutting access there
			// would defeat it.
			name: "past_due still grants",
			fake: &entFakeStore{billing: &store.UserBilling{
				Status: "past_due", MaxNodes: ptrInt64(1), MaxLinks: ptrInt64(1),
			}},
			wantByon:      true,
			wantRouteOnly: true,
			wantSource:    EntitlementSourcePlan,
		},
		{
			name: "an active grant alone grants its kind",
			fake: &entFakeStore{billing: &store.UserBilling{
				Status:                     "active",
				ManualEntitlement:          EntitlementByon,
				ManualEntitlementExpiresAt: ptrTime(entNow.Add(24 * time.Hour)),
			}},
			wantByon:      true,
			wantRouteOnly: false,
			wantSource:    EntitlementSourceGrant,
		},
		{
			// Union, deliberately: "here are 14 days now, subscribe later" must
			// not have the grant and the subscription fight.
			name: "a grant and a purchase union",
			fake: &entFakeStore{billing: &store.UserBilling{
				Status:                     "active",
				MaxLinks:                   ptrInt64(1),
				ManualEntitlement:          EntitlementByon,
				ManualEntitlementExpiresAt: ptrTime(entNow.Add(24 * time.Hour)),
			}},
			wantByon:      true,
			wantRouteOnly: true,
			wantSource:    EntitlementSourceBoth,
		},
		{
			// Fail CLOSED on a kind nobody recognises.
			name: "an unknown grant kind grants nothing",
			fake: &entFakeStore{billing: &store.UserBilling{
				Status:                     "active",
				ManualEntitlement:          "premium",
				ManualEntitlementExpiresAt: ptrTime(entNow.Add(24 * time.Hour)),
			}},
			wantByon:      false,
			wantRouteOnly: false,
			wantSource:    EntitlementSourceNone,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := EffectiveEntitlement(tt.fake, "u1", entNow, true)
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

// The rule this decides: a self-host install, which has no store and therefore
// nothing to buy, keeps the uncapped behaviour it has always had. Denying there
// would lock out every install that never configured any of this.
func TestEffectiveEntitlement_SelfHostIsUnlimited(t *testing.T) {
	cases := []struct {
		name string
		fake *entFakeStore
	}{
		{"no billing row at all", &entFakeStore{}},
		{"a billing row with nothing set", &entFakeStore{billing: &store.UserBilling{Status: "active"}}},
		{"explicit zeroes", &entFakeStore{billing: &store.UserBilling{
			Status: "active", MaxNodes: ptrInt64(0), MaxLinks: ptrInt64(0),
		}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := EffectiveEntitlement(tc.fake, "u1", entNow, false)
			if err != nil {
				t.Fatalf("EffectiveEntitlement: %v", err)
			}
			if !got.Byon || !got.RouteOnly {
				t.Errorf("byon=%v routeOnly=%v, want both true on a self-host install", got.Byon, got.RouteOnly)
			}
			if got.Source != EntitlementSourceUnlimited {
				t.Errorf("Source = %q, want %q", got.Source, EntitlementSourceUnlimited)
			}
		})
	}
}

// Suspension outranks the self-host shortcut. An operator who suspended an
// account on a self-host install means it.
func TestEffectiveEntitlement_SelfHostStillHonoursSuspension(t *testing.T) {
	fake := &entFakeStore{billing: &store.UserBilling{Status: "suspended"}}
	got, err := EffectiveEntitlement(fake, "u1", entNow, false)
	if err != nil {
		t.Fatalf("EffectiveEntitlement: %v", err)
	}
	if got.Byon || got.RouteOnly {
		t.Errorf("a suspended account got byon=%v routeOnly=%v", got.Byon, got.RouteOnly)
	}
}

// An expired grant must not appear in the response at all, or the admin UI shows
// "granted until <past date>" as if it were live.
func TestEffectiveEntitlement_ExpiredGrantIsNotReported(t *testing.T) {
	fake := &entFakeStore{
		billing: &store.UserBilling{
			Status:                     "active",
			ManualEntitlement:          EntitlementByon,
			ManualEntitlementExpiresAt: ptrTime(entNow.Add(-24 * time.Hour)),
		},
	}
	got, err := EffectiveEntitlement(fake, "u1", entNow, true)
	if err != nil {
		t.Fatalf("EffectiveEntitlement: %v", err)
	}
	if got.GrantKind != "" || got.GrantExpiresAt != nil {
		t.Errorf("expired grant reported as %q until %v, want it absent", got.GrantKind, got.GrantExpiresAt)
	}
	if got.Byon {
		t.Error("an expired grant still granted byon")
	}
}

// The expiry is exclusive, so the exact instant it lapses is already lapsed.
// Pinned because an off-by-one here means a grant that outlives its own date.
func TestEffectiveEntitlement_GrantBoundary(t *testing.T) {
	mk := func(exp time.Time) *entFakeStore {
		return &entFakeStore{
			billing: &store.UserBilling{
				Status:                     "active",
				ManualEntitlement:          EntitlementByon,
				ManualEntitlementExpiresAt: ptrTime(exp),
			},
		}
	}
	if got, _ := EffectiveEntitlement(mk(entNow), "u1", entNow, true); got.Byon {
		t.Error("a grant expiring exactly now still granted")
	}
	if got, _ := EffectiveEntitlement(mk(entNow.Add(time.Nanosecond)), "u1", entNow, true); !got.Byon {
		t.Error("a grant expiring a moment from now did not grant")
	}
}
