package services

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"sort"
	"strings"
	"time"

	"github.com/libdns/bunny"
	"github.com/libdns/cloudflare"
	"github.com/libdns/cloudns"
	"github.com/libdns/desec"
	"github.com/libdns/gandi"
	"github.com/libdns/godaddy"
	"github.com/libdns/hetzner"
	"github.com/libdns/ionos"
	"github.com/libdns/libdns"
	"github.com/libdns/namecheap"
	"github.com/libdns/netcup"
	"github.com/libdns/ovh"
	"github.com/libdns/porkbun"
	"github.com/libdns/route53"
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

// DNSCredentialField is one input the panel has to render for a provider. Most
// providers want a single API token; a few want two to four values, and there is
// no way to guess which from the name alone, so each provider declares its own.
type DNSCredentialField struct {
	// Key is the JSON key the value is stored under. Stable: it is persisted.
	Key string `json:"key"`
	// Label is what the admin reads.
	Label string `json:"label"`
	// Secret marks a value that should be typed into a password field and never
	// echoed back by the API.
	Secret bool `json:"secret"`
	// Optional fields may be left empty (a provider default applies).
	Optional bool `json:"optional,omitempty"`
	// Hint is a short "where do I get this" note, shown under the input.
	Hint string `json:"hint,omitempty"`
}

// DNSProviderSpec is one selectable provider plus the credential shape it needs.
type DNSProviderSpec struct {
	Name   string               `json:"name"`
	Label  string               `json:"label"`
	Fields []DNSCredentialField `json:"fields"`
}

// tokenField is the single-API-token shape most providers use.
func tokenField(label, hint string) []DNSCredentialField {
	return []DNSCredentialField{{Key: "token", Label: label, Secret: true, Hint: hint}}
}

// dnsProviderSpecs is the catalogue. Adding a provider is a libdns import plus one
// entry here and one case in newLibDNSProvider - that was the whole point of
// going through libdns rather than writing a client per vendor.
//
// Only providers with a libdns release built against libdns v1 are listed: the v1
// Record interface is not satisfied by the older packages, so a v0 provider does
// not compile against wrapLibDNS in the first place.
var dnsProviderSpecs = []DNSProviderSpec{
	{Name: "cloudflare", Label: "Cloudflare", Fields: tokenField("API token", "A scoped token with Zone:DNS:Edit on the zones below.")},
	{Name: "hetzner", Label: "Hetzner DNS", Fields: tokenField("API token", "Hetzner DNS Console -> API tokens.")},
	{Name: "desec", Label: "deSEC", Fields: tokenField("API token", "desec.io -> Token management.")},
	{Name: "ionos", Label: "IONOS", Fields: tokenField("API token", "IONOS DNS API key, in the form publicprefix.secret.")},
	{Name: "godaddy", Label: "GoDaddy", Fields: tokenField("API token", "Developer portal key and secret joined as key:secret.")},
	{Name: "gandi", Label: "Gandi", Fields: tokenField("Personal access token", "Gandi account -> Personal Access Token.")},
	{Name: "bunny", Label: "Bunny.net", Fields: tokenField("Access key", "Bunny dashboard -> Account -> API key.")},
	{Name: "porkbun", Label: "Porkbun", Fields: []DNSCredentialField{
		{Key: "api_key", Label: "API key", Secret: true},
		{Key: "api_secret_key", Label: "API secret key", Secret: true},
	}},
	{Name: "netcup", Label: "netcup", Fields: []DNSCredentialField{
		{Key: "customer_number", Label: "Customer number"},
		{Key: "api_key", Label: "API key", Secret: true},
		{Key: "api_password", Label: "API password", Secret: true},
	}},
	{Name: "namecheap", Label: "Namecheap", Fields: []DNSCredentialField{
		{Key: "user", Label: "API user"},
		{Key: "api_key", Label: "API key", Secret: true},
		{Key: "client_ip", Label: "Whitelisted client IP", Optional: true, Hint: "Namecheap only answers from IPs on its API allowlist. Leave empty to auto-detect."},
	}},
	{Name: "cloudns", Label: "ClouDNS", Fields: []DNSCredentialField{
		{Key: "auth_id", Label: "Auth ID", Optional: true, Hint: "Set either Auth ID or the sub-auth pair."},
		{Key: "sub_auth_id", Label: "Sub-auth ID", Optional: true},
		{Key: "sub_auth_user", Label: "Sub-auth user", Optional: true},
		{Key: "auth_password", Label: "Auth password", Secret: true},
	}},
	{Name: "ovh", Label: "OVH", Fields: []DNSCredentialField{
		{Key: "endpoint", Label: "Endpoint", Hint: "e.g. ovh-eu, ovh-ca, ovh-us."},
		{Key: "application_key", Label: "Application key", Secret: true},
		{Key: "application_secret", Label: "Application secret", Secret: true},
		{Key: "consumer_key", Label: "Consumer key", Secret: true},
	}},
	{Name: "route53", Label: "AWS Route 53", Fields: []DNSCredentialField{
		{Key: "access_key_id", Label: "Access key ID"},
		{Key: "secret_access_key", Label: "Secret access key", Secret: true},
		{Key: "region", Label: "Region", Optional: true, Hint: "Defaults to us-east-1."},
	}},
}

// SupportedDNSProviders is what the panel offers, with the credential shape each
// one needs. Kept beside the constructor so the two cannot drift apart.
func SupportedDNSProviders() []DNSProviderSpec { return dnsProviderSpecs }

