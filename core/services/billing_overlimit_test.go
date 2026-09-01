package services

import (
	"context"
	"strings"
	"testing"
	"time"

	"dylaris-core/models"
	"dylaris-core/store"
)

// overLimitStore is a narrow fake: the sweep reads counts and caps, and writes a
// single timestamp. Everything the CUTOFF touches is left nil on purpose - a
// tenant with no servers and no warp peers exercises the decision without
// needing a queue, a registry or a gateway.
type overLimitStore struct {
	store.Store

	billing []store.UserBilling
	plan    *store.Plan

	nodes, keys, kits int
	enrollTokens      int
	// A POINTER so the zero value still means "this fake has no route-limit row",
	// which is what every test that does not mention routes relies on. With a
	// plain int that default would now read as a real cap of 0 and route-cap
	// every one of them.
	routeLimit *int

	stamps   []overLimitStamp
	stampErr error
}

func (f *overLimitStore) ListUserBilling() ([]store.UserBilling, error) { return f.billing, nil }

// The sweep counts node slots through services.NodeSlotsUsed, which asks for
// pending ENROLL TOKENS as well as warp keys. This fake carried only the keys,
// which is precisely the blindness the shared counter exists to remove.
func (f *overLimitStore) CountPendingNodeEnrollTokens(string) (int, error) {
	return f.enrollTokens, nil
}
func (f *overLimitStore) SetUserOverLimitSince(userID string, at *time.Time) error {
	f.stamps = append(f.stamps, overLimitStamp{userID, at})
	return f.stampErr
}
func (f *overLimitStore) GetUserBilling(id string) (*store.UserBilling, error) {
	for i := range f.billing {
		if f.billing[i].UserID == id {
			return &f.billing[i], nil
		}
	}
	return nil, nil
}
func (f *overLimitStore) GetUserPlanID(string) (*int, error)   { return nil, nil }
func (f *overLimitStore) GetPlan(int) (*store.Plan, error)     { return f.plan, nil }
func (f *overLimitStore) GetDefaultPlan() (*store.Plan, error) { return f.plan, nil }

func (f *overLimitStore) CountNodesByOwner(string) (int, error)        { return f.nodes, nil }
func (f *overLimitStore) CountNodeWarpKeysByOwner(string) (int, error) { return f.keys, nil }
func (f *overLimitStore) CountLinkKitsByOwner(string) (int, error)     { return f.kits, nil }
func (f *overLimitStore) GetGatewayRouteLimit(scope string) (*models.GatewayRouteLimit, error) {
	if f.routeLimit == nil {
		return nil, nil
	}
	return &models.GatewayRouteLimit{Scope: scope, MaxRoutes: f.routeLimit}, nil
}

// No user means no address, so sendOverLimitEmail returns before it needs a
// mailer. The decision is what these tests are about; the mail body has its own.
func (f *overLimitStore) GetUserByID(string) (*models.User, error) { return nil, nil }

func (f *overLimitStore) ListServersByOwner(string) ([]models.Server, error) { return nil, nil }
func (f *overLimitStore) ListWarpAPIKeysByOwner(string) ([]store.WarpAPIKey, error) {
	return nil, nil
}

func overLimitSvc(fs *overLimitStore) *BillingLifecycleService {
	return &BillingLifecycleService{store: fs}
}

// billingRow carries the caps as well as the clock now: EffectiveLimits reads
// the per-user overrides and plans no longer exist. maxNodes/maxLinks of 0 would
// mean unlimited, so every case that wants a cap has to set one here.
func billingRow(id string, since *time.Time, status string) store.UserBilling {
	maxNodes, maxLinks := int64(2), int64(1)
	return store.UserBilling{
		UserID: id, Status: status, OverLimitSince: since,
		MaxNodes: &maxNodes, MaxLinks: &maxLinks,
	}
}

