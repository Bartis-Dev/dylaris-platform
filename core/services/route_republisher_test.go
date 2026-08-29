package services

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"dylaris-core/store"
)

type republishFakeStore struct {
	rows []store.CoreLinkRoute
	err  error
}

func (f *republishFakeStore) ListCoreLinkRoutes() ([]store.CoreLinkRoute, error) {
	return f.rows, f.err
}

func (f *republishFakeStore) UpsertCoreLinkRoute(r store.CoreLinkRoute) error {
	for i := range f.rows {
		if f.rows[i].Domain == r.Domain {
			f.rows[i] = r
			return nil
		}
	}
	f.rows = append(f.rows, r)
	return nil
}

func liveRoute(t *testing.T, rp *RouteRepublisher, domain string) GatewayRoute {
	t.Helper()
	val, err := rp.redis.Get(context.Background(), "route:"+domain).Result()
	if err != nil {
		t.Fatalf("route:%s not in redis: %v", domain, err)
	}
	var r GatewayRoute
	if err := json.Unmarshal([]byte(val), &r); err != nil {
		t.Fatalf("route:%s is not valid JSON: %v", domain, err)
	}
	return r
}

func indexed(t *testing.T, rp *RouteRepublisher, domain string) bool {
	t.Helper()
	ok, err := rp.redis.SIsMember(context.Background(), "sys:index:routes", domain).Result()
	if err != nil {
		t.Fatalf("index read: %v", err)
	}
	return ok
}

// The whole point. A route-only entry that Redis no longer has comes back, with
// the target and the tunnel the row records, and in the index every listing on
// the platform reads.
func TestRepublish_RestoresARouteRedisLost(t *testing.T) {
	rdb := newQueueTestRedis(t)
	fs := &republishFakeStore{rows: []store.CoreLinkRoute{
		{Domain: "play.example.com", OwnerID: "owner-1", LinkToken: "tunnel-1", TargetHost: "127.0.0.1", TargetPort: 25565},
	}}
	rp := NewRouteRepublisher(fs, rdb)

	rp.RunOnce(context.Background())

	got := liveRoute(t, rp, "play.example.com")
	if !got.CoreOwned || got.OwnerID != "owner-1" || got.TunnelID != "tunnel-1" {
		t.Errorf("restored route = %+v, want owner-1 / tunnel-1 / core-owned", got)
	}
	if got.TargetIP != "127.0.0.1" || got.TargetPort != 25565 {
		t.Errorf("restored target = %s:%d, want 127.0.0.1:25565", got.TargetIP, got.TargetPort)
	}
	if !indexed(t, rp, "play.example.com") {
		t.Error("restored route is not in sys:index:routes, so nothing lists it")
	}
}

// It must never take a domain away from whoever holds it now. A tenant whose
// route was deleted and whose address someone else has since taken keeps their
// row until an operator resolves it, and the live entry is left alone.
func TestRepublish_LeavesADomainHeldByAnotherOwner(t *testing.T) {
	rdb := newQueueTestRedis(t)
	seedGatewayRoute(t, rdb, "taken.example.com", GatewayRoute{
		CoreOwned: true, OwnerID: "owner-2", TunnelID: "tunnel-2", TargetIP: "10.0.0.9", TargetPort: 25577,
	})
	fs := &republishFakeStore{rows: []store.CoreLinkRoute{
		{Domain: "taken.example.com", OwnerID: "owner-1", LinkToken: "tunnel-1", TargetHost: "127.0.0.1", TargetPort: 25565},
	}}
	rp := NewRouteRepublisher(fs, rdb)

	rp.RunOnce(context.Background())

	got := liveRoute(t, rp, "taken.example.com")
	if got.OwnerID != "owner-2" || got.TunnelID != "tunnel-2" {
		t.Errorf("live route = %+v, want owner-2's entry untouched", got)
	}
}

// A managed server's route is written by the hub and carries no core_owned
// flag. Overwriting one from here would point a paying customer's players at
// somebody's home PC, so the same guard covers it.
func TestRepublish_LeavesAHubManagedRoute(t *testing.T) {
	rdb := newQueueTestRedis(t)
	seedGatewayRoute(t, rdb, "srv.example.com", GatewayRoute{
		CoreOwned: false, TunnelID: "node-tunnel", TargetIP: "mc_abc", TargetPort: 25565,
	})
	fs := &republishFakeStore{rows: []store.CoreLinkRoute{
		{Domain: "srv.example.com", OwnerID: "owner-1", LinkToken: "tunnel-1", TargetHost: "127.0.0.1", TargetPort: 25565},
	}}
	rp := NewRouteRepublisher(fs, rdb)

	rp.RunOnce(context.Background())

	if got := liveRoute(t, rp, "srv.example.com"); got.TargetIP != "mc_abc" {
		t.Errorf("live route = %+v, want the hub's managed entry untouched", got)
	}
}

// A live entry of the tenant's own that disagrees with the row is corrected:
// the row is the record, and the entry drifting from it is how a target change
// that half-applied would otherwise stay wrong forever.
func TestRepublish_CorrectsTheTenantsOwnDriftedEntry(t *testing.T) {
	rdb := newQueueTestRedis(t)
	seedGatewayRoute(t, rdb, "play.example.com", GatewayRoute{
		CoreOwned: true, OwnerID: "owner-1", TunnelID: "tunnel-1", TargetIP: "127.0.0.1", TargetPort: 25000,
	})
	fs := &republishFakeStore{rows: []store.CoreLinkRoute{
		{Domain: "play.example.com", OwnerID: "owner-1", LinkToken: "tunnel-1", TargetHost: "127.0.0.1", TargetPort: 25565},
	}}
	rp := NewRouteRepublisher(fs, rdb)

	rp.RunOnce(context.Background())

	if got := liveRoute(t, rp, "play.example.com"); got.TargetPort != 25565 {
		t.Errorf("target port = %d, want the stored 25565", got.TargetPort)
	}
}

