package services

import "dylaris-core/store"

// Limits is a tenant's effective per-tenant caps. 0 means unlimited. Traffic
// limits are monthly GB and warn-only; max_nodes + R2 quota are hard-enforced.
type Limits struct {
	MaxNodes          int64 `json:"maxNodes"`
	MaxLinks          int64 `json:"maxLinks"`
	R2QuotaGB         int64 `json:"r2QuotaGb"`
	TrafficEdgeGB     int64 `json:"trafficEdgeGb"`
	TrafficRelayGB    int64 `json:"trafficRelayGb"`
	TrafficCombinedGB int64 `json:"trafficCombinedGb"`
}

// limitStore is the narrow store surface EffectiveLimits needs. One lookup:
// caps are the per-user overrides the store pushes on purchase, or an admin
// sets by hand. Plans no longer take part - see EffectiveLimits.
type limitStore interface {
	GetUserBilling(userID string) (*store.UserBilling, error)
}

// EffectiveLimits resolves a tenant's caps from the per-user overrides in
// user_billing. 0 (and "not set") means unlimited, which is the self-host case.
//
// Plans used to provide a baseline under these. They are gone: the hosted store
// never sold one - it pushes a node COUNT - and letting self-hosters hand out
// tariffs of their own was a product nobody asked for. What remains is the one
// mechanism both paths actually used.
//
// IMPORTANT for callers: a zero here means "no cap", NOT "nothing allowed". An
// account that bought nothing also has zeroes, so a bare `if lim.MaxNodes > 0`
// check silently allows it. Gate on services.EffectiveEntitlement first and use
// this only for the ceiling behind it.
//
// Callers must gate this behind feature_byon_enabled.
func EffectiveLimits(st limitStore, userID string) (Limits, error) {
	lim := Limits{}
	b, err := st.GetUserBilling(userID)
	if err != nil {
		return Limits{}, err
	}
	if b == nil {
		return lim, nil
	}
	if b.MaxNodes != nil {
		lim.MaxNodes = *b.MaxNodes
	}
	if b.MaxLinks != nil {
		lim.MaxLinks = *b.MaxLinks
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
	return lim, nil
}
