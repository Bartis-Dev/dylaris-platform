package services

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"dylaris-core/services/redisacl"
	"dylaris-core/store"

	"github.com/redis/go-redis/v9"
)

// linkRevokeFakeStore embeds store.Store (nil) so it satisfies the full
// interface at compile time; only RevokeWarpAPIKeyByNodeID is overridden.
type linkRevokeFakeStore struct {
	store.Store
	revokeErr   map[string]error
	revokeCalls []string
}

func (f *linkRevokeFakeStore) RevokeWarpAPIKeyByNodeID(nodeID string) error {
	f.revokeCalls = append(f.revokeCalls, nodeID)
	return f.revokeErr[nodeID]
}

// linkRevokeFakeGateway embeds GatewayProvider (nil) so it satisfies the
// full interface at compile time; only LinkToken and DeleteCoreOwnedRoute
// (the two methods RevokeLinkKitTeardown calls) are overridden.
type linkRevokeFakeGateway struct {
	GatewayProvider
	tunnelToken    string
	deleteErrFor   map[string]error
	deletedDomains []string
}

func (g *linkRevokeFakeGateway) LinkToken(nodeID string) string { return g.tunnelToken }

func (g *linkRevokeFakeGateway) DeleteCoreOwnedRoute(domain string) error {
	g.deletedDomains = append(g.deletedDomains, domain)
	return g.deleteErrFor[domain]
}

func TestRevokeLinkKitTeardown_RevokeFails_NoSideEffects(t *testing.T) {
	rdb := newQueueTestRedis(t)
	fs := &linkRevokeFakeStore{revokeErr: map[string]error{"link-1": errors.New("db down")}}
	gw := &linkRevokeFakeGateway{tunnelToken: "tunnel-1"}
	prov := redisacl.NewProvisioner(rdb)

	removed, err := RevokeLinkKitTeardown(context.Background(), fs, gw, rdb, prov, "link-1", "owner-1")

	if err == nil {
		t.Fatal("expected error when the durable revoke fails")
	}
	if removed != 0 {
		t.Errorf("removed = %d, want 0", removed)
	}
	if len(gw.deletedDomains) != 0 {
		t.Errorf("expected no route deletion attempts after a failed revoke, got %v", gw.deletedDomains)
	}
}

func TestRevokeLinkKitTeardown_RemovesOnlyMatchingCoreOwnedRoutes(t *testing.T) {
	ctx := context.Background()
	rdb := newQueueTestRedis(t)
	fs := &linkRevokeFakeStore{revokeErr: map[string]error{}}
	const tunnelToken = "tunnel-1"
	gw := &linkRevokeFakeGateway{tunnelToken: tunnelToken}
	prov := redisacl.NewProvisioner(rdb)

	seedGatewayRoute(t, rdb, "a.example.com", GatewayRoute{CoreOwned: true, OwnerID: "owner-1", TunnelID: tunnelToken})
	seedGatewayRoute(t, rdb, "b.example.com", GatewayRoute{CoreOwned: true, OwnerID: "owner-2", TunnelID: tunnelToken})    // different owner
	seedGatewayRoute(t, rdb, "c.example.com", GatewayRoute{CoreOwned: false, OwnerID: "owner-1", TunnelID: tunnelToken})   // not core-owned
	seedGatewayRoute(t, rdb, "d.example.com", GatewayRoute{CoreOwned: true, OwnerID: "owner-1", TunnelID: "other-tunnel"}) // different tunnel
	seedGatewayRoute(t, rdb, "e.example.com", GatewayRoute{CoreOwned: true, OwnerID: "owner-1", TunnelID: tunnelToken})

	removed, err := RevokeLinkKitTeardown(ctx, fs, gw, rdb, prov, "link-1", "owner-1")
	if err != nil {
		t.Fatalf("RevokeLinkKitTeardown: %v", err)
	}
	if removed != 2 {
		t.Errorf("removed = %d, want 2", removed)
	}

	deleted := map[string]bool{}
	for _, d := range gw.deletedDomains {
		deleted[d] = true
	}
	if !deleted["a.example.com"] || !deleted["e.example.com"] {
		t.Errorf("deletedDomains = %v, want a.example.com and e.example.com", gw.deletedDomains)
	}
	if deleted["b.example.com"] || deleted["c.example.com"] || deleted["d.example.com"] {
		t.Errorf("deletedDomains = %v, want only owner-matched core-owned same-tunnel routes", gw.deletedDomains)
	}

	if len(fs.revokeCalls) != 1 || fs.revokeCalls[0] != "link-1" {
		t.Errorf("revokeCalls = %v, want [link-1]", fs.revokeCalls)
	}
}

