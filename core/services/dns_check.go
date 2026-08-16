package services

import (
	"context"
	"net"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// dnsLookupTimeout bounds every individual DNS lookup and TCP dial so one
// unreachable target can never stall the whole check.
const dnsLookupTimeout = 3 * time.Second

// Public resolvers we query directly (instead of the host resolver) so the
// result reflects the PUBLIC view of DNS and bypasses any local cache. We try
// Cloudflare first, fall back to Google.
var publicDNSServers = []string{"1.1.1.1:53", "8.8.8.8:53"}

// DNSRecordCheck is one computed-and-verified DNS record in the contract.
type DNSRecordCheck struct {
	Category string   `json:"category"` // player | wildcard | cname | panel
	Type     string   `json:"type"`     // A | CNAME
	Name     string   `json:"name"`     // the FQDN the operator must create
	Expected []string `json:"expected"` // edge IPs (empty for panel: operator origin)
	Actual   []string `json:"actual"`   // what public DNS currently returns
	Status   string   `json:"status"`   // ok | mismatch | missing | unresolved
	Hint     string   `json:"hint"`
}

// DNSReachabilityCheck is one TCP-dial result in the contract.
type DNSReachabilityCheck struct {
	Target string `json:"target"`
	OK     bool   `json:"ok"`
	Hint   string `json:"hint"`
}

// DNSCheckResult is the full GET /api/gateway/dns-check payload.
type DNSCheckResult struct {
	Success      bool                   `json:"success"`
	Records      []DNSRecordCheck       `json:"records"`
	Reachability []DNSReachabilityCheck `json:"reachability"`
	Edges        []string               `json:"edges"`
	CheckedAt    string                 `json:"checkedAt"`
}

// DNSCheckConfig is the operator's OWN configuration the checker is bound to.
// Nothing here is user-supplied at request time — the handler fills it from
// FRONTEND_URL + the settings store, so there is no arbitrary-host / SSRF or
// recon surface: we only ever resolve and dial the operator's own domains and
// the registered edge IPs.
type DNSCheckConfig struct {
	PanelHost       string   // host of FRONTEND_URL (no scheme/port)
	PanelDialTarget string   // host:port to TCP-dial for the panel
	HosterDomains   []string // player base domains, e.g. "play.example.com"
	CustomDomainsOn bool     // gateway_custom_domains_enabled == "true"
	CNAMETarget     string   // gateway_cname_target (only when CustomDomainsOn)
}

// publicResolver builds a net.Resolver that dials the public DNS servers
// directly. PreferGo forces Go's own resolver so the custom Dial is actually
// used (the cgo resolver would ignore it).
func publicResolver() *net.Resolver {
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			d := net.Dialer{Timeout: dnsLookupTimeout}
			var lastErr error
			for _, server := range publicDNSServers {
				// DNS over UDP/TCP — honour whichever the resolver asked for.
				conn, err := d.DialContext(ctx, network, server)
				if err == nil {
					return conn, nil
				}
				lastErr = err
			}
			return nil, lastErr
		},
	}
}

