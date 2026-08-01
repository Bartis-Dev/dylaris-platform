package services

import (
	"context"
	"dylaris-core/pkg/leader"
	"encoding/json"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// DNSReconciler keeps each region's edge wildcard A record in sync with the live
// edge IPs in that region. Declarative: desired = IPs of the online edges that
// advertise a wildcard, actual = the provider's current A records for that name;
// it creates missing and deletes stale records.
//
// Leader-gated (one Core writes DNS). Configuration is resolved on every tick
// rather than at construction, so a provider or zone changed in the panel takes
// effect within one interval instead of at the next restart.
//
// Safety: a wildcard is only touched while at least one online edge advertises
// it, so a whole-region outage leaves the last-known records in place (better
// stale than a blackholed region) rather than deleting everything.
type DNSReconciler struct {
	redis    *redis.Client
	resolver *DNSConfigResolver
	leader   leader.Election
	interval time.Duration
	// relaySource reads the live beam relays. Injected because the registry
	// reader lives in the handlers package, which imports this one - passing the
	// function in keeps the dependency pointing one way.
	relaySource func(context.Context) []RelayAdvert

	// Provider cache. Rebuilt only when the credential or provider actually
	// changes, so a stable configuration costs one hash per tick.
	provider    DNSProvider
	providerFP  string
	providerErr error
}

// DNSStatusKey holds the last reconciler outcome, written by the leader and read
// by every Core's settings API. Without it a misconfigured token fails silently
// in a log nobody is tailing.
const DNSStatusKey = "dylaris:dns:status"

// DNSReconcilerStatus is what the panel shows about the last run.
type DNSReconcilerStatus struct {
	LastRunAt    time.Time `json:"lastRunAt"`
	OK           bool      `json:"ok"`
	Error        string    `json:"error,omitempty"`
	ManagedNames []string  `json:"managedNames"`
	RecordCount  int       `json:"recordCount"`
}

func NewDNSReconciler(r *redis.Client, resolver *DNSConfigResolver) *DNSReconciler {
	return &DNSReconciler{
		redis:    r,
		resolver: resolver,
		interval: 30 * time.Second,
	}
}

func (d *DNSReconciler) SetLeader(l leader.Election) { d.leader = l }

// SetRelaySource wires the beam relay registry reader. Without one the
// reconciler manages edge names only, which is the correct behaviour for a
// deployment with no relays.
func (d *DNSReconciler) SetRelaySource(fn func(context.Context) []RelayAdvert) {
	d.relaySource = fn
}

func (d *DNSReconciler) Start(ctx context.Context) {
	if d.redis == nil || d.resolver == nil {
		return
	}
	log.Println("DNS Reconciler started (regional edge wildcards)")
	ticker := time.NewTicker(d.interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if d.leader != nil && !d.leader.IsLeader() {
					continue
				}
				d.reconcile(ctx)
			}
		}
	}()
}

// ensureProvider builds the provider for the current configuration, reusing the
// cached one while the credential and provider are unchanged. Returns nil when
// DNS is not configured, which is the normal state for a self-hoster.
func (d *DNSReconciler) ensureProvider(cfg DNSConfig) (DNSProvider, error) {
	if !cfg.Complete() {
		d.provider, d.providerFP, d.providerErr = nil, "", nil
		return nil, nil
	}
	if fp := cfg.fingerprint(); fp != d.providerFP {
		d.provider, d.providerErr = NewDNSProvider(cfg.Provider, cfg.Token)
		d.providerFP = fp
	}
	return d.provider, d.providerErr
}

func (d *DNSReconciler) reconcile(ctx context.Context) {
	cfg := d.resolver.Resolve()
	provider, err := d.ensureProvider(cfg)
	if err != nil {
		d.writeStatus(ctx, DNSReconcilerStatus{
			LastRunAt: time.Now().UTC(),
			Error:     "provider could not be built: " + err.Error(),
		})
		return
	}
	if provider == nil {
		return // not configured - nothing to report and nothing to do
	}

	edges := GetEdgesFromRedis(ctx, d.redis)
	var relays []RelayAdvert
	if d.relaySource != nil {
		relays = d.relaySource(ctx)
	}
	plan := BuildDNSPlan(edges, relays, cfg.RegionNames, cfg.Zones)
	if len(plan.Names) == 0 && len(plan.Unroutable) == 0 {
		return // nothing advertised anywhere — never touch DNS
	}

	now := time.Now().UTC()
	written, failures, status := applyDNSPlan(ctx, provider, plan)
	status.LastRunAt = now

	if orphanFailures := d.sweepOrphans(ctx, provider, written, now, cfg.OrphanGrace); len(orphanFailures) > 0 {
		failures = append(failures, orphanFailures...)
	}

	sort.Strings(status.ManagedNames)
	if len(failures) > 0 {
		sort.Strings(failures)
		status.OK = false
		status.Error = strings.Join(failures, "; ")
	}
	d.writeStatus(ctx, status)
}

