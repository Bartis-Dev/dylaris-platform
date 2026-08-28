package services

import "dylaris-core/store"

// Limits is a tenant's effective per-tenant caps.
//
// THE CONVENTION, platform-wide:
//
//	nil  = no limit at all (unlimited)
//	0    = none - the tenant may hold zero of this
//	n>0  = the cap
//
// A POINTER rather than an int with a magic value, because the two states a
// plain int cannot tell apart are exactly the two that matter. This platform
// used to spell unlimited as 0, and a zero is what arithmetic produces when a
// tenant bought nothing: the one number meaning "none" was the one number that
// switched the check off. That shape has been a real defect here four times -
// most recently an address allowance of zero granting unlimited addresses - so
// it is now unrepresentable rather than merely documented.
//
// Reading one of these is therefore always "if the pointer is nil there is no
// cap; otherwise compare against the value", and 0 compares like any other
// number. There is no branch that skips itself.
type Limits struct {
	MaxNodes          *int64 `json:"maxNodes"`
	MaxLinks          *int64 `json:"maxLinks"`
	R2QuotaGB         *int64 `json:"r2QuotaGb"`
	TrafficEdgeGB     *int64 `json:"trafficEdgeGb"`
	TrafficRelayGB    *int64 `json:"trafficRelayGb"`
	TrafficCombinedGB *int64 `json:"trafficCombinedGb"`
}

// Exceeds reports whether used is over the cap. A nil cap is never exceeded.
// The one place the nil check lives, so no caller can forget it.
func Exceeds(cap *int64, used int64) bool {
	return cap != nil && used > *cap
}

// AtOrOver reports whether used has reached the cap, which is the question a
// CREATE path asks: may one more be added. A nil cap never blocks.
func AtOrOver(cap *int64, used int64) bool {
	return cap != nil && used >= *cap
}

// LimitOr returns the cap's value, or def when there is no cap. For rendering
// and for the few callers that genuinely want a number.
func LimitOr(cap *int64, def int64) int64 {
	if cap == nil {
		return def
	}
	return *cap
}

// limitStore is the narrow store surface EffectiveLimits needs. One lookup:
// caps are the per-user overrides the store pushes on purchase, or an admin
// sets by hand. Plans no longer take part - see EffectiveLimits.
type limitStore interface {
	GetUserBilling(userID string) (*store.UserBilling, error)
}

// EffectiveLimits resolves a tenant's caps from the per-user overrides in
// user_billing. An unset column means no cap, which is the self-host case.
//
// Plans used to provide a baseline under these. They are gone: the hosted store
// never sold one - it pushes a node COUNT - and letting self-hosters hand out
// tariffs of their own was a product nobody asked for. What remains is the one
// mechanism both paths actually used.
//
// IMPORTANT for callers: these caps answer "how many", never "may they at all".
// An account that bought nothing has no overrides and therefore no caps, so a
// cap check alone would let it through. Gate on services.EffectiveEntitlement
// first and use this only for the ceiling behind it.
//
// Callers must gate this behind feature_byon_enabled.
func EffectiveLimits(st limitStore, userID string) (Limits, error) {
	b, err := st.GetUserBilling(userID)
	if err != nil {
		return Limits{}, err
	}
	if b == nil {
		return Limits{}, nil
	}
	return Limits{
		MaxNodes:          b.MaxNodes,
		MaxLinks:          b.MaxLinks,
		R2QuotaGB:         b.R2QuotaGB,
		TrafficEdgeGB:     b.TrafficEdgeGB,
		TrafficRelayGB:    b.TrafficRelayGB,
		TrafficCombinedGB: b.TrafficCombinedGB,
	}, nil
}
