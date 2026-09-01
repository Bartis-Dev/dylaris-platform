package services

import (
	"context"
	"fmt"
	"log"
	"strings"

	"dylaris-core/services/redisacl"
	"dylaris-core/store"

	"github.com/redis/go-redis/v9"
)

// TeardownTenantInfrastructure removes everything an account HOLDS outside its
// own database row: its route-only link kits (durable revoke, scoped Redis ACL
// user, tunnel key) and its protected addresses.
//
// It exists because removing an account did not remove what the account ran,
// and the two paths that remove accounts disagreed about how much of that was
// their problem.
//
// The admin delete endpoint cleaned up ADDRESSES - core_link_routes.owner_id is
// TEXT with no constraint, so nothing cascades and the republisher writes every
// stored row back into Redis every 60 seconds. The auto-delete sweep, which is
// what removes an account in practice, called store.DeleteUser directly and
// cleaned up nothing at all. Its DEFAULT mode is "anonymize", which keeps the
// row: the person is gone, and their link and addresses carry on.
//
// The link kit is the sharper half, and it is invisible to every self-heal the
// platform has. warp_api_keys.owner_id is ON DELETE CASCADE, so deleting the
// account deletes the ROW - and the reconciler's teardown sweep finds work by
// enumerating rows. With the row gone it can never see that the kit's Redis ACL
// user and its tunnel key are still there, both still valid, with no expiry and
// nothing left that refers to them. Measured on the live instance: two link-*
// ACL users against one live kit.
//
// Order is the same one RevokeLinkKitTeardown documents, for the same reason:
// the durable revoke first, so a partial failure cannot undo itself on retry.
// The caller must run this BEFORE removing the account row - afterwards there is
// no owner left to look any of it up by.
//
// A non-nil error means something DURABLE failed and the account must not be
// removed yet. Everything best-effort is logged and carries on, so one
// unreachable route does not strand the rest.
func TeardownTenantInfrastructure(ctx context.Context, st store.Store, gw GatewayProvider, rdb *redis.Client, prov *redisacl.Provisioner, userID string) error {
	if st == nil || userID == "" {
		return nil
	}

	// Link kits first: RevokeLinkKitTeardown also removes the routes that belong
	// to each kit's tunnel, so the sweep below is left with whatever is not tied
	// to a link.
	//
	// Skipped, loudly, when the link plane is not wired rather than silently: on
	// an install with no gateway there are no kits, but a MISSING dependency
	// where there should be one would otherwise look identical to that.
	if gw != nil && rdb != nil && prov != nil {
		keys, err := st.ListWarpAPIKeysByOwner(userID)
		if err != nil {
			return fmt.Errorf("list link kits: %w", err)
		}
		for _, k := range keys {
			if !strings.HasPrefix(k.NodeID, "link-") {
				continue
			}
			if _, rerr := RevokeLinkKitTeardown(ctx, st, gw, rdb, prov, k.NodeID, userID); rerr != nil {
				return fmt.Errorf("revoke link kit %s: %w", k.NodeID, rerr)
			}
		}
	} else {
		log.Printf("tenant teardown for %s: link plane not wired, link kits and their credentials are NOT torn down", userID)
	}

	// Whatever addresses are left. A route can outlive the link it was created
	// through, and one created by an admin on the tenant's behalf may never have
	// had a link token at all, so this is keyed on the OWNER rather than on any
	// tunnel.
	if gw == nil {
		return nil
	}
	routes, err := st.ListCoreLinkRoutes()
	if err != nil {
		return fmt.Errorf("list protected addresses: %w", err)
	}
	for _, rt := range routes {
		if rt.OwnerID != userID {
			continue
		}
		if derr := gw.DeleteCoreOwnedRoute(rt.Domain); derr != nil {
			// Durable: a route left behind keeps sending players to a machine
			// whose owner no longer exists, and the republisher will put it back
			// within the minute even if someone clears it by hand.
			return fmt.Errorf("remove protected address %s: %w", rt.Domain, derr)
		}
	}
	return nil
}