// DNSProviderSpecFor looks up one spec. Second return is false for an unknown name.
func DNSProviderSpecFor(name string) (DNSProviderSpec, bool) {
	want := normalizeDNSProviderName(name)
	for _, spec := range dnsProviderSpecs {
		if spec.Name == want {
			return spec, true
		}
	}
	return DNSProviderSpec{}, false
}

// normalizeDNSProviderName folds the empty name onto cloudflare, the only
// provider that existed before this catalogue, so a pre-catalogue deployment that
// stored only a token keeps working.
func normalizeDNSProviderName(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" {
		return "cloudflare"
	}
	return n
}

// parseDNSCredential turns the stored credential into per-field values.
//
// Two accepted forms, and the reason both exist: multi-field providers need a
// JSON object, while every deployment that predates them stored a bare token
// string. A value that does not parse as a JSON object is therefore treated as
// the provider's FIRST field - which for a single-token provider is exactly the
// token, so nothing has to be migrated and DNS_API_TOKEN in the environment stays
// a plain token.
func parseDNSCredential(spec DNSProviderSpec, credential string) map[string]string {
	credential = strings.TrimSpace(credential)
	out := map[string]string{}
	if strings.HasPrefix(credential, "{") {
		var m map[string]string
		if err := json.Unmarshal([]byte(credential), &m); err == nil {
			for k, v := range m {
				out[k] = strings.TrimSpace(v)
			}
			return out
		}
		// Fall through: a value that merely looks like JSON but is not gets the
		// bare-token treatment rather than being silently dropped.
	}
	if len(spec.Fields) > 0 {
		out[spec.Fields[0].Key] = credential
	}
	return out
}

// missingDNSCredentialFields lists required fields the credential does not carry.
// Returned as a named error rather than letting the provider fail on its first
// API call, where the message is the vendor's and says nothing about which field
// was forgotten.
func missingDNSCredentialFields(spec DNSProviderSpec, values map[string]string) []string {
	var missing []string
	for _, f := range spec.Fields {
		if f.Optional {
			continue
		}
		if strings.TrimSpace(values[f.Key]) == "" {
			missing = append(missing, f.Label)
		}
	}
	return missing
}

// NewDNSProvider builds a provider by name from the stored credential. Returns
// (nil, nil) when the credential is missing entirely, so the caller keeps treating
// "not configured" as "feature off".
func NewDNSProvider(providerName, credential string) (DNSProvider, error) {
	if strings.TrimSpace(credential) == "" {
		return nil, nil
	}
	name := normalizeDNSProviderName(providerName)
	spec, ok := DNSProviderSpecFor(name)
	if !ok {
		return nil, fmt.Errorf("unknown DNS provider %q", providerName)
	}
	values := parseDNSCredential(spec, credential)
	if missing := missingDNSCredentialFields(spec, values); len(missing) > 0 {
		return nil, fmt.Errorf("dns provider %s is missing: %s", spec.Label, strings.Join(missing, ", "))
	}
	return newLibDNSProvider(name, values)
}

// newLibDNSProvider is the one place a provider name becomes a libdns client.
func newLibDNSProvider(name string, v map[string]string) (DNSProvider, error) {
	switch name {
	case "cloudflare":
		return wrapLibDNS(name, &cloudflare.Provider{APIToken: v["token"]})
	case "hetzner":
		return wrapLibDNS(name, &hetzner.Provider{AuthAPIToken: v["token"]})
	case "desec":
		return wrapLibDNS(name, &desec.Provider{Token: v["token"]})
	case "ionos":
		return wrapLibDNS(name, &ionos.Provider{AuthAPIToken: v["token"]})
	case "godaddy":
		return wrapLibDNS(name, &godaddy.Provider{APIToken: v["token"]})
	case "gandi":
		return wrapLibDNS(name, &gandi.Provider{BearerToken: v["token"]})
	case "bunny":
		return wrapLibDNS(name, &bunny.Provider{AccessKey: v["token"]})
	case "porkbun":
		return wrapLibDNS(name, &porkbun.Provider{APIKey: v["api_key"], APISecretKey: v["api_secret_key"]})
	case "netcup":
		return wrapLibDNS(name, &netcup.Provider{
			CustomerNumber: v["customer_number"],
			APIKey:         v["api_key"],
			APIPassword:    v["api_password"],
		})
	case "namecheap":
		return wrapLibDNS(name, &namecheap.Provider{
			User:     v["user"],
			APIKey:   v["api_key"],
			ClientIP: v["client_ip"],
		})
	case "cloudns":
		return wrapLibDNS(name, &cloudns.Provider{
			AuthId:       v["auth_id"],
			SubAuthId:    v["sub_auth_id"],
			SubAuthUser:  v["sub_auth_user"],
			AuthPassword: v["auth_password"],
		})
	case "ovh":
		return wrapLibDNS(name, &ovh.Provider{
			Endpoint:          v["endpoint"],
			ApplicationKey:    v["application_key"],
			ApplicationSecret: v["application_secret"],
			ConsumerKey:       v["consumer_key"],
		})
	case "route53":
		region := v["region"]
		if region == "" {
			region = "us-east-1"
		}
		return wrapLibDNS(name, &route53.Provider{
			AccessKeyId:     v["access_key_id"],
			SecretAccessKey: v["secret_access_key"],
			Region:          region,
		})
	default:
		// Unreachable: DNSProviderSpecFor already rejected unknown names. Kept so
		// a spec added without a constructor fails here instead of silently
		// building nothing.
		return nil, fmt.Errorf("DNS provider %q has no constructor", name)
	}
}

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
