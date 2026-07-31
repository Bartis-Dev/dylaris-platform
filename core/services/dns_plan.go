package services

import (
	"sort"
	"strings"
	"time"
)

// Planning for the DNS reconciler, kept free of Redis and provider calls so the
// rules that decide what gets WRITTEN and what gets DELETED are testable on
// their own. Everything here is a pure function.
//
// The governing risk: once a hoster releases a zone that also carries their
// website and mail records, "delete everything in the zone that is not in my
// desired set" becomes catastrophic. So deletion is never derived from a zone
// listing. A name can only ever be deleted if the reconciler recorded creating
// it, and the plan below is the only thing that decides what to record.

// Origin says where a managed name came from, so the panel can show it. The
// distinction matters: an admin who ticks domains in the panel while a leftover
// EDGE_WILDCARD keeps winning would otherwise see a screen that looks correct.
const (
	DNSOriginPanel = "panel"
	DNSOriginEdge  = "edge"
	// A relay's name always comes from its own BEAM_PUBLIC_HOST. Relays are
	// deliberately not part of the panel picker: the name is one full host, not
	// a wildcard, and it is pinned to the relay that answers for it.
	DNSOriginRelay = "relay"
)

// RelayAdvert is what the planner needs from a beam relay: the name it claims
// and the address that should answer for it. Narrow on purpose so this package
// stays free of the handlers package that reads the registry.
type RelayAdvert struct {
	Name string
	IP   string
}

// PlannedName is one A-record name plus the IPs that should answer for it.
type PlannedName struct {
	Name   string
	Zone   string
	Region string
	Origin string
	IPs    []string
}

// UnroutableName is a name some edge advertises that falls outside every managed
// zone. Reported, never written: without a zone there is nothing to write it to,
// and guessing one would mean touching a zone the operator never released.
type UnroutableName struct {
	Name   string
	Region string
	Origin string
}

// DNSPlan is the desired state for one reconcile pass.
type DNSPlan struct {
	Names      []PlannedName
	Unroutable []UnroutableName
}

// AdvertisedNames is the set of names this plan claims ownership of. Used to
// refresh the ownership registry, so a name stays owned while it is advertised.
func (p DNSPlan) AdvertisedNames() map[string]bool {
	out := make(map[string]bool, len(p.Names))
	for _, n := range p.Names {
		out[n.Name] = true
	}
	return out
}

// ResolveZone picks the managed zone a name belongs to, longest match wins so a
// delegated sub-zone beats its parent.
//
// The suffix check requires a label boundary. Without it "evil-dylaris.com"
// would match the zone "dylaris.com" and the reconciler would write into a zone
// on behalf of a name that does not belong to it.
func ResolveZone(name string, zones []string) string {
	n := normalizeDNSName(name)
	if n == "" {
		return ""
	}
	best := ""
	for _, z := range zones {
		zn := normalizeDNSName(z)
		if zn == "" {
			continue
		}
		if n != zn && !strings.HasSuffix(n, "."+zn) {
			continue
		}
		if len(zn) > len(best) {
			best = zn
		}
	}
	return best
}

// normalizeDNSName lowercases and drops the trailing root dot. Zones are
// compared as strings throughout, so they have to agree on one form.
func normalizeDNSName(s string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(s)), ".")
}

// BuildDNSPlan derives the desired records from the live edges, the live beam
// relays, the operator's per-region name selection, and the managed zones.
//
// Per region the panel selection is authoritative and the edges' own
// EDGE_WILDCARD is the fallback. That ordering is what lets one region serve
// several domains: a hoster offering two brands lists both names for the region
// and every online edge there answers for both.
//
// Edges and relays are planned TOGETHER, in one plan, on purpose. The ownership
// registry and the orphan sweep both work off the plan, so a relay name missing
// from it would be treated as abandoned and deleted after the grace period.
func BuildDNSPlan(edges []GatewayEdgeInfo, relays []RelayAdvert, regionNames map[string][]string, zones []string) DNSPlan {
	// region -> IPs of its online edges, and the names those edges advertise.
	ips := map[string][]string{}
	edgeNames := map[string][]string{}
	seenIP := map[string]bool{}
	seenName := map[string]bool{}

	for _, e := range edges {
		if e.Status != "online" {
			continue
		}
		ip := strings.TrimSpace(e.IP)
		if ip == "" {
			continue
		}
		region := normalizeDNSName(e.Region)
		if k := region + "|" + ip; !seenIP[k] {
			seenIP[k] = true
			ips[region] = append(ips[region], ip)
		}
		if w := normalizeDNSName(e.Wildcard); w != "" {
			if k := region + "|" + w; !seenName[k] {
				seenName[k] = true
				edgeNames[region] = append(edgeNames[region], w)
			}
		}
	}

	// Regions come from the live edges. A panel selection for a region with no
	// online edge is deliberately skipped: writing records for it would point a
	// name at nothing, and removing it is the orphan path's job, under grace.
	regions := make([]string, 0, len(ips))
	for region := range ips {
		regions = append(regions, region)
	}
	sort.Strings(regions)

	plan := DNSPlan{}
	for _, region := range regions {
		names, origin := resolveRegionNames(region, regionNames, edgeNames)
		regionIPs := append([]string(nil), ips[region]...)
		sort.Strings(regionIPs)

		for _, name := range names {
			zone := ResolveZone(name, zones)
			if zone == "" {
				plan.Unroutable = append(plan.Unroutable, UnroutableName{
					Name: name, Region: region, Origin: origin,
				})
				continue
			}
			plan.Names = append(plan.Names, PlannedName{
				Name: name, Zone: zone, Region: region, Origin: origin, IPs: regionIPs,
			})
		}
	}
	addRelayNames(&plan, relays, zones)

	sort.Slice(plan.Names, func(i, j int) bool { return plan.Names[i].Name < plan.Names[j].Name })
	sort.Slice(plan.Unroutable, func(i, j int) bool { return plan.Unroutable[i].Name < plan.Unroutable[j].Name })
	return plan
}

