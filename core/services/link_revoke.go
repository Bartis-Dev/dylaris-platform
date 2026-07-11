package services

import (
	"context"
	"fmt"
	"log"

	"dylaris-core/services/redisacl"
	"dylaris-core/store"

	"github.com/redis/go-redis/v9"
)

// RevokeLinkKitTeardown durably revokes one route-only link kit end to end:
// marks the warp key revoked (blocking future warp-enroll and link-boot for
// this identity), drops its scoped Redis ACL user, deletes its tunnel key, and
// removes its Core-owned routes. Order matters twice, exactly as the original
// handlers.WarpHandler.RevokeLinkKit this was extracted from: the durable DB
// revoke runs FIRST so a partial failure below cannot undo itself on retry,
// and the ACL user is dropped before the tunnel key so a restarted link
// cannot re-register. Shared by the tenant-facing revoke endpoint and the
// admin force-suspend path (BillingLifecycleService.SuspendNow) so the
// teardown ordering has one source of truth. ownerID scopes the route
// cleanup: a route is only removed when it belongs to this owner AND this
// link's tunnel. Returns the number of routes removed; a non-nil error means
// the durable revoke itself failed (the caller should treat the link as NOT
// torn down and may retry) - every step after that is best-effort and only
// logged, never returned as an error.
func RevokeLinkKitTeardown(ctx context.Context, st store.Store, gw GatewayProvider, rdb *redis.Client, prov *redisacl.Provisioner, linkID, ownerID string) (removedRoutes int, err error) {
	if derr := st.RevokeWarpAPIKeyByNodeID(linkID); derr != nil {
		return 0, fmt.Errorf("mark revoked: %w", derr)
	}
	tunnelToken := gw.LinkToken(linkID)
	prov.RemoveRouteOnlyLinkACL(ctx, linkID)
	if derr := rdb.Del(ctx, "link:"+tunnelToken).Err(); derr != nil {
		log.Printf("revoke link %s: delete tunnel key: %v", linkID, derr)
	}
	for _, rt := range GetRoutesFromRedis(ctx, rdb) {
		if rt.CoreOwned && rt.OwnerID == ownerID && rt.TunnelID == tunnelToken {
			if derr := gw.DeleteCoreOwnedRoute(rt.Domain); derr != nil {
				log.Printf("revoke link %s: delete route %s: %v", linkID, rt.Domain, derr)
				continue
			}
			removedRoutes++
		}
	}
	return removedRoutes, nil
}