func TestRevokeLinkKitTeardown_RouteDeleteError_ContinuesAndSkipsCount(t *testing.T) {
	ctx := context.Background()
	rdb := newQueueTestRedis(t)
	fs := &linkRevokeFakeStore{revokeErr: map[string]error{}}
	const tunnelToken = "tunnel-2"
	gw := &linkRevokeFakeGateway{
		tunnelToken:  tunnelToken,
		deleteErrFor: map[string]error{"f.example.com": errors.New("redis blip")},
	}
	prov := redisacl.NewProvisioner(rdb)

	seedGatewayRoute(t, rdb, "f.example.com", GatewayRoute{CoreOwned: true, OwnerID: "owner-1", TunnelID: tunnelToken})
	seedGatewayRoute(t, rdb, "g.example.com", GatewayRoute{CoreOwned: true, OwnerID: "owner-1", TunnelID: tunnelToken})

	removed, err := RevokeLinkKitTeardown(ctx, fs, gw, rdb, prov, "link-2", "owner-1")
	if err != nil {
		t.Fatalf("RevokeLinkKitTeardown: %v", err)
	}
	if removed != 1 {
		t.Errorf("removed = %d, want 1 (the failed delete must not be counted)", removed)
	}
}

func TestRevokeLinkKitTeardown_NoMatchingRoutes_ReturnsZero(t *testing.T) {
	ctx := context.Background()
	rdb := newQueueTestRedis(t)
	fs := &linkRevokeFakeStore{revokeErr: map[string]error{}}
	gw := &linkRevokeFakeGateway{tunnelToken: "tunnel-3"}
	prov := redisacl.NewProvisioner(rdb)

	removed, err := RevokeLinkKitTeardown(ctx, fs, gw, rdb, prov, "link-3", "owner-1")
	if err != nil {
		t.Fatalf("RevokeLinkKitTeardown: %v", err)
	}
	if removed != 0 {
		t.Errorf("removed = %d, want 0", removed)
	}
}

func TestRevokeLinkKitTeardown_DeletesTunnelKey(t *testing.T) {
	ctx := context.Background()
	rdb := newQueueTestRedis(t)
	fs := &linkRevokeFakeStore{revokeErr: map[string]error{}}
	const tunnelToken = "tunnel-4"
	gw := &linkRevokeFakeGateway{tunnelToken: tunnelToken}
	prov := redisacl.NewProvisioner(rdb)

	if err := rdb.Set(ctx, "link:"+tunnelToken, "some-value", 0).Err(); err != nil {
		t.Fatalf("seed tunnel key: %v", err)
	}

	if _, err := RevokeLinkKitTeardown(ctx, fs, gw, rdb, prov, "link-4", "owner-1"); err != nil {
		t.Fatalf("RevokeLinkKitTeardown: %v", err)
	}

	n, err := rdb.Exists(ctx, "link:"+tunnelToken).Result()
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	if n != 0 {
		t.Errorf("tunnel key still exists after teardown")
	}
}

// seedGatewayRoute writes a route in the same format GetRoutesFromRedis reads:
// route:{domain} JSON + membership in sys:index:routes.
func seedGatewayRoute(t *testing.T, rdb *redis.Client, domain string, route GatewayRoute) {
	t.Helper()
	route.Domain = domain
	data, err := json.Marshal(route)
	if err != nil {
		t.Fatalf("marshal route: %v", err)
	}
	ctx := context.Background()
	if err := rdb.Set(ctx, "route:"+domain, data, 0).Err(); err != nil {
		t.Fatalf("seed route:%s: %v", domain, err)
	}
	if err := rdb.SAdd(ctx, "sys:index:routes", domain).Err(); err != nil {
		t.Fatalf("seed sys:index:routes: %v", err)
	}
}
