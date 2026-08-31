package services

import (
	"testing"
	"time"

	"dylaris-core/store"
)

// The rule this decides: BYON and route-only are TWO grants, each with its own
// deadline, and granting one never ends the other.
//
// They used to be one string plus one clock. "both" existed as a value, so the
// state was reachable, but only by choosing it up front - granting route-only to
// a tenant who already had BYON overwrote the BYON grant. Reported from BYON
// testing as "it just switches between the two", which is precisely what one
// column can do.
func TestManualGrantsAreIndependent(t *testing.T) {
	soon := entNow.Add(24 * time.Hour)
	later := entNow.Add(90 * 24 * time.Hour)

	t.Run("both can be held at once, with different deadlines", func(t *testing.T) {
		st := &entFakeStore{billing: &store.UserBilling{
			ManualByonExpiresAt:  ptrTime(soon),
			ManualRouteExpiresAt: ptrTime(later),
		}}
		got, err := EffectiveEntitlement(st, "u1", entNow, true, false)
		if err != nil {
			t.Fatal(err)
		}
		if !got.Byon || !got.RouteOnly {
			t.Fatalf("got byon=%v routeOnly=%v, want both", got.Byon, got.RouteOnly)
		}
		if got.GrantKind != EntitlementBoth {
			t.Errorf("GrantKind = %q, want %q", got.GrantKind, EntitlementBoth)
		}
		// The summary date is the LATER one: it answers "when does this tenant
		// stop being entitled to anything", not "when does the first half lapse".
		if got.GrantExpiresAt == nil || !got.GrantExpiresAt.Equal(later) {
			t.Errorf("GrantExpiresAt = %v, want the later deadline %v", got.GrantExpiresAt, later)
		}
	})

	// The whole point of separate clocks: one runs out, the other keeps running.
	t.Run("one expiring leaves the other standing", func(t *testing.T) {
		st := &entFakeStore{billing: &store.UserBilling{
			ManualByonExpiresAt:  ptrTime(entNow.Add(-time.Hour)),
			ManualRouteExpiresAt: ptrTime(later),
		}}
		got, _ := EffectiveEntitlement(st, "u1", entNow, true, false)
		if got.Byon {
			t.Error("an expired BYON grant still counted")
		}
		if !got.RouteOnly {
			t.Error("the route-only grant was dropped along with the expired BYON one")
		}
		if got.GrantKind != EntitlementRouteOnly {
			t.Errorf("GrantKind = %q, want %q", got.GrantKind, EntitlementRouteOnly)
		}
	})

	t.Run("neither granted is no grant at all", func(t *testing.T) {
		st := &entFakeStore{billing: &store.UserBilling{}}
		got, _ := EffectiveEntitlement(st, "u1", entNow, true, false)
		if got.Byon || got.RouteOnly || got.GrantKind != "" {
			t.Errorf("got %+v, want nothing granted", got)
		}
	})
}

// A row written before the split has only the old string and the old expiry. It
// must keep working: the migration backfills, but a Core rolled back mid-upgrade
// would otherwise read every existing grant as revoked and cut tenants off.
func TestLegacyGrantShapeStillResolves(t *testing.T) {
	future := entNow.Add(48 * time.Hour)

	for _, tc := range []struct {
		kind            string
		byon, routeOnly bool
	}{
		{EntitlementByon, true, false},
		{EntitlementRouteOnly, false, true},
		{EntitlementBoth, true, true},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			st := &entFakeStore{billing: &store.UserBilling{
				ManualEntitlement:          tc.kind,
				ManualEntitlementExpiresAt: ptrTime(future),
			}}
			got, _ := EffectiveEntitlement(st, "u1", entNow, true, false)
			if got.Byon != tc.byon || got.RouteOnly != tc.routeOnly {
				t.Errorf("got byon=%v routeOnly=%v, want %v/%v", got.Byon, got.RouteOnly, tc.byon, tc.routeOnly)
			}
		})
	}

	// And the fallback must not resurrect a kind that the per-kind columns have
	// deliberately cleared. Once either new column is set, the row has been
	// written by the new code and the legacy string is stale by definition.
	t.Run("a revoked kind is not restored by the stale string", func(t *testing.T) {
		st := &entFakeStore{billing: &store.UserBilling{
			ManualEntitlement:          EntitlementBoth,
			ManualEntitlementExpiresAt: ptrTime(future),
			ManualRouteExpiresAt:       ptrTime(future),
			// BYON was revoked: its column is nil while the old string still says "both".
		}}
		got, _ := EffectiveEntitlement(st, "u1", entNow, true, false)
		if got.Byon {
			t.Error("the stale legacy string brought a revoked BYON grant back")
		}
		if !got.RouteOnly {
			t.Error("the surviving route-only grant was lost")
		}
	})
}