// TestEnforceEntitlementLimits_Stamping pins WHEN the clock starts and stops.
// Getting this wrong in either direction is expensive: a clock that never starts
// means a downgrade is free forever, and one that never clears means a tenant who
// fixed the problem is still cut off three days later.
func TestEnforceEntitlementLimits_Stamping(t *testing.T) {
	t.Run("going over starts the clock", func(t *testing.T) {
		fs := &overLimitStore{
			billing: []store.UserBilling{billingRow("u1", nil, "active")},
			nodes:   5,
		}
		overLimitSvc(fs).enforceEntitlementLimits(context.Background())
		if len(fs.stamps) != 1 || fs.stamps[0].at == nil {
			t.Fatalf("stamps = %+v, want one non-nil stamp", fs.stamps)
		}
	})

	t.Run("already stamped and still over does not restart the clock", func(t *testing.T) {
		since := time.Now().Add(-time.Hour)
		fs := &overLimitStore{
			billing: []store.UserBilling{billingRow("u1", &since, "active")},
			nodes:   5,
		}
		overLimitSvc(fs).enforceEntitlementLimits(context.Background())
		if len(fs.stamps) != 0 {
			t.Fatalf("stamps = %+v, want none: restarting would make the grace endless", fs.stamps)
		}
	})

	t.Run("getting back under clears the clock", func(t *testing.T) {
		since := time.Now().Add(-time.Hour)
		fs := &overLimitStore{
			billing: []store.UserBilling{billingRow("u1", &since, "active")},
			nodes:   2,
		}
		overLimitSvc(fs).enforceEntitlementLimits(context.Background())
		if len(fs.stamps) != 1 || fs.stamps[0].at != nil {
			t.Fatalf("stamps = %+v, want one nil stamp (cleared)", fs.stamps)
		}
	})

	t.Run("within limits and never stamped writes nothing", func(t *testing.T) {
		fs := &overLimitStore{
			billing: []store.UserBilling{billingRow("u1", nil, "active")},
			nodes:   1,
		}
		overLimitSvc(fs).enforceEntitlementLimits(context.Background())
		if len(fs.stamps) != 0 {
			t.Fatalf("stamps = %+v, want none", fs.stamps)
		}
	})

	// A suspended tenant is already stopped by the payment path. Warning them
	// about a second deadline for services that are not running is noise.
	t.Run("a suspended tenant is skipped entirely", func(t *testing.T) {
		fs := &overLimitStore{
			billing: []store.UserBilling{billingRow("u1", nil, "suspended")},
			nodes:   9,
		}
		overLimitSvc(fs).enforceEntitlementLimits(context.Background())
		if len(fs.stamps) != 0 {
			t.Fatalf("stamps = %+v, want none", fs.stamps)
		}
	})
}

// TestTenantUsage_Over pins which dimension trips the sweep, and that 0 keeps
// meaning UNLIMITED - the same convention the mint gates use. A 0 read as "none
// allowed" would cut off every tenant on an uncapped platform.
//
// Which is why the entitlement is a separate field. The caps cannot say "this
// tenant may hold nothing", because the value that would mean it is the value
// that means "no cap". Every case below that is about a CAP therefore sets
// entitled, and the ones about the entitlement set the caps to zero.
func TestTenantUsage_Over(t *testing.T) {
	n := func(v int64) *int64 { return &v }
	tests := []struct {
		name string
		u    tenantUsage
		want bool
	}{
		{"within every limit", tenantUsage{entitledByon: true, entitledRoute: true, nodes: 2, nodeLimit: n(2), links: 1, linkLimit: n(1)}, false},
		{"one node too many", tenantUsage{entitledByon: true, entitledRoute: true, nodes: 3, nodeLimit: n(2)}, true},
		{"one location too many", tenantUsage{entitledByon: true, entitledRoute: true, links: 2, linkLimit: n(1)}, true},
		{"one address too many", tenantUsage{entitledByon: true, entitledRoute: true, routes: 7, routeLimit: n(6)}, true},

		// These three used to read "a zero X limit is unlimited", and that was
		// the defect, not the design: a zero is what arithmetic produces for a
		// tenant who bought none of something, so the one number meaning "none"
		// was the one number that switched the check off. Unlimited is now the
		// ABSENCE of a cap, which a nil pointer says and no number can.
		{"a zero node limit means none, so holding any is over", tenantUsage{entitledByon: true, entitledRoute: true, nodes: 99, nodeLimit: n(0)}, true},
		{"a zero link limit means none", tenantUsage{entitledByon: true, entitledRoute: true, links: 99, linkLimit: n(0)}, true},
		{"a zero route limit means none", tenantUsage{entitledByon: true, entitledRoute: true, routes: 99, routeLimit: n(0)}, true},

		// ...and holding nothing against a zero cap is still fine. A cap of none
		// is met by holding none; it is not a permanent fault.
		{"a zero limit with nothing held is not over", tenantUsage{entitledByon: true, entitledRoute: true, nodeLimit: n(0), linkLimit: n(0), routeLimit: n(0)}, false},

		{"no cap anywhere is unlimited", tenantUsage{entitledByon: true, entitledRoute: true, nodes: 99, links: 99, routes: 99}, false},
		{"nothing held anywhere", tenantUsage{entitledByon: true, entitledRoute: true}, false},

		// The hole this closes. A cancellation pushes maxNodes 0, and 0 is
		// "no cap", so a tenant who cancelled EVERYTHING read as permanently
		// within their limits: the entitlement gate stopped them creating
		// anything new and nothing ever took away what they already had.
		// Downgrading five nodes to one was enforced; downgrading to none
		// was not.
		{"no entitlement but still holding nodes", tenantUsage{entitledByon: false, entitledRoute: false, nodes: 3}, true},
		{"no entitlement but still holding locations", tenantUsage{entitledByon: false, entitledRoute: false, links: 1}, true},
		{"no entitlement but still holding addresses", tenantUsage{entitledByon: false, entitledRoute: false, routes: 1}, true},

		// And the boundary that keeps it from being a blanket cutoff: holding
		// nothing is not over anything, so an account that simply never bought
		// in is never warned or swept.
		{"no entitlement and holding nothing", tenantUsage{entitledByon: false, entitledRoute: false}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.u.over(); got != tt.want {
				t.Fatalf("over() = %v, want %v for %+v", got, tt.want, tt.u)
			}
		})
	}
}

