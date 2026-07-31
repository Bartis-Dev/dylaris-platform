package services

import (
	"context"
	"fmt"
	"net/netip"
	"sort"
	"strings"
	"time"

	"github.com/libdns/cloudflare"
	"github.com/libdns/libdns"
)

// DNS is managed through libdns rather than a hand-written client per provider.
//
// The reconciler only ever needs three operations on A records, but writing them
// against one vendor's REST API meant that supporting a second vendor was a
// second client. libdns is the abstraction Caddy uses for ~95 provider packages,
// so adding one here becomes a constructor entry instead of an HTTP client.
//
// Only A records are managed: raw Minecraft traffic is TCP/UDP, so these names
// must resolve to real addresses and can be neither proxied nor CNAMEd.

// dnsRecordTTL is what new records get. Deliberately short - these names follow
// live edges, and a long TTL would keep sending players to an edge that is
// already gone. Carried over unchanged from the previous client.
const dnsRecordTTL = 60 * time.Second

// DNSRecord is one A record as the reconciler sees it.
//
// There is deliberately no provider-assigned ID. libdns treats provider data as
// explicitly non-portable, so a record is identified the way DNS itself does it:
// by name, type and value. That also removes a class of bug where a stale ID
// deletes the wrong record.
type DNSRecord struct {
	Name string // FQDN, e.g. "*.eu.dylaris.com"
	IP   string // the IPv4 address
}

// DNSProvider is the surface the reconciler needs from a DNS backend. Every
// method takes the zone explicitly, because one credential is meant to manage
// several zones - a hoster offering more than one domain.
type DNSProvider interface {
	ListA(ctx context.Context, zone, name string) ([]DNSRecord, error)
	CreateA(ctx context.Context, zone, name, ip string) error
	DeleteA(ctx context.Context, zone, name, ip string) error
	// Zones lists the zones this credential can see. Optional in libdns, so it
	// returns ErrZoneListingUnsupported when the provider cannot do it - a
	// caller must tell that apart from "the call failed" and from "no zones
	// visible", because the three lead to different remedies.
	Zones(ctx context.Context) ([]string, error)
}

// ErrZoneListingUnsupported means the provider does not implement libdns's
// optional ZoneLister. The operator has to name their zones by hand; it is not a
// fault in their configuration and must not be reported as one.
var ErrZoneListingUnsupported = fmt.Errorf("this DNS provider cannot list zones")

// libdnsProvider adapts any libdns provider to DNSProvider.
type libdnsProvider struct {
	name     string
	records  libdns.RecordGetter
	appender libdns.RecordAppender
	deleter  libdns.RecordDeleter
	zones    libdns.ZoneLister // nil when the provider does not support listing
}

// NewDNSProvider builds a provider by name. Returns (nil, nil) when the token is
// missing, so the caller keeps treating "not configured" as "feature off".
func NewDNSProvider(providerName, token string) (DNSProvider, error) {
	if strings.TrimSpace(token) == "" {
		return nil, nil
	}
	switch strings.ToLower(strings.TrimSpace(providerName)) {
	case "", "cloudflare":
		return wrapLibDNS("cloudflare", &cloudflare.Provider{APIToken: token})
	default:
		return nil, fmt.Errorf("unknown DNS provider %q", providerName)
	}
}

// SupportedDNSProviders is what the panel offers. Kept beside the constructor so
// the two cannot drift apart.
func SupportedDNSProviders() []string { return []string{"cloudflare"} }

// wrapLibDNS asserts the three required interfaces plus the optional one. A
// provider missing a required interface is a wiring mistake here, not a runtime
// condition, so it fails loudly at construction rather than at 3am.
func wrapLibDNS(name string, p any) (DNSProvider, error) {
	getter, ok := p.(libdns.RecordGetter)
	if !ok {
		return nil, fmt.Errorf("dns provider %s cannot read records", name)
	}
	appender, ok := p.(libdns.RecordAppender)
	if !ok {
		return nil, fmt.Errorf("dns provider %s cannot create records", name)
	}
	deleter, ok := p.(libdns.RecordDeleter)
	if !ok {
		return nil, fmt.Errorf("dns provider %s cannot delete records", name)
	}
	lister, _ := p.(libdns.ZoneLister) // optional; nil is a valid state
	return &libdnsProvider{name: name, records: getter, appender: appender, deleter: deleter, zones: lister}, nil
}

// aRecord builds the libdns record for one name/ip pair. libdns names are
// RELATIVE to the zone, while the reconciler works in FQDNs throughout.
func aRecord(zone, name, ip string) (libdns.Address, error) {
	addr, err := netip.ParseAddr(strings.TrimSpace(ip))
	if err != nil {
		return libdns.Address{}, fmt.Errorf("invalid IP %q: %w", ip, err)
	}
	return libdns.Address{
		Name: libdns.RelativeName(strings.TrimSuffix(name, "."), zone),
		TTL:  dnsRecordTTL,
		IP:   addr,
	}, nil
}

func (l *libdnsProvider) ListA(ctx context.Context, zone, name string) ([]DNSRecord, error) {
	recs, err := l.records.GetRecords(ctx, zone)
	if err != nil {
		return nil, err
	}
	want := strings.TrimSuffix(strings.ToLower(name), ".")
	out := []DNSRecord{}
	for _, rec := range recs {
		rr := rec.RR()
		if !strings.EqualFold(rr.Type, "A") {
			continue
		}
		// GetRecords returns the whole zone with relative names; compare on the
		// absolute form so callers keep speaking FQDNs.
		fqdn := strings.TrimSuffix(strings.ToLower(libdns.AbsoluteName(rr.Name, zone)), ".")
		if fqdn != want {
			continue
		}
		out = append(out, DNSRecord{Name: name, IP: rr.Data})
	}
	// Stable order so logs and tests do not depend on provider iteration order.
	sort.Slice(out, func(i, j int) bool { return out[i].IP < out[j].IP })
	return out, nil
}

func (l *libdnsProvider) CreateA(ctx context.Context, zone, name, ip string) error {
	rec, err := aRecord(zone, name, ip)
	if err != nil {
		return err
	}
	// Append, not Set: several edges answer for one wildcard, and SetRecords
	// would remove exactly the siblings we deliberately keep.
	_, err = l.appender.AppendRecords(ctx, zone, []libdns.Record{rec})
	return err
}

func (l *libdnsProvider) DeleteA(ctx context.Context, zone, name, ip string) error {
	rec, err := aRecord(zone, name, ip)
	if err != nil {
		return err
	}
	_, err = l.deleter.DeleteRecords(ctx, zone, []libdns.Record{rec})
	return err
}

func (l *libdnsProvider) Zones(ctx context.Context) ([]string, error) {
	if l.zones == nil {
		return nil, ErrZoneListingUnsupported
	}
	zones, err := l.zones.ListZones(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(zones))
	for _, z := range zones {
		if name := strings.TrimSpace(z.Name); name != "" {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out, nil
}