// RunDNSCheck computes the expected DNS records from the operator's config,
// resolves each via the public resolver, and probes reachability of the edges
// and the panel. A single failed lookup or dial yields a per-record
// unresolved/false status with a hint — never an error for the whole call.
func RunDNSCheck(ctx context.Context, rdb *redis.Client, cfg DNSCheckConfig) DNSCheckResult {
	resolver := publicResolver()

	// Edge IPs = public IPs of the ONLINE edges. This is both the "edges"
	// array and the expected target for player/wildcard/cname records.
	edgeIPs := onlineEdgeIPs(ctx, rdb)

	result := DNSCheckResult{
		Success:      true,
		Records:      []DNSRecordCheck{},
		Reachability: []DNSReachabilityCheck{},
		Edges:        edgeIPs,
		CheckedAt:    time.Now().Format(time.RFC3339),
	}

	// --- Panel record: A <panelHost> -> operator origin (any IP counts). ---
	if cfg.PanelHost != "" {
		rec := DNSRecordCheck{
			Category: "panel",
			Type:     "A",
			Name:     cfg.PanelHost,
			Expected: []string{}, // operator-specific origin, no fixed expected IP
		}
		actual := lookupIPs(ctx, resolver, cfg.PanelHost)
		rec.Actual = actual
		if len(actual) == 0 {
			rec.Status = "unresolved"
			rec.Hint = "Panel domain does not resolve. Create an A record pointing it at your panel server's public IP."
		} else {
			rec.Status = "ok"
			rec.Hint = "Panel domain resolves."
		}
		result.Records = append(result.Records, rec)
	}

	// --- Player + wildcard records for each hoster base domain. ---
	for _, base := range cfg.HosterDomains {
		base = strings.ToLower(strings.TrimSpace(base))
		if base == "" {
			continue
		}

		// Player base: A <base> -> edge IP.
		result.Records = append(result.Records,
			checkAgainstEdges(ctx, resolver, "player", base, base, edgeIPs))

		// Wildcard: A *.<base> -> edge IP. You can't query "*.base" directly;
		// a wildcard A record answers ANY subdomain, so we resolve a synthetic
		// label that no operator would ever create as a real host. If it
		// resolves to an edge IP, the wildcard is in place.
		wildcardProbe := "dylaris-dnscheck." + base
		rec := checkAgainstEdges(ctx, resolver, "wildcard", "*."+base, wildcardProbe, edgeIPs)
		result.Records = append(result.Records, rec)
	}

	// --- Custom-domain CNAME targets: one per hoster base. ---
	// CNAMETarget is a LABEL ("route"), expanded to route.<base> for every
	// hoster domain, so each region gets its own target and a customer points
	// their domain at the region they actually want.
	if cfg.CustomDomainsOn && cfg.CNAMETarget != "" {
		label := strings.ToLower(strings.TrimSpace(cfg.CNAMETarget))
		for _, base := range cfg.HosterDomains {
			base = strings.ToLower(strings.TrimSpace(base))
			if base == "" {
				continue
			}
			fqdn := label + "." + base
			result.Records = append(result.Records,
				checkAgainstEdges(ctx, resolver, "cname", fqdn, fqdn, edgeIPs))
		}
	}

	// --- Reachability: TCP-dial the MC ingress on each edge + the panel. ---
	for _, ip := range edgeIPs {
		addr := net.JoinHostPort(ip, "25565")
		ok, hint := dialTCP(addr)
		result.Reachability = append(result.Reachability, DNSReachabilityCheck{
			Target: "edge " + addr,
			OK:     ok,
			Hint:   hint,
		})
	}
	if cfg.PanelDialTarget != "" {
		ok, hint := dialTCP(cfg.PanelDialTarget)
		result.Reachability = append(result.Reachability, DNSReachabilityCheck{
			Target: "panel " + cfg.PanelDialTarget,
			OK:     ok,
			Hint:   hint,
		})
	}

	return result
}

// onlineEdgeIPs returns the public IPs of the online edges, deduplicated and
// in stable order.
func onlineEdgeIPs(ctx context.Context, rdb *redis.Client) []string {
	edges := GetEdgesFromRedis(ctx, rdb)
	seen := map[string]struct{}{}
	ips := []string{}
	for _, e := range edges {
		if e.Status != "online" {
			continue
		}
		ip := strings.TrimSpace(e.IP)
		if ip == "" {
			continue
		}
		if _, dup := seen[ip]; dup {
			continue
		}
		seen[ip] = struct{}{}
		ips = append(ips, ip)
	}
	return ips
}

// checkAgainstEdges resolves probeName and grades it against the expected edge
// IP set. recordName is what's reported to the operator (may differ from the
// probe, e.g. "*.base" vs the synthetic wildcard probe).
func checkAgainstEdges(ctx context.Context, resolver *net.Resolver, category, recordName, probeName string, edgeIPs []string) DNSRecordCheck {
	rec := DNSRecordCheck{
		Category: category,
		Type:     "A",
		Name:     recordName,
		Expected: edgeIPs,
	}
	actual := lookupIPs(ctx, resolver, probeName)
	rec.Actual = actual

	if len(edgeIPs) == 0 {
		rec.Status = "missing"
		rec.Hint = "No online edges registered. Bring an edge online before its IP can be used as the record target."
		return rec
	}
	if len(actual) == 0 {
		rec.Status = "unresolved"
		rec.Hint = "Record does not resolve. Create an A record pointing " + recordName + " at one of the edge IPs."
		return rec
	}
	for _, a := range actual {
		for _, e := range edgeIPs {
			if a == e {
				rec.Status = "ok"
				rec.Hint = "Resolves to an edge IP."
				return rec
			}
		}
	}
	rec.Status = "mismatch"
	rec.Hint = "Resolves, but not to any current edge IP. Update the record to point at one of the edge IPs."
	return rec
}

// lookupIPs resolves a host to its IPv4/IPv6 addresses via the public
// resolver. A failed lookup returns an empty slice (graded as unresolved by
// the caller) — it never propagates an error.
func lookupIPs(ctx context.Context, resolver *net.Resolver, host string) []string {
	lctx, cancel := context.WithTimeout(ctx, dnsLookupTimeout)
	defer cancel()
	addrs, err := resolver.LookupHost(lctx, host)
	if err != nil {
		return []string{}
	}
	return addrs
}

// dialTCP attempts a short TCP connection and returns ok plus a hint.
func dialTCP(addr string) (bool, string) {
	conn, err := net.DialTimeout("tcp", addr, dnsLookupTimeout)
	if err != nil {
		return false, "Could not connect: " + err.Error()
	}
	_ = conn.Close()
	return true, ""
}
