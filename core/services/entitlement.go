package services

import (
	"strings"
	"time"

	"dylaris-core/store"
)

// Entitlement kinds. A plan or a grant carries one of these; "" means none.
const (
	EntitlementByon      = "byon"
	EntitlementRouteOnly = "route_only"
	EntitlementBoth      = "both"
)

// Where an entitlement came from. Reported so the UI can say "from your
// subscription" vs "granted by an admin until <date>" instead of just yes/no.
const (
	EntitlementSourceNone      = "none"
	EntitlementSourcePlan      = "plan"
	EntitlementSourceGrant     = "grant"
	EntitlementSourceBoth      = "plan+grant"
	EntitlementSourceUnlimited = "unlimited"
	EntitlementSourceSuspended = "suspended"
)

// Entitlement answers "what may this tenant use", separately from "how much"
// (that is Limits).
type Entitlement struct {
	// Byon: may enroll their own nodes and run servers on them.
	Byon bool `json:"byon"`
	// RouteOnly: may create routes / link kits without owning a node.
	RouteOnly bool `json:"routeOnly"`
	// Source explains the answer; see the EntitlementSource* constants.
	Source string `json:"source"`
	// PlanKind is the kind the resolved plan carries ("" when there is no plan).
	PlanKind string `json:"planKind,omitempty"`
	// GrantKind + GrantExpiresAt describe an ACTIVE manual grant only. An expired
	// grant is reported as no grant at all, so a stale row can never read as one.
	GrantKind      string     `json:"grantKind,omitempty"`
	GrantExpiresAt *time.Time `json:"grantExpiresAt,omitempty"`
}

// entitlementStore is the narrow store surface EffectiveEntitlement needs.
type entitlementStore interface {
	GetUserBilling(userID string) (*store.UserBilling, error)
	GetUserPlanID(userID string) (*int, error)
	GetPlan(id int) (*store.Plan, error)
	GetDefaultPlan() (*store.Plan, error)
}

// kindGrants expands a kind into the two booleans. An unknown kind grants
// nothing rather than everything: a typo in a plan row must not widen access.
func kindGrants(kind string) (byon, routeOnly bool) {
	switch strings.TrimSpace(strings.ToLower(kind)) {
	case EntitlementBoth:
		return true, true
	case EntitlementByon:
		return true, false
	case EntitlementRouteOnly:
		return false, true
	default:
		return false, false
	}
}

// EffectiveEntitlement resolves what a tenant may use, at time `now`.
//
// The rules, in order:
//
//  1. A suspended tenant gets nothing, whatever they hold. Suspension is a hard
//     stop; an admin who wants to restore access sets the status back to active.
//     (past_due is NOT a stop - that is what the grace window is for.)
//  2. An ACTIVE manual grant contributes its kind. Active means a kind is set and
//     the expiry is in the future; an expired row contributes nothing.
//  3. The plan contributes its kind. With no plan at all the answer is
//     "unlimited", which is what the platform does today: EffectiveLimits treats a
//     missing plan as uncapped, so denying here would lock out every self-host
//     install that never defined plans.
//
// The two contributions are a UNION, deliberately: "give them 14 days now, they
// can subscribe later" must not have the subscription and the grant fight.
//
// Callers must gate this behind feature_byon_enabled; with BYON off none of it
// is meaningful.
func EffectiveEntitlement(st entitlementStore, userID string, now time.Time) (Entitlement, error) {
	billing, err := st.GetUserBilling(userID)
	if err != nil {
		return Entitlement{}, err
	}
	if billing != nil && billing.Status == "suspended" {
		return Entitlement{Source: EntitlementSourceSuspended}, nil
	}

	out := Entitlement{Source: EntitlementSourceNone}

	// Manual grant.
	grantByon, grantRoute := false, false
	if billing != nil && strings.TrimSpace(billing.ManualEntitlement) != "" {
		exp := billing.ManualEntitlementExpiresAt
		if exp != nil && exp.After(now) {
			grantByon, grantRoute = kindGrants(billing.ManualEntitlement)
			out.GrantKind = billing.ManualEntitlement
			out.GrantExpiresAt = exp
		}
	}

	// Plan.
	var plan *store.Plan
	pid, err := st.GetUserPlanID(userID)
	if err != nil {
		return Entitlement{}, err
	}
	if pid != nil {
		// A deleted plan falls back to the default rather than erroring, matching
		// EffectiveLimits.
		if p, perr := st.GetPlan(*pid); perr == nil {
			plan = p
		}
	}
	if plan == nil {
		plan, _ = st.GetDefaultPlan()
	}

	planByon, planRoute := false, false
	if plan != nil {
		out.PlanKind = plan.Kind
		planByon, planRoute = kindGrants(plan.Kind)
	} else {
		// No plan anywhere: today's uncapped behaviour.
		planByon, planRoute = true, true
		out.Source = EntitlementSourceUnlimited
	}

	out.Byon = grantByon || planByon
	out.RouteOnly = grantRoute || planRoute

	// Name the source(s) that actually contributed.
	hasGrant := grantByon || grantRoute
	hasPlan := planByon || planRoute
	switch {
	case out.Source == EntitlementSourceUnlimited && !hasGrant:
		// keep "unlimited"
	case hasGrant && hasPlan && out.Source != EntitlementSourceUnlimited:
		out.Source = EntitlementSourceBoth
	case hasGrant && out.Source == EntitlementSourceUnlimited:
		out.Source = EntitlementSourceBoth
	case hasGrant:
		out.Source = EntitlementSourceGrant
	case hasPlan:
		out.Source = EntitlementSourcePlan
	default:
		out.Source = EntitlementSourceNone
	}
	return out, nil
}
