package services

import (
	"context"
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
	routeLimit        int

	stamps   []overLimitStamp
	stampErr error
}

func (f *overLimitStore) ListUserBilling() ([]store.UserBilling, error) { return f.billing, nil }
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
	if f.routeLimit == 0 {
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
func TestTenantUsage_Over(t *testing.T) {
	tests := []struct {
		name string
		u    tenantUsage
		want bool
	}{
		{"within every limit", tenantUsage{nodes: 2, nodeLimit: 2, links: 1, linkLimit: 1}, false},
		{"one node too many", tenantUsage{nodes: 3, nodeLimit: 2}, true},
		{"one location too many", tenantUsage{links: 2, linkLimit: 1}, true},
		{"one address too many", tenantUsage{routes: 7, routeLimit: 6}, true},
		{"a zero node limit is unlimited", tenantUsage{nodes: 99, nodeLimit: 0}, false},
		{"a zero link limit is unlimited", tenantUsage{links: 99, linkLimit: 0}, false},
		{"a zero route limit is unlimited", tenantUsage{routes: 99, routeLimit: 0}, false},
		{"nothing held anywhere", tenantUsage{}, false},
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
	one := tenantUsage{nodes: 3, nodeLimit: 2}
	if got := one.describe(); got != "3 nodes (allowed 2)" {
		t.Fatalf("describe() = %q", got)
	}
	both := tenantUsage{nodes: 3, nodeLimit: 2, routes: 7, routeLimit: 6}
	if got := both.describe(); got != "3 nodes (allowed 2) and 7 protected addresses (allowed 6)" {
		t.Fatalf("describe() = %q", got)
	}
}