// A failed listing is not an empty one. Nothing is written, and in particular
// nothing is treated as absent.
func TestRepublish_ListFails_WritesNothing(t *testing.T) {
	rdb := newQueueTestRedis(t)
	fs := &republishFakeStore{err: errors.New("db down"), rows: []store.CoreLinkRoute{
		{Domain: "play.example.com", OwnerID: "owner-1", LinkToken: "tunnel-1", TargetHost: "127.0.0.1", TargetPort: 25565},
	}}
	rp := NewRouteRepublisher(fs, rdb)

	rp.RunOnce(context.Background())

	if n, _ := rdb.Exists(context.Background(), "route:play.example.com").Result(); n != 0 {
		t.Error("a route was published from a listing that failed")
	}
}

// Only the leader repairs. Every replica writing the same rows every minute
// would be harmless but pointless; the gate is the same one the warp registrar
// and the hub's own sweep take, and it is what keeps this from racing a create
// happening on another replica.
func TestRepublish_NonLeaderDoesNothing(t *testing.T) {
	rdb := newQueueTestRedis(t)
	fs := &republishFakeStore{rows: []store.CoreLinkRoute{
		{Domain: "play.example.com", OwnerID: "owner-1", LinkToken: "tunnel-1", TargetHost: "127.0.0.1", TargetPort: 25565},
	}}
	rp := NewRouteRepublisher(fs, rdb)
	rp.SetLeader(notTheLeader{})

	rp.RunOnce(context.Background())

	if n, _ := rdb.Exists(context.Background(), "route:play.example.com").Result(); n != 0 {
		t.Error("a non-leader replica published routes")
	}
}

// Every route-only address that existed before this table did has to be
// recorded, or the repair loop protects only the ones created afterwards and an
// operator cannot tell which of theirs are covered.
func TestAdoptExisting_RecordsRoutesThatPredateTheTable(t *testing.T) {
	rdb := newQueueTestRedis(t)
	seedGatewayRoute(t, rdb, "old.example.com", GatewayRoute{
		CoreOwned: true, OwnerID: "owner-1", TunnelID: "tunnel-1", TargetIP: "192.168.1.50", TargetPort: 25566,
	})
	fs := &republishFakeStore{}
	rp := NewRouteRepublisher(fs, rdb)

	rp.adoptExisting(context.Background())

	if len(fs.rows) != 1 {
		t.Fatalf("stored %d rows, want 1", len(fs.rows))
	}
	got := fs.rows[0]
	if got.Domain != "old.example.com" || got.OwnerID != "owner-1" || got.LinkToken != "tunnel-1" {
		t.Errorf("adopted row = %+v", got)
	}
	if got.TargetHost != "192.168.1.50" || got.TargetPort != 25566 {
		t.Errorf("adopted target = %s:%d, want 192.168.1.50:25566", got.TargetHost, got.TargetPort)
	}
}

// A hub-managed route already has a durable row in the hub's own database.
// Copying it here would give one route two owners that both write it.
func TestAdoptExisting_IgnoresHubManagedRoutes(t *testing.T) {
	rdb := newQueueTestRedis(t)
	seedGatewayRoute(t, rdb, "srv.example.com", GatewayRoute{
		CoreOwned: false, TunnelID: "node-tunnel", TargetIP: "mc_abc", TargetPort: 25565,
	})
	fs := &republishFakeStore{}
	rp := NewRouteRepublisher(fs, rdb)

	rp.adoptExisting(context.Background())

	if len(fs.rows) != 0 {
		t.Errorf("adopted %+v, want nothing: the hub owns that route", fs.rows)
	}
}

// A route already recorded is left exactly as it is. Re-reading it from Redis
// would let a drifted cache entry overwrite the record it is supposed to be
// corrected BY, which is the wrong way round.
func TestAdoptExisting_DoesNotOverwriteAnExistingRow(t *testing.T) {
	rdb := newQueueTestRedis(t)
	seedGatewayRoute(t, rdb, "play.example.com", GatewayRoute{
		CoreOwned: true, OwnerID: "owner-1", TunnelID: "tunnel-1", TargetIP: "10.0.0.9", TargetPort: 25000,
	})
	fs := &republishFakeStore{rows: []store.CoreLinkRoute{
		{Domain: "play.example.com", OwnerID: "owner-1", LinkToken: "tunnel-1", TargetHost: "127.0.0.1", TargetPort: 25565},
	}}
	rp := NewRouteRepublisher(fs, rdb)

	rp.adoptExisting(context.Background())

	if len(fs.rows) != 1 || fs.rows[0].TargetPort != 25565 {
		t.Errorf("rows = %+v, want the stored record untouched", fs.rows)
	}
}

// A failed listing would make every domain look unrecorded, and this writes
// rows - so it writes none.
func TestAdoptExisting_ListFails_RecordsNothing(t *testing.T) {
	rdb := newQueueTestRedis(t)
	seedGatewayRoute(t, rdb, "old.example.com", GatewayRoute{
		CoreOwned: true, OwnerID: "owner-1", TunnelID: "tunnel-1", TargetIP: "127.0.0.1", TargetPort: 25565,
	})
	fs := &republishFakeStore{err: errors.New("db down")}
	rp := NewRouteRepublisher(fs, rdb)

	rp.adoptExisting(context.Background())

	if len(fs.rows) != 0 {
		t.Errorf("rows = %+v, want none written from a failed listing", fs.rows)
	}
}
