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

	// Counter subjects for route-only addresses carry this infix. It lives
	// under the edge prefix rather than beside it so the edge's existing Redis
	// ACL grant covers it - see subjectOwners.
	trafficOwnerSubjectPrefix = "owner:"

	// Region seen keys hang off the subject's own seen key. '#' separates them
	// because a SUBJECT may contain ':' ("owner:<id>") and a region may not -
	// see meterRegion on the edge, which strips everything outside [a-z0-9-].
	trafficRegionSeenSep = "#"

	// Where bytes go when the edge that wrote them did not say. It is a named
	// region rather than a dropped one: they are real bytes, they are in the
	// billing total, and a breakdown that quietly omitted them would not add up
	// to the number the tenant is charged on.
	regionUnknown = "unknown"
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
	// byRegion splits the same bytes by (region, kind). Per kind it sums to the
	// matching total above, so the breakdown always adds up to the bill; a
	// producer that did not tag contributes to regionUnknown rather than
	// vanishing from the split.
	byRegion map[regionKind]int64
}

// regionKind is one cell of the breakdown: where the bytes moved, and which
// component moved them. Player traffic and data traffic are priced and capped
// separately, so they cannot share a cell.
type regionKind struct {
	region string
	kind   string
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
	owners, err := a.subjectOwners()
	if err != nil {
		log.Printf("traffic aggregator: tenant lookup: %v", err)
		return
	}
	if len(owners) == 0 {
		return
	}

	perTenant := map[string]*trafficAcc{}
	tenantSeen := map[string][]seenUpdate{}

	a.collect(ctx, trafficEdgePrefix, owners, perTenant, tenantSeen, store.TrafficKindEdge)
	a.collect(ctx, trafficRelayPrefix, owners, perTenant, tenantSeen, store.TrafficKindRelay)

	period := monthStartUTC(time.Now())
	for tenant, acc := range perTenant {
		if err := a.store.AddTrafficUsage(tenant, period, acc.edge, acc.relay); err != nil {
			// Leave `seen` untouched so the delta is retried next tick.
			log.Printf("traffic aggregator: add usage for %s: %v", tenant, err)
			continue
		}
		// The breakdown is written AFTER the total and its failure does not
		// abort the tick: traffic_usage is what a tenant is charged on, and a
		// region row that cannot be written must never hold that back. The
		// markers still advance, so a lost region delta is lost rather than
		// re-billed - under-counting the split, never the bill.
		for rk, bytes := range acc.byRegion {
			if bytes <= 0 {
				continue
			}
			if err := a.store.AddTrafficUsageRegion(tenant, period, rk.region, rk.kind, bytes); err != nil {
				log.Printf("traffic aggregator: add region usage for %s/%s/%s: %v", tenant, rk.region, rk.kind, err)
			}
		}
		for _, su := range tenantSeen[tenant] {
			a.redis.Set(ctx, su.key, su.val, trafficSeenTTL)
		}
	}

	// Deliberately NOT the widened map: this walks tenants who own SERVERS, and
	// a route-only tenant has no backups to snapshot. Passing them in would
	// write a zero-byte storage row for an account that never had one.
	serverOwners, err := a.store.TenantServerOwners()
	if err != nil {
		log.Printf("traffic aggregator: server owner lookup for backup snapshot: %v", err)
		return
	}
	a.snapshotBackupStorage(serverOwners, period)
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

// subjectOwners maps every counter SUBJECT the edges write to the tenant it
// belongs to.
//
// Two kinds share one namespace, because they share one Redis key prefix (the
// edge's ACL grants exactly dylaris:traffic:edge:*, so route-only could not get
// a prefix of its own without a re-provisioned ACL):
//
//	<serverUUID>      a managed server on a tenant-owned node
//	owner:<userID>    a route-only address, which has no server at all
//
// Route-only used to be absent from this map because it was absent from the
// EDGE too: Core writes server_uuid:"" for those routes, metering keyed on the
// server, and the whole product's traffic reached no counter. Adding the
// counter without adding it here would have been the same defect one layer up.
//
// A subject that resolves to nothing is skipped by collect, which is what keeps
// a stale counter for a deleted server or a removed route from inventing a row.
func (a *TrafficAggregator) subjectOwners() (map[string]string, error) {
	owners, err := a.store.TenantServerOwners()
	if err != nil {
		return nil, err
	}
	if owners == nil {
		owners = map[string]string{}
	}
	routes, err := a.store.ListCoreLinkRoutes()
	if err != nil {
		return nil, err
	}
	for _, r := range routes {
		if r.OwnerID == "" {
			continue
		}
		owners[trafficOwnerSubjectPrefix+r.OwnerID] = r.OwnerID
	}
	return owners, nil
}

// collect scans one counter family, attributes each subject's new bytes to its
// tenant, and records the matching `seen` advance (applied later, after the DB
// write succeeds).
func (a *TrafficAggregator) collect(ctx context.Context, prefix string, owners map[string]string, perTenant map[string]*trafficAcc, tenantSeen map[string][]seenUpdate, kind string) {
	isEdge := kind == store.TrafficKindEdge
	var cursor uint64
	for {
		keys, next, err := a.redis.Scan(ctx, cursor, prefix+"*", trafficScanCount).Result()
		if err != nil {
			log.Printf("traffic aggregator: scan %s: %v", prefix, err)
			return
		}
		for _, key := range keys {
			subject := strings.TrimPrefix(key, prefix)
			tenant, ok := owners[subject]
			if !ok {
				continue // platform server, deleted route, or unknown — not billed
			}
			fields, err := a.redis.HGetAll(ctx, key).Result()
			if err != nil {
				continue
			}
			current := sumByteFields(fields)
			seenKey := trafficSeenPrefix + kind + ":" + subject
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
				acc = &trafficAcc{byRegion: map[regionKind]int64{}}
				perTenant[tenant] = acc
			}
			if isEdge {
				acc.edge += delta
			} else {
				acc.relay += delta
			}
			// Both kinds are split. The beam relay carries a customer's file
			// transfers and is region-aware in its own right, so leaving it out
			// would put a US customer's data traffic nowhere - which is the
			// cross-region case the split exists for.
			a.collectRegions(ctx, subject, kind, fields, last > 0, acc, tenantSeen, tenant)
			tenantSeen[tenant] = append(tenantSeen[tenant], seenUpdate{key: seenKey, val: current})
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
}

// regionBytes splits a counter hash into per-region totals.
//
// An edge new enough to tag writes "rx:<region>" / "tx:<region>"; one that is
// not writes bare "rx" / "tx", and those land in regionUnknown. Both shapes can
// be present in the same hash at once - that is what a rolling edge upgrade
// looks like - and the sum across regions equals sumByteFields either way,
// which is the property the whole breakdown rests on.
func regionBytes(fields map[string]string) map[string]int64 {
	out := map[string]int64{}
	for name, v := range fields {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			continue
		}
		_, region, tagged := strings.Cut(name, ":")
		if !tagged || region == "" {
			region = regionUnknown
		}
		out[region] += n
	}
	return out
}

// collectRegions computes the per-region deltas for one edge counter.
//
// hadTotalSeen says the subject was ALREADY being counted before this Core knew
// about regions. Its region seen keys are therefore missing not because the
// bytes are new but because nobody was tracking them yet, and treating that as
// a delta would post the whole historical counter into the breakdown in one
// tick - a split that would not match the total it is supposed to break down.
// Those subjects are seeded silently instead: the marker is set, nothing is
// billed, and the region rows start accumulating from the next tick.
//
// A genuinely new subject has no total seen either, so it takes the normal path
// and its first region delta is its full counter - which is correct, because
// the total is doing exactly the same thing on the same tick.
func (a *TrafficAggregator) collectRegions(
	ctx context.Context,
	subject, kind string,
	fields map[string]string,
	hadTotalSeen bool,
	acc *trafficAcc,
	tenantSeen map[string][]seenUpdate,
	tenant string,
) {
	for region, current := range regionBytes(fields) {
		seenKey := trafficSeenPrefix + kind + ":" + subject + trafficRegionSeenSep + region
		last, err := a.redis.Get(ctx, seenKey).Int64()
		missing := err != nil
		if missing && hadTotalSeen {
			// Seed, do not bill. Deferred like every other marker so a failed
			// DB write leaves it unset and the seeding is retried.
			tenantSeen[tenant] = append(tenantSeen[tenant], seenUpdate{key: seenKey, val: current})
			continue
		}
		delta := current - last
		if delta < 0 {
			delta = current
		}
		if delta <= 0 {
			continue
		}
		if acc.byRegion == nil {
			acc.byRegion = map[regionKind]int64{}
		}
		acc.byRegion[regionKind{region: region, kind: kind}] += delta
		tenantSeen[tenant] = append(tenantSeen[tenant], seenUpdate{key: seenKey, val: current})
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
