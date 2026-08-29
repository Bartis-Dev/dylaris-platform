package services

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/redis/go-redis/v9"

	"dylaris-core/pkg/leader"
	"dylaris-core/store"
)

// republishInterval is how often the durable route-only records are compared
// against the live routing table.
//
// A minute, matching the hub's own sync cadence: this is a repair loop, not the
// write path. Creating a route publishes it immediately and does not wait for a
// tick, so the only thing this interval bounds is how long a route stays gone
// after Redis lost it - which previously was forever.
const republishInterval = 60 * time.Second

type routeRepublishStore interface {
	ListCoreLinkRoutes() ([]store.CoreLinkRoute, error)
}

// RouteRepublisher writes the stored route-only entries back into Redis.
//
// Route-only routes are published by Core straight into Redis, because the edge
// reaches the tenant's own link over the tenant's own tunnel and no hub row
// describes that. The consequence was that route:<domain> was their ONLY copy:
// the hub rebuilds its own routes from its rows every sync tick and skips these
// by design, so a Redis restart without persistence, a recreated volume or an
// eviction took every route-only route away for good while every managed route
// came back within the minute. An operator saw it as "after a redeploy the
// route is sometimes just gone".
//
// This is the writer that was missing. It never deletes and never invents: it
// restores a domain that has no entry, and corrects one whose entry disagrees
// with the row - and it leaves anything owned by somebody else exactly where it
// is, saying so once per pass.
type RouteRepublisher struct {
	store  routeRepublishStore
	redis  *redis.Client
	leader leader.Election
}

func NewRouteRepublisher(s routeRepublishStore, r *redis.Client) *RouteRepublisher {
	return &RouteRepublisher{store: s, redis: r}
}

func (s *RouteRepublisher) SetLeader(l leader.Election) { s.leader = l }

func (s *RouteRepublisher) Start(ctx context.Context) {
	log.Printf("Route republisher started (interval: %s)", republishInterval)
	s.RunOnce(ctx)
	ticker := time.NewTicker(republishInterval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.RunOnce(ctx)
			}
		}
	}()
}

// RunOnce reconciles every stored route once. Exported for tests and for a
// caller that wants the routing table current right now.
func (s *RouteRepublisher) RunOnce(ctx context.Context) {
	if s.leader != nil && !s.leader.IsLeader() {
		return
	}
	if s.redis == nil || s.store == nil {
		return
	}
	rows, err := s.store.ListCoreLinkRoutes()
	if err != nil {
		// Reading it as "no routes" would be a silent no-op every minute, and
		// this loop only ever exists for the case where something already went
		// wrong.
		log.Printf("[routes] republish: list stored routes: %v", err)
		return
	}
	restored, corrected := 0, 0
	for _, r := range rows {
		switch s.republish(ctx, r) {
		case republishRestored:
			restored++
		case republishCorrected:
			corrected++
		}
	}
	if restored > 0 || corrected > 0 {
		log.Printf("[routes] republished %d missing and corrected %d drifted route-only entries", restored, corrected)
	}
}

type republishOutcome int

const (
	republishUnchanged republishOutcome = iota
	republishRestored
	republishCorrected
	republishSkipped
)

func (s *RouteRepublisher) republish(ctx context.Context, r store.CoreLinkRoute) republishOutcome {
	data, err := json.Marshal(map[string]interface{}{
		"domain":      r.Domain,
		"target_ip":   r.TargetHost,
		"target_port": r.TargetPort,
		"tunnel_id":   r.LinkToken,
		"server_uuid": "",
		"core_owned":  true,
		"owner_id":    r.OwnerID,
	})
	if err != nil {
		return republishSkipped
	}

	// SETNX, the same claim the create path makes, so restoring a route can
	// never take a domain from whoever holds it now.
	claimed, err := s.redis.SetNX(ctx, "route:"+r.Domain, data, coreOwnedRouteTTL).Result()
	if err != nil {
		log.Printf("[routes] republish %s: %v", r.Domain, err)
		return republishSkipped
	}
	if claimed {
		if err := s.redis.SAdd(ctx, "sys:index:routes", r.Domain).Err(); err != nil {
			log.Printf("[routes] republish %s: index: %v", r.Domain, err)
		}
		log.Printf("[routes] restored route-only entry %s (it was missing from Redis)", r.Domain)
		return republishRestored
	}

	live, err := s.redis.Get(ctx, "route:"+r.Domain).Result()
	if err != nil {
		return republishSkipped
	}
	if live == string(data) {
		// Still index it: the value can be present while the set that lists it
		// is not, and every listing in the platform reads the set.
		s.redis.SAdd(ctx, "sys:index:routes", r.Domain)
		return republishUnchanged
	}
	var holder struct {
		CoreOwned bool   `json:"core_owned"`
		OwnerID   string `json:"owner_id"`
	}
	if json.Unmarshal([]byte(live), &holder) != nil || !holder.CoreOwned || holder.OwnerID != r.OwnerID {
		// Somebody else's entry, or a managed route the hub wrote. Never
		// overwritten from here: this loop repairs, and a domain held by
		// another owner is a conflict for a human to resolve, not a race for
		// two writers to keep flipping every minute.
		log.Printf("[routes] not republishing %s: it is held by another route now", r.Domain)
		return republishSkipped
	}
	if err := s.redis.Set(ctx, "route:"+r.Domain, data, coreOwnedRouteTTL).Err(); err != nil {
		log.Printf("[routes] republish %s: %v", r.Domain, err)
		return republishSkipped
	}
	s.redis.SAdd(ctx, "sys:index:routes", r.Domain)
	return republishCorrected
}
