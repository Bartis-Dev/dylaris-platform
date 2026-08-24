package services

import (
	"context"
	"fmt"
	"log"
	"time"

	"dylaris-core/mailer"
)

// overLimitGrace is how long a tenant keeps everything running after they are
// first seen holding more than they bought. Fixed rather than configurable: it
// is a fairness window, not a knob, and the platform-wide payment grace is a
// different clock for a different problem.
// Exported so the panel can be told the same deadline the sweep enforces,
// rather than duplicating the number and drifting from it.
const OverLimitGrace = 72 * time.Hour

// tenantUsage is what a tenant currently HOLDS, against what they may hold.
// Counted fresh each pass rather than tracked, because every one of these can
// change without going through billing (a node is deleted, a kit is revoked).
type tenantUsage struct {
	nodes, nodeLimit   int64
	links, linkLimit   int64
	routes, routeLimit int64
}

func (u tenantUsage) over() bool {
	return (u.nodeLimit > 0 && u.nodes > u.nodeLimit) ||
		(u.linkLimit > 0 && u.links > u.linkLimit) ||
		(u.routeLimit > 0 && u.routes > u.routeLimit)
}

// describe renders the dimensions that are actually over, for the email and the
// log. Naming only what is wrong keeps a one-node overage from reading like a
// total failure.
func (u tenantUsage) describe() string {
	var parts []string
	if u.nodeLimit > 0 && u.nodes > u.nodeLimit {
		parts = append(parts, fmt.Sprintf("%d nodes (allowed %d)", u.nodes, u.nodeLimit))
	}
	if u.linkLimit > 0 && u.links > u.linkLimit {
		parts = append(parts, fmt.Sprintf("%d route-only locations (allowed %d)", u.links, u.linkLimit))
	}
	if u.routeLimit > 0 && u.routes > u.routeLimit {
		parts = append(parts, fmt.Sprintf("%d protected addresses (allowed %d)", u.routes, u.routeLimit))
	}
	out := ""
	for i, p := range parts {
		switch {
		case i == 0:
			out = p
		case i == len(parts)-1:
			out += " and " + p
		default:
			out += ", " + p
		}
	}
	return out
}

// tenantUsageFor counts what one tenant holds against their effective caps.
//
// Node usage counts UNREDEEMED warp keys alongside live nodes, matching the mint
// gate: a minted key is a machine that has not connected yet, and ignoring it
// would let a downgraded tenant sit under the cap on paper while three boxes are
// mid-setup.
func (s *BillingLifecycleService) tenantUsageFor(ctx context.Context, userID string) (tenantUsage, error) {
	var u tenantUsage

	lim, err := EffectiveLimits(s.store, userID)
	if err != nil {
		return u, err
	}
	u.nodeLimit, u.linkLimit = lim.MaxNodes, lim.MaxLinks

	nodes, err := s.store.CountNodesByOwner(userID)
	if err != nil {
		return u, err
	}
	keys, err := s.store.CountNodeWarpKeysByOwner(userID)
	if err != nil {
		return u, err
	}
	u.nodes = int64(nodes + keys)

	kits, err := s.store.CountLinkKitsByOwner(userID)
	if err != nil {
		return u, err
	}
	u.links = int64(kits)

	// The address pool lives in gateway_route_limits under the per-user scope,
	// which is what a purchase writes. Only that scope is read here: the
	// user_default and global fallbacks are the PLATFORM's baseline, and cutting
	// a tenant off for exceeding a number nobody sold them would be wrong.
	if l, lerr := s.store.GetGatewayRouteLimit("user:" + userID); lerr == nil && l != nil {
		u.routeLimit = int64(l.MaxRoutes)
	}
	// Routes live in Redis, not the database - they are gateway data plane state.
	// With no Redis there is no route plane, so there is nothing to be over.
	//
	// Only addresses on OUR domains count, exactly as the two create paths count
	// them. If this counted custom domains too, a tenant could create a route the
	// panel allowed and then be cut off by this sweep for holding it - the guard
	// one door takes and its sibling does not.
	if s.redis != nil {
		raw, _ := s.store.GetSetting(HosterDomainsSettingKey)
		bases := HosterBaseDomains(raw)
		for _, rt := range GetRoutesFromRedis(ctx, s.redis) {
			if rt.OwnerID == userID && DomainIsOurs(rt.Domain, bases) {
				u.routes++
			}
		}
	}

	return u, nil
}

