package services

import (
	"context"
	"encoding/json"
	"testing"

	"dylaris-core/models"
)

// readRoute decodes route:{domain} the way the edge and the panel both read it.
func readRoute(t *testing.T, g *RedisGateway, domain string) GatewayRoute {
	t.Helper()
	val, err := g.redis.Get(context.Background(), "route:"+domain).Result()
	if err != nil {
		t.Fatalf("get route:%s: %v", domain, err)
	}
	var r GatewayRoute
	if err := json.Unmarshal([]byte(val), &r); err != nil {
		t.Fatalf("unmarshal route:%s: %v", domain, err)
	}
	return r
}

// A route-only entry is published straight to Redis with no Postgres row behind
// it, so nothing else in the system can enforce that one domain has one owner.
// The managed path gets that for free: it goes through the hub queue, and the
// hub's routes table has a unique index on domain and answers a second create
// with ErrRouteExists.
//
// Without the check here, the second tenant's SET simply overwrites the first
// tenant's route - tunnel_id and owner_id and all - so their players are handed
// to a stranger's Link and the domain disappears from their own panel listing
// (ListLinkRoutes filters on owner_id).
func TestCreateRouteViaLink_RefusesADomainAnotherTenantHolds(t *testing.T) {
	g, _, _ := newHubBridgeTestGateway(t)

	if err := g.CreateRouteViaLink("victim", "play.example.com", "victim-tok", "192.168.1.50", 25565); err != nil {
		t.Fatalf("victim create: %v", err)
	}
	err := g.CreateRouteViaLink("attacker", "play.example.com", "attacker-tok", "10.0.0.9", 25565)
	if err == nil {
		t.Error("a second tenant claimed a domain that was already taken, and got no error")
	}

	got := readRoute(t, g, "play.example.com")
	if got.OwnerID != "victim" || got.TunnelID != "victim-tok" {
		t.Errorf("route now points at owner=%q tunnel=%q; the first tenant's route was overwritten",
			got.OwnerID, got.TunnelID)
	}
}

// A managed server's route is written by the hub from its own table, so it
// carries no core_owned flag. A tenant must not be able to shadow it either -
// the overwrite would hold until the hub's next reconcile pass rewrites it,
// which is a five-minute outage that repeats every time the attacker re-posts.
func TestCreateRouteViaLink_RefusesAManagedServersDomain(t *testing.T) {
	g, rdb, _ := newHubBridgeTestGateway(t)

	managed, err := json.Marshal(map[string]interface{}{
		"tunnel_id": "node-link-tok", "target_ip": "mc_srv-uuid", "target_port": 25565,
		"server_uuid": "srv-uuid",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := rdb.Set(context.Background(), "route:mc.example.com", managed, 0).Err(); err != nil {
		t.Fatal(err)
	}

	if err := g.CreateRouteViaLink("attacker", "mc.example.com", "attacker-tok", "10.0.0.9", 25565); err == nil {
		t.Error("a tenant claimed a managed server's domain, and got no error")
	}
	if got := readRoute(t, g, "mc.example.com"); got.TunnelID != "node-link-tok" {
		t.Errorf("tunnel_id = %q, want node-link-tok; the managed route was overwritten", got.TunnelID)
	}
}

// The same collision from the other side. A managed server route has no row in
// the hub's table for a route-only domain, so the hub's unique index sees
// nothing to reject and its next sync writes route:{domain} over the tenant's
// entry - a takeover with a five-minute delay instead of an instant one.
func TestCreateServerRoute_RefusesATenantsRouteOnlyDomain(t *testing.T) {
	g, rdb, fs := newHubBridgeTestGateway(t)
	fs.nodes[1] = models.Node{ID: 1, Token: "node-tok-1"}
	fs.servers[10] = models.Server{ID: 10, UUID: "srv-uuid-10", NodeID: 1}

	if err := g.CreateRouteViaLink("victim", "play.example.com", "victim-tok", "192.168.1.50", 25565); err != nil {
		t.Fatalf("victim create: %v", err)
	}
	if err := g.CreateServerRoute(10, "attacker", "play.example.com", 25565); err == nil {
		t.Error("a managed server claimed a tenant's route-only domain, and got no error")
	}

	n, err := rdb.LLen(context.Background(), hubQueueKey).Result()
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("%d queue messages pushed; the create should not have reached the hub", n)
	}
	if got := readRoute(t, g, "play.example.com"); got.OwnerID != "victim" {
		t.Errorf("owner_id = %q, want victim", got.OwnerID)
	}
}

// Managed-vs-managed must NOT be refused here. The hub's unique index already
// keeps the first route, and a deleted managed route's Redis key outlives the
// delete (the zombie sweep runs only on the leader, up to five minutes later) -
// so a check on this path would reject the legitimate delete-then-recreate.
func TestCreateServerRoute_StillQueuesOverAManagedRoutesKey(t *testing.T) {
	g, rdb, fs := newHubBridgeTestGateway(t)
	fs.nodes[1] = models.Node{ID: 1, Token: "node-tok-1"}
	fs.servers[10] = models.Server{ID: 10, UUID: "srv-uuid-10", NodeID: 1}

	managed, err := json.Marshal(map[string]interface{}{
		"tunnel_id": "old-tok", "target_ip": "mc_old-uuid", "target_port": 25565,
		"server_uuid": "old-uuid",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := rdb.Set(context.Background(), "route:mc.example.com", managed, 0).Err(); err != nil {
		t.Fatal(err)
	}

	if err := g.CreateServerRoute(10, "owner-1", "mc.example.com", 25565); err != nil {
		t.Fatalf("CreateServerRoute: %v", err)
	}
	if msgs := readHubQueueMessages(t, rdb); len(msgs) != 1 {
		t.Fatalf("got %d queue messages, want 1", len(msgs))
	}
}

// Re-creating your OWN route has to keep working: it is how a tenant changes the
// target host or port of a route they already hold.
func TestCreateRouteViaLink_OwnerMayRewriteTheirOwnRoute(t *testing.T) {
	g, _, _ := newHubBridgeTestGateway(t)

	if err := g.CreateRouteViaLink("owner-2", "play.example.com", "tok", "192.168.1.50", 25565); err != nil {
		t.Fatalf("first create: %v", err)
	}
	if err := g.CreateRouteViaLink("owner-2", "play.example.com", "tok", "192.168.1.77", 25566); err != nil {
		t.Fatalf("owner re-create of their own route: %v", err)
	}

	got := readRoute(t, g, "play.example.com")
	if got.TargetIP != "192.168.1.77" || got.TargetPort != 25566 {
		t.Errorf("route = %+v, want the owner's new target", got)
	}
}