// addRelayNames folds the beam relays into the plan. Several relays in one
// region deliberately share one public host - that is what makes them
// round-robin and rolling-update capable - so they are grouped by name and every
// one of their addresses answers for it.
func addRelayNames(plan *DNSPlan, relays []RelayAdvert, zones []string) {
	// A name already claimed by an edge wins. Planning the same name twice would
	// make each pass delete the other's addresses as stale, flapping the record
	// on every tick, so the collision is reported instead of written.
	taken := map[string]bool{}
	for _, n := range plan.Names {
		taken[n.Name] = true
	}

	byName := map[string][]string{}
	seen := map[string]bool{}
	order := []string{}
	for _, r := range relays {
		name := normalizeDNSName(r.Name)
		ip := strings.TrimSpace(r.IP)
		if name == "" || ip == "" {
			continue
		}
		if _, ok := byName[name]; !ok {
			order = append(order, name)
		}
		if k := name + "|" + ip; !seen[k] {
			seen[k] = true
			byName[name] = append(byName[name], ip)
		}
	}
	sort.Strings(order)

	for _, name := range order {
		if taken[name] {
			plan.Unroutable = append(plan.Unroutable, UnroutableName{
				Name: name, Origin: DNSOriginRelay,
			})
			continue
		}
		zone := ResolveZone(name, zones)
		if zone == "" {
			plan.Unroutable = append(plan.Unroutable, UnroutableName{
				Name: name, Origin: DNSOriginRelay,
			})
			continue
		}
		ips := byName[name]
		sort.Strings(ips)
		plan.Names = append(plan.Names, PlannedName{
			Name: name, Zone: zone, Origin: DNSOriginRelay, IPs: ips,
		})
	}
}

// resolveRegionNames returns the names for one region plus where they came from.
func resolveRegionNames(region string, regionNames map[string][]string, edgeNames map[string][]string) ([]string, string) {
	if picked := dedupeNames(regionNames[region]); len(picked) > 0 {
		return picked, DNSOriginPanel
	}
	return dedupeNames(edgeNames[region]), DNSOriginEdge
}

func dedupeNames(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, raw := range in {
		n := normalizeDNSName(raw)
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// OwnedName is one registry entry: a name the reconciler created, and when it
// was last advertised by a live edge.
type OwnedName struct {
	Zone           string    `json:"zone"`
	LastAdvertised time.Time `json:"lastAdvertised"`
}

// PlanOrphans returns the owned names that are no longer advertised and whose
// grace period has run out, so their records can be removed.
//
// The registry is the ONLY source of deletable names. A name the reconciler
// never created cannot appear here however the zone listing looks, which is what
// keeps a released zone's unrelated records safe.
//
// The grace period exists because "not advertised" also covers a rolling edge
// restart and a brief hub outage. Deleting on the first missed heartbeat would
// take a live region out of DNS for as long as the TTL, over nothing.
func PlanOrphans(owned map[string]OwnedName, advertised map[string]bool, now time.Time, grace time.Duration) []PlannedName {
	var out []PlannedName
	for name, entry := range owned {
		if advertised[name] || entry.Zone == "" {
			continue
		}
		// A zero timestamp means the entry predates this field. Treat it as just
		// seen rather than instantly expired: one restart must not turn every
		// managed record into an orphan.
		if entry.LastAdvertised.IsZero() {
			continue
		}
		if now.Sub(entry.LastAdvertised) < grace {
			continue
		}
		out = append(out, PlannedName{Name: name, Zone: entry.Zone})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// RefreshOwnership folds one plan into the registry: advertised names get their
// timestamp bumped, names that disappeared keep theirs (that is what the grace
// period measures), and expired orphans are dropped by the caller after their
// records are actually gone.
func RefreshOwnership(owned map[string]OwnedName, plan DNSPlan, now time.Time) map[string]OwnedName {
	next := make(map[string]OwnedName, len(owned)+len(plan.Names))
	for name, entry := range owned {
		next[name] = entry
	}
	for _, n := range plan.Names {
		next[n.Name] = OwnedName{Zone: n.Zone, LastAdvertised: now}
	}
	return next
}
