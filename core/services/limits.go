package services

import "dylaris-core/store"

// Limits is a tenant's effective per-tenant caps. 0 means unlimited. Traffic
// limits are monthly GB and warn-only; max_nodes + R2 quota are hard-enforced.
type Limits struct {
	MaxNodes          int64 `json:"maxNodes"`
	R2QuotaGB         int64 `json:"r2QuotaGb"`
	TrafficEdgeGB     int64 `json:"trafficEdgeGb"`
	TrafficRelayGB    int64 `json:"trafficRelayGb"`
	TrafficCombinedGB int64 `json:"trafficCombinedGb"`
}

// limitStore is the narrow store surface EffectiveLimits needs.
type limitStore interface {
	GetUserBilling(userID string) (*store.UserBilling, error)
	GetUserPlanID(userID string) (*int, error)
	GetPlan(id int) (*store.Plan, error)
	GetDefaultPlan() (*store.Plan, error)
}

// EffectiveLimits resolves a tenant's caps: the plan (the user's plan_id, else the
// default plan, else unlimited) provides the baseline, and a per-user override in
// user_billing wins where set. Callers must gate this behind feature_byon_enabled.
func EffectiveLimits(st limitStore, userID string) (Limits, error) {
	// Plan baseline.
	var plan *store.Plan
	pid, err := st.GetUserPlanID(userID)
	if err != nil {
		return Limits{}, err
	}
	if pid != nil {
		// A deleted plan falls back to the default rather than erroring.
		if p, perr := st.GetPlan(*pid); perr == nil {
			plan = p
		}
	}
	if plan == nil {
		plan, _ = st.GetDefaultPlan() // nil -> everything unlimited
	}

	lim := Limits{}
	if plan != nil {
		lim = Limits{
			MaxNodes:          plan.MaxNodes,
			R2QuotaGB:         plan.R2QuotaGB,
			TrafficEdgeGB:     plan.TrafficEdgeGB,
			TrafficRelayGB:    plan.TrafficRelayGB,
			TrafficCombinedGB: plan.TrafficCombinedGB,
		}
	}

	// Per-user overrides win.
	if b, berr := st.GetUserBilling(userID); berr == nil && b != nil {
		if b.MaxNodes != nil {
			lim.MaxNodes = *b.MaxNodes
		}
		if b.R2QuotaGB != nil {
			lim.R2QuotaGB = *b.R2QuotaGB
		}
		if b.TrafficEdgeGB != nil {
			lim.TrafficEdgeGB = *b.TrafficEdgeGB
		}
		if b.TrafficRelayGB != nil {
			lim.TrafficRelayGB = *b.TrafficRelayGB
		}
		if b.TrafficCombinedGB != nil {
			lim.TrafficCombinedGB = *b.TrafficCombinedGB
		}
	}
	return lim, nil
}