// TestTenantUsage_Describe proves the warning names only what is actually wrong.
// A tenant one node over should not read a message implying everything is.
func TestTenantUsage_Describe(t *testing.T) {
	n := func(v int64) *int64 { return &v }
	one := tenantUsage{entitledByon: true, entitledRoute: true, nodes: 3, nodeLimit: n(2)}
	if got := one.describe(); got != "3 nodes (allowed 2)" {
		t.Fatalf("describe() = %q", got)
	}
	both := tenantUsage{entitledByon: true, entitledRoute: true, nodes: 3, nodeLimit: n(2), routes: 7, routeLimit: n(6)}
	if got := both.describe(); got != "3 nodes (allowed 2) and 7 protected addresses (allowed 6)" {
		t.Fatalf("describe() = %q", got)
	}
	// No plan at all is ONE fact, not three dimensions. Rendering it as
	// "3 nodes (allowed 0)" would read as a cap somebody lowered, when what
	// happened is that the subscription ended.
	none := tenantUsage{entitledByon: false, entitledRoute: false, nodes: 3, links: 1}
	want := "3 nodes, 1 route-only locations and 0 protected addresses with no active plan"
	if got := none.describe(); got != want {
		t.Fatalf("describe() = %q, want %q", got, want)
	}
}

// A lapsed grant on ONE product, while another is paid for.
//
// The doors ask per kind - requireEntitlement resolves EntitlementByon for a
// node and EntitlementRouteOnly for a link kit - but this sweep asked whether
// the tenant was entitled to EITHER. A tenant whose comp BYON grant expired
// while they kept paying for route-only therefore read as entitled, and the
// numeric arms could not catch them either: grantedCap returns nil for a lapsed
// grant with nothing purchased, and nil is "no cap". They could not mint a
// second machine, and nobody ever took away the first.
func TestALapsedGrantOnOneProductIsCaughtWhileTheOtherIsPaid(t *testing.T) {
	n := func(v int64) *int64 { return &v }
	cases := []struct {
		name string
		u    tenantUsage
		want bool
		why  string
	}{
		{
			name: "BYON grant lapsed, route-only still paid, node still running",
			// nodeLimit is nil on purpose: that is what a lapsed grant leaves.
			u:    tenantUsage{entitledByon: false, entitledRoute: true, nodes: 1, links: 1, linkLimit: n(1)},
			want: true,
			why:  "the granted machine keeps running for free for as long as they pay for anything else",
		},
		{
			name: "route-only lapsed, BYON still paid, location still up",
			u:    tenantUsage{entitledByon: true, entitledRoute: false, nodes: 1, nodeLimit: n(1), links: 1},
			want: true,
			why:  "same defect, the other way round",
		},
		{
			name: "BYON lapsed and no node held is not over",
			u:    tenantUsage{entitledByon: false, entitledRoute: true, links: 1, linkLimit: n(1)},
			want: false,
			why:  "an entitlement they are not using is not an overage",
		},
		{
			name: "addresses survive on either entitlement",
			// Route creation gates on the allowance, not on a kind, so a BYON
			// tenant holding addresses is entitled to them.
			u:    tenantUsage{entitledByon: true, entitledRoute: false, routes: 3},
			want: false,
			why:  "addresses come out of one pool that either product feeds",
		},
		{
			name: "addresses with neither entitlement are over",
			u:    tenantUsage{entitledByon: false, entitledRoute: false, routes: 3},
			want: true,
			why:  "nothing is paying for the pool they came out of",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.u.over(); got != c.want {
				t.Errorf("over() = %v, want %v: %s", got, c.want, c.why)
			}
		})
	}
}

// The email and the log must name a lapsed product as a lapsed product. There
// is no cap to quote for one - a lapsed grant leaves the ceiling at nil - so
// the numeric wording would have printed a nil pointer or, worse, nothing.
func TestDescribeNamesALapsedProductRatherThanACap(t *testing.T) {
	n := func(v int64) *int64 { return &v }
	u := tenantUsage{entitledByon: false, entitledRoute: true, nodes: 2, links: 1, linkLimit: n(1)}
	got := u.describe()
	if !strings.Contains(got, "2 nodes with no active bring-your-own-node plan") {
		t.Errorf("describe() = %q, expected it to name the lapsed product", got)
	}
	if strings.Contains(got, "allowed") {
		t.Errorf("describe() = %q, quoted an allowance for a product that has none", got)
	}
}