// applyDNSPlan brings each planned name in line with what the provider holds:
// create the addresses that are missing, remove the ones that no longer belong.
// Pure in the sense that matters - it takes a plan and a provider and touches
// nothing else - so the create/delete rules can be tested without Redis.
//
// Returns the names that were written CLEANLY, which is what the caller records
// as owned. A name whose create or delete failed is deliberately left out: it
// must not be claimed, because a claim is what makes a name deletable later.
func applyDNSPlan(ctx context.Context, provider DNSProvider, plan DNSPlan) (DNSPlan, []string, DNSReconcilerStatus) {
	status := DNSReconcilerStatus{OK: true}
	var failures []string

	for _, n := range plan.Unroutable {
		// Reported, never written: without a managed zone there is nowhere to
		// put it, and picking one would mean writing into a zone the operator
		// never released.
		failures = append(failures, n.Name+": outside every managed zone")
	}

	written := DNSPlan{}
	for _, n := range plan.Names {
		status.ManagedNames = append(status.ManagedNames, n.Name)
		status.RecordCount += len(n.IPs)

		wantIPs := map[string]bool{}
		for _, ip := range n.IPs {
			wantIPs[ip] = true
		}

		actual, err := provider.ListA(ctx, n.Zone, n.Name)
		if err != nil {
			log.Printf("dns reconciler: list %s: %v", n.Name, err)
			failures = append(failures, n.Name+": "+err.Error())
			continue // never delete on a read failure
		}
		haveIPs := map[string]bool{}
		for _, r := range actual {
			haveIPs[r.IP] = true
		}
		nameOK := true
		// Create missing.
		for ip := range wantIPs {
			if haveIPs[ip] {
				continue
			}
			if err := provider.CreateA(ctx, n.Zone, n.Name, ip); err != nil {
				log.Printf("dns reconciler: create %s -> %s: %v", n.Name, ip, err)
				failures = append(failures, n.Name+" -> "+ip+": "+err.Error())
				nameOK = false
			} else {
				log.Printf("dns reconciler: created %s -> %s", n.Name, ip)
			}
		}
		// Delete stale (records pointing at an IP that is no longer a live edge).
		// Identified by name+value rather than a provider id: libdns treats
		// provider data as non-portable, and a stale id can delete the wrong row.
		for ip := range haveIPs {
			if wantIPs[ip] {
				continue
			}
			if err := provider.DeleteA(ctx, n.Zone, n.Name, ip); err != nil {
				log.Printf("dns reconciler: delete %s (%s): %v", n.Name, ip, err)
				failures = append(failures, n.Name+" -> "+ip+": "+err.Error())
				nameOK = false
			} else {
				log.Printf("dns reconciler: deleted stale %s -> %s", n.Name, ip)
			}
		}
		if nameOK {
			written.Names = append(written.Names, n)
		}
	}
	return written, failures, status
}

// sweepOrphans removes the records of names the reconciler once created and
// nothing advertises any more, once their grace period has run out.
//
// The ownership registry is the ONLY source of deletable names. A zone released
// by a hoster also carries their website and mail records, so deletion is never
// derived from "everything in the zone that is not in my desired set" - a name
// the reconciler did not create cannot be reached from here at all.
func (d *DNSReconciler) sweepOrphans(ctx context.Context, provider DNSProvider, written DNSPlan, now time.Time, grace time.Duration) []string {
	return sweepOrphansWith(ctx, provider, d.ownership(), written, now, grace)
}

// ownershipStore is the registry as the sweep needs it. Narrow on purpose: the
// deletion path is the one place where a mistake removes a record that is not
// ours, so it has to be testable without standing up Redis.
type ownershipStore interface {
	load(ctx context.Context) (map[string]OwnedName, error)
	save(ctx context.Context, owned map[string]OwnedName) error
}

// redisOwnership is the production implementation, backed by DNSOwnershipKey.
type redisOwnership struct{ d *DNSReconciler }

