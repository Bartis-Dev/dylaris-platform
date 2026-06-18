package services

import (
	"context"
	"dylaris-core/pkg/leader"
	"dylaris-core/store"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// Traffic metering keys (written by the edge + beam relay, read here).
//
//	dylaris:traffic:edge:{serverUUID}   HASH {rx,tx}   monotonic player bytes
//	dylaris:traffic:relay:{serverUUID}  HASH {up,down}  monotonic filebrowser bytes
//
// Each writer flushes positive DELTAS via HINCRBY, so the Redis value is the
// cumulative byte count for that server (across edge restarts and multiple
// edges). The aggregator stores the last value it has already billed in
//
//	dylaris:traffic:agg:seen:{kind}:{serverUUID}
//
// and adds (current - seen) to the tenant's monthly row, so it is idempotent and
// loss-tolerant: a tick that fails to write the DB simply leaves `seen` untouched
// and re-reads the same delta next tick.
const (
	trafficEdgePrefix  = "dylaris:traffic:edge:"
	trafficRelayPrefix = "dylaris:traffic:relay:"
	trafficSeenPrefix  = "dylaris:traffic:agg:seen:"
	trafficSeenTTL     = 30 * 24 * time.Hour
	trafficScanCount   = 200
)

// TrafficAggregator turns the per-server byte counters edges + relays publish to
// Redis into per-tenant monthly rows in traffic_usage. Leader-gated (one Core
// bills) and BYON-gated (no tenants = nothing to meter), so it is a no-op in
// solo/hoster mode.
type TrafficAggregator struct {
	store    store.Store
	redis    *redis.Client
	flags    *FeatureFlags
	leader   leader.Election
	interval time.Duration
}

func NewTrafficAggregator(st store.Store, r *redis.Client, flags *FeatureFlags) *TrafficAggregator {
	return &TrafficAggregator{store: st, redis: r, flags: flags, interval: 60 * time.Second}
}

// SetLeader wires the leader-election gate. Call once at boot.
func (a *TrafficAggregator) SetLeader(l leader.Election) { a.leader = l }

func (a *TrafficAggregator) Start(ctx context.Context) {
	if a.redis == nil {
		return
	}
	log.Println("Traffic Aggregator started (BYON metering)")
	ticker := time.NewTicker(a.interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if a.leader != nil && !a.leader.IsLeader() {
					continue
				}
				a.runOnce(ctx)
			}
		}
	}()
}

// trafficAcc accumulates one tenant's new bytes within a single tick.
type trafficAcc struct {
	edge  int64
	relay int64
}

// seenUpdate is a `seen` key write deferred until the tenant's DB row is written.
type seenUpdate struct {
	key string
	val int64
}

func (a *TrafficAggregator) runOnce(ctx context.Context) {
	if !a.flags.IsBYONEnabled(ctx) {
		return // no tenants to bill
	}
	owners, err := a.store.TenantServerOwners()
	if err != nil {
		log.Printf("traffic aggregator: tenant lookup: %v", err)
		return
	}
	if len(owners) == 0 {
		return
	}

	perTenant := map[string]*trafficAcc{}
	tenantSeen := map[string][]seenUpdate{}

	a.collect(ctx, trafficEdgePrefix, owners, perTenant, tenantSeen, true)
	a.collect(ctx, trafficRelayPrefix, owners, perTenant, tenantSeen, false)

	period := monthStartUTC(time.Now())
	for tenant, acc := range perTenant {
		if err := a.store.AddTrafficUsage(tenant, period, acc.edge, acc.relay); err != nil {
			// Leave `seen` untouched so the delta is retried next tick.
			log.Printf("traffic aggregator: add usage for %s: %v", tenant, err)
			continue
		}
		for _, su := range tenantSeen[tenant] {
			a.redis.Set(ctx, su.key, su.val, trafficSeenTTL)
		}
	}

	a.snapshotBackupStorage(owners, period)
}

// snapshotBackupStorage overwrites each tenant's R2 backup-storage gauge for the
// period. Unlike traffic (a cumulative flow), storage is a current total, so it
// is set, not added — and tenants whose backups dropped to 0 are explicitly
// reset, which is why every known tenant is written, not just those with backups.
func (a *TrafficAggregator) snapshotBackupStorage(owners map[string]string, period time.Time) {
	byTenant, err := a.store.TenantBackupBytes()
	if err != nil {
		log.Printf("traffic aggregator: backup storage lookup: %v", err)
		return
	}
	// Union of tenants that own a server and tenants that still hold backups
	// (a tenant can keep backups after deleting every server).
	tenants := map[string]struct{}{}
	for _, tenant := range owners {
		tenants[tenant] = struct{}{}
	}
	for tenant := range byTenant {
		tenants[tenant] = struct{}{}
	}
	for tenant := range tenants {
		if err := a.store.SetTrafficBackupBytes(tenant, period, byTenant[tenant]); err != nil {
			log.Printf("traffic aggregator: set backup bytes for %s: %v", tenant, err)
		}
	}
}

// collect scans one counter family, attributes each server's new bytes to its
// tenant, and records the matching `seen` advance (applied later, after the DB
// write succeeds).
func (a *TrafficAggregator) collect(ctx context.Context, prefix string, owners map[string]string, perTenant map[string]*trafficAcc, tenantSeen map[string][]seenUpdate, isEdge bool) {
	kind := "edge"
	if !isEdge {
		kind = "relay"
	}
	var cursor uint64
	for {
		keys, next, err := a.redis.Scan(ctx, cursor, prefix+"*", trafficScanCount).Result()
		if err != nil {
			log.Printf("traffic aggregator: scan %s: %v", prefix, err)
			return
		}
		for _, key := range keys {
			serverUUID := strings.TrimPrefix(key, prefix)
			tenant, ok := owners[serverUUID]
			if !ok {
				continue // platform server or unknown — not billed
			}
			fields, err := a.redis.HGetAll(ctx, key).Result()
			if err != nil {
				continue
			}
			current := sumByteFields(fields)
			seenKey := trafficSeenPrefix + kind + ":" + serverUUID
			last, _ := a.redis.Get(ctx, seenKey).Int64()
			delta := current - last
			if delta < 0 {
				delta = current // counter reset (key expired then recreated)
			}
			if delta <= 0 {
				continue
			}
			acc := perTenant[tenant]
			if acc == nil {
				acc = &trafficAcc{}
				perTenant[tenant] = acc
			}
			if isEdge {
				acc.edge += delta
			} else {
				acc.relay += delta
			}
			tenantSeen[tenant] = append(tenantSeen[tenant], seenUpdate{key: seenKey, val: current})
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
}

// sumByteFields sums every numeric value in a counter hash (rx+tx, up+down),
// ignoring unparseable fields so an extra label never poisons the total.
func sumByteFields(fields map[string]string) int64 {
	var total int64
	for _, v := range fields {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			continue
		}
		total += n
	}
	return total
}

// monthStartUTC is the first instant of t's month in UTC — the billing-period key.
func monthStartUTC(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}
