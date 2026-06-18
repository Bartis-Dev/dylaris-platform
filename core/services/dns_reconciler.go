package services

import (
	"context"
	"dylaris-core/pkg/leader"
	"log"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// DNSReconciler keeps each region's edge wildcard A record in sync with the live
// edge IPs in that region. Declarative: desired = IPs of the online edges that
// advertise a wildcard, actual = the provider's current A records for that name;
// it creates missing and deletes stale records.
//
// Leader-gated (one Core writes DNS) and only constructed when a provider is
// configured. Safety: a wildcard is only touched while at least one online edge
// advertises it, so a whole-region outage leaves the last-known records in place
// (better stale than a blackholed region) rather than deleting everything.
type DNSReconciler struct {
	redis    *redis.Client
	provider DNSProvider
	leader   leader.Election
	interval time.Duration
}

func NewDNSReconciler(r *redis.Client, provider DNSProvider) *DNSReconciler {
	return &DNSReconciler{redis: r, provider: provider, interval: 30 * time.Second}
}

func (d *DNSReconciler) SetLeader(l leader.Election) { d.leader = l }

func (d *DNSReconciler) Start(ctx context.Context) {
	if d.provider == nil || d.redis == nil {
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

func (d *DNSReconciler) reconcile(ctx context.Context) {
	edges := GetEdgesFromRedis(ctx, d.redis)

	// desired: wildcard name -> set of edge IPs that should answer for it.
	desired := map[string]map[string]bool{}
	for _, e := range edges {
		if e.Status != "online" {
			continue
		}
		wildcard := strings.TrimSpace(e.Wildcard)
		ip := strings.TrimSpace(e.IP)
		if wildcard == "" || ip == "" {
			continue
		}
		if desired[wildcard] == nil {
			desired[wildcard] = map[string]bool{}
		}
		desired[wildcard][ip] = true
	}
	if len(desired) == 0 {
		return // no regional edges advertising a wildcard — never touch DNS
	}

	for wildcard, wantIPs := range desired {
		actual, err := d.provider.ListA(ctx, wildcard)
		if err != nil {
			log.Printf("dns reconciler: list %s: %v", wildcard, err)
			continue // never delete on a read failure
		}
		haveIPs := map[string]string{} // ip -> record id
		for _, r := range actual {
			haveIPs[r.Content] = r.ID
		}
		// Create missing.
		for ip := range wantIPs {
			if _, ok := haveIPs[ip]; ok {
				continue
			}
			if err := d.provider.CreateA(ctx, wildcard, ip); err != nil {
				log.Printf("dns reconciler: create %s -> %s: %v", wildcard, ip, err)
			} else {
				log.Printf("dns reconciler: created %s -> %s", wildcard, ip)
			}
		}
		// Delete stale (records pointing at an IP that is no longer a live edge).
		for ip, id := range haveIPs {
			if wantIPs[ip] {
				continue
			}
			if err := d.provider.DeleteRecord(ctx, id); err != nil {
				log.Printf("dns reconciler: delete %s (%s): %v", wildcard, ip, err)
			} else {
				log.Printf("dns reconciler: deleted stale %s -> %s", wildcard, ip)
			}
		}
	}
}