func (r redisOwnership) load(ctx context.Context) (map[string]OwnedName, error) {
	return r.d.loadOwnership(ctx)
}

func (r redisOwnership) save(ctx context.Context, owned map[string]OwnedName) error {
	return r.d.saveOwnership(ctx, owned)
}

func (d *DNSReconciler) ownership() ownershipStore { return redisOwnership{d: d} }

func sweepOrphansWith(ctx context.Context, provider DNSProvider, registry ownershipStore, written DNSPlan, now time.Time, grace time.Duration) []string {
	owned, err := registry.load(ctx)
	if err != nil {
		// Without the registry there is no proof of ownership, so nothing may be
		// deleted this pass. Creates already happened and are unaffected.
		log.Printf("dns reconciler: ownership registry unreadable, skipping orphan sweep: %v", err)
		return nil
	}

	var failures []string
	for _, orphan := range PlanOrphans(owned, written.AdvertisedNames(), now, grace) {
		records, err := provider.ListA(ctx, orphan.Zone, orphan.Name)
		if err != nil {
			log.Printf("dns reconciler: list orphan %s: %v", orphan.Name, err)
			failures = append(failures, orphan.Name+": "+err.Error())
			continue // never delete on a read failure
		}
		removed := true
		for _, rec := range records {
			if err := provider.DeleteA(ctx, orphan.Zone, orphan.Name, rec.IP); err != nil {
				log.Printf("dns reconciler: delete orphan %s (%s): %v", orphan.Name, rec.IP, err)
				failures = append(failures, orphan.Name+" -> "+rec.IP+": "+err.Error())
				removed = false
			} else {
				log.Printf("dns reconciler: removed orphaned %s -> %s", orphan.Name, rec.IP)
			}
		}
		// Only drop the claim once every record is actually gone, so a partial
		// failure is retried next pass instead of leaking a record that nothing
		// remembers owning.
		if removed {
			delete(owned, orphan.Name)
		}
	}

	if err := registry.save(ctx, RefreshOwnership(owned, written, now)); err != nil {
		log.Printf("dns reconciler: could not persist ownership registry: %v", err)
	}
	return failures
}

// DNSOwnershipKey is the registry of names the reconciler created, and the only
// thing that makes a name deletable.
const DNSOwnershipKey = "dylaris:dns:owned"

func (d *DNSReconciler) loadOwnership(ctx context.Context) (map[string]OwnedName, error) {
	raw, err := d.redis.HGetAll(ctx, DNSOwnershipKey).Result()
	if err != nil {
		return nil, err
	}
	owned := make(map[string]OwnedName, len(raw))
	for name, val := range raw {
		var entry OwnedName
		if json.Unmarshal([]byte(val), &entry) == nil {
			owned[name] = entry
		}
	}
	return owned, nil
}

func (d *DNSReconciler) saveOwnership(ctx context.Context, owned map[string]OwnedName) error {
	pipe := d.redis.TxPipeline()
	pipe.Del(ctx, DNSOwnershipKey)
	if len(owned) > 0 {
		fields := make(map[string]any, len(owned))
		for name, entry := range owned {
			raw, err := json.Marshal(entry)
			if err != nil {
				continue
			}
			fields[name] = raw
		}
		if len(fields) > 0 {
			pipe.HSet(ctx, DNSOwnershipKey, fields)
		}
	}
	_, err := pipe.Exec(ctx)
	return err
}

// writeStatus publishes the last outcome. Best-effort: a Redis hiccup must not
// affect the reconciliation that already happened.
func (d *DNSReconciler) writeStatus(ctx context.Context, s DNSReconcilerStatus) {
	if d.redis == nil {
		return
	}
	if s.ManagedNames == nil {
		s.ManagedNames = []string{}
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return
	}
	if err := d.redis.Set(ctx, DNSStatusKey, raw, 0).Err(); err != nil {
		log.Printf("dns reconciler: could not publish status: %v", err)
	}
}

// LoadDNSStatus reads the last published reconciler outcome. Returns nil when
// the reconciler has never completed a run, which the caller must present as
// "not run yet" rather than as a failure.
func LoadDNSStatus(ctx context.Context, r *redis.Client) *DNSReconcilerStatus {
	if r == nil {
		return nil
	}
	raw, err := r.Get(ctx, DNSStatusKey).Result()
	if err != nil || raw == "" {
		return nil
	}
	var s DNSReconcilerStatus
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return nil
	}
	return &s
}