// enforceEntitlementLimits is the over-limit half of the lifecycle: a tenant who
// holds more than they bought gets a fixed grace window with a warning, and is
// cut off in full if they are still over when it ends.
//
// Cutting EVERYTHING rather than the excess is deliberate. Picking which three
// of five nodes to kill is a judgement the platform has no business making, and
// any rule for it (oldest, emptiest, last added) is wrong for someone. The
// tenant chooses instead, by getting back under their own limit; if they do not
// choose, nothing runs. Same cutoff the payment path uses, so a tenant who is
// both over-limit and unpaid is not stopped twice.
func (s *BillingLifecycleService) enforceEntitlementLimits(ctx context.Context) {
	rows, err := s.store.ListUserBilling()
	if err != nil {
		log.Printf("billing over-limit: list user_billing: %v", err)
		return
	}
	now := time.Now()

	for _, b := range rows {
		// A suspended tenant is already stopped by the payment path. Marking them
		// over-limit on top would send a second warning about a second deadline
		// for services that are not running.
		if b.Status == "suspended" {
			continue
		}

		u, err := s.tenantUsageFor(ctx, b.UserID)
		if err != nil {
			log.Printf("billing over-limit: usage for %s: %v", b.UserID, err)
			continue
		}

		if !u.over() {
			if b.OverLimitSince != nil {
				if err := s.store.SetUserOverLimitSince(b.UserID, nil); err != nil {
					log.Printf("billing over-limit: clear %s: %v", b.UserID, err)
					continue
				}
				log.Printf("billing over-limit: %s is back within its limits", b.UserID)
			}
			continue
		}

		if b.OverLimitSince == nil {
			at := now
			if err := s.store.SetUserOverLimitSince(b.UserID, &at); err != nil {
				log.Printf("billing over-limit: stamp %s: %v", b.UserID, err)
				continue
			}
			deadline := at.Add(OverLimitGrace)
			log.Printf("billing over-limit: %s holds %s, cutoff at %s",
				b.UserID, u.describe(), deadline.Format(time.RFC3339))
			s.sendOverLimitEmail(b.UserID, u, deadline)
			continue
		}

		if now.Before(b.OverLimitSince.Add(OverLimitGrace)) {
			continue
		}

		log.Printf("billing over-limit: cutoff for %s, still holding %s after the grace window",
			b.UserID, u.describe())
		s.stopTenantServers(ctx, b.UserID)
		s.suspendTenantWarpPeers(ctx, b.UserID)
	}
}

func (s *BillingLifecycleService) sendOverLimitEmail(userID string, u tenantUsage, deadline time.Time) {
	usr, err := s.store.GetUserByID(userID)
	if err != nil || usr == nil || usr.Email == "" {
		return
	}
	cfg, err := mailer.LoadConfig(s.store, "auth")
	if err != nil {
		return
	}
	body := fmt.Sprintf(`Hi %s,

Your account is using more than your subscription covers: %s.

Nothing has stopped. You have until %s to get back within your limits, either by
removing what you no longer need or by raising your subscription.

If you are still over the limit after that, EVERYTHING on your account is
disconnected - not just the excess. We do not choose which of your machines to
stop; that choice is yours.

Nothing is deleted either way. Your servers, data and backups stay where they
are and come straight back once you are within your limits.

Manage your account here:

%s

- Dylaris
`, usr.Username, u.describe(), deadline.Format("2 January 2006, 15:04 MST"), s.frontendURL)

	if err := mailer.Send(cfg, mailer.Message{
		To:      usr.Email,
		Subject: "Action needed: your account is over its limit",
		Body:    body,
	}); err != nil {
		log.Printf("billing over-limit: email %s: %v", userID, err)
	}
}
