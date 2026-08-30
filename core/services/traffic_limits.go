package services

import (
	"dylaris-core/models"
)

// TrafficLimitScopes is the order a (region, kind) question is asked in, most
// specific first. Exported because the panel shows which scope answered, and a
// screen that guessed the order would eventually disagree with the resolver.
func TrafficLimitScopes(userID string) []string {
	return []string{"user:" + userID, "user_default", "global"}
}

// TrafficLimitReader is the slice of the store this resolver needs. Narrow on
// purpose: it makes the walk testable without a database, and the walk is the
// part worth testing.
type TrafficLimitReader interface {
	GetTrafficLimit(scope, region, kind string) (*models.TrafficLimit, error)
}

// ResolvedTrafficLimit is one answer: how much is included, how much may be
// bought, and which scope said so.
//
// Both numbers follow the platform-wide convention (CLAUDE.md, "Limits"): nil
// is no limit at all, 0 is none, n is the cap. Read them through Exceeds /
// AtOrOver rather than comparing by hand, so no call site can forget the nil
// check and so a 0 compares like any other number.
type ResolvedTrafficLimit struct {
	IncludedGB    *int64
	MaxPurchaseGB *int64
	// Scope that answered, "" when nothing did. "" therefore means no limit of
	// any kind for this (region, kind) - which is a real state and the reason
	// the panel needs to show unconfigured regions rather than hide them.
	Scope string
}

// ResolveTrafficLimit walks the scopes most-specific-first and returns the
// first ROW that exists.
//
// The row is the unit of answering, not the column. A scope holding a row has
// answered both questions, including with NULL - so a per-user override cannot
// set an allowance and silently inherit a purchase cap from a scope the
// operator was not looking at. That split answer is the shape that produced
// four separate defects on this platform before limits were made pointers.
//
// No row anywhere means no limit, which matches every other limit here. It is
// permissive by default and that is deliberate: an operator who has configured
// nothing has not yet decided, and refusing traffic on an undecided platform
// would take servers down over a settings screen nobody opened. The panel is
// where that gap has to be visible.
func ResolveTrafficLimit(r TrafficLimitReader, userID, region, kind string) (ResolvedTrafficLimit, error) {
	for _, scope := range TrafficLimitScopes(userID) {
		l, err := r.GetTrafficLimit(scope, region, kind)
		if err != nil {
			return ResolvedTrafficLimit{}, err
		}
		if l == nil {
			continue // this scope says nothing: ask the next one
		}
		return ResolvedTrafficLimit{
			IncludedGB:    l.IncludedGB,
			MaxPurchaseGB: l.MaxPurchaseGB,
			Scope:         scope,
		}, nil
	}
	return ResolvedTrafficLimit{}, nil
}

// PurchasableGB is how much extra traffic a tenant may still buy in a
// (region, kind), given what they have already bought.
//
// Returns nil for "no cap". A cap of 0 - a region where extra traffic is not
// for sale at any price - returns 0, never nil, which is exactly the
// distinction the pointer convention exists to keep: the number an operator
// types to mean "none" must not be the number that switches the check off.
func PurchasableGB(limit ResolvedTrafficLimit, alreadyBoughtGB int64) *int64 {
	if limit.MaxPurchaseGB == nil {
		return nil
	}
	left := *limit.MaxPurchaseGB - alreadyBoughtGB
	if left < 0 {
		left = 0
	}
	return &left
}
