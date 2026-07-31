package services

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// DNS configuration comes from two places and the precedence between them is a
// deliberate decision: the ENVIRONMENT WINS whenever it sets a credential.
//
// An operator who mounts a Docker secret or sets DNS_API_TOKEN has chosen not to
// put that token in the database. If a panel value could override it, a setting
// saved months later would silently retire the deployment's real credential and
// nothing on screen would say so. So the panel is the fallback, not the winner,
// and the API reports which source is in effect so the screen can say it plainly.

// Settings keys backing panel-configured DNS. dnsTokenSettingKey is registered
// in store.settingsSecretKeys, so it is encrypted at rest transparently.
const (
	DNSProviderSettingKey = "dns.provider"
	DNSTokenSettingKey    = "dns.api_token"
	// DNSZonesSettingKey holds a JSON array of managed zone names. One credential
	// can manage several zones, because what multiplies for a hoster is zones,
	// not providers.
	DNSZonesSettingKey = "dns.zones"
	// DNSRegionNamesSettingKey holds a JSON object of region -> record names, so
	// one region can serve several domains.
	DNSRegionNamesSettingKey = "dns.region_names"
	DNSGraceSettingKey       = "dns.orphan_grace_minutes"
	DNSEnabledSettingKey     = "dns.enabled"
)

// Orphan grace bounds. A name that stops being advertised is not deleted until
// this much time has passed, so a rolling edge restart or a brief hub outage
// cannot take a live region out of DNS.
const (
	DefaultDNSOrphanGraceMinutes = 15
	MinDNSOrphanGraceMinutes     = 1
)

// DNSConfigSource says where the effective configuration came from. Purely for
// display: an admin looking at a greyed-out field needs to know why.
type DNSConfigSource string

const (
	DNSSourceNone  DNSConfigSource = "none"
	DNSSourceEnv   DNSConfigSource = "env"
	DNSSourcePanel DNSConfigSource = "panel"
)

// DNSConfig is one resolved configuration. Token is the plaintext credential and
// must never be serialised into an API response.
type DNSConfig struct {
	Enabled  bool
	Provider string
	Token    string
	// Zones the reconciler may write into. A name outside all of them is never
	// written: the operator has not released that zone.
	Zones []string
	// RegionNames maps a region to the record names it should serve. Empty for a
	// region means falling back to that region's EDGE_WILDCARD.
	RegionNames map[string][]string
	// OrphanGrace is how long a name must go unadvertised before its records are
	// removed.
	OrphanGrace time.Duration
	Source      DNSConfigSource
}

// Complete reports whether this config can actually drive the reconciler. At
// least one zone is required because libdns addresses a zone by name - with none
// configured every name would be unroutable and nothing would ever be written.
func (c DNSConfig) Complete() bool {
	return c.Enabled && c.Provider != "" && c.Token != "" && len(c.Zones) > 0
}

// fingerprint changes whenever something the provider is built from changes. The
// reconciler compares it per tick to decide whether to rebuild, so a panel edit
// takes effect within one interval and no restart is needed.
//
// The token participates as a HASH, not as its length: provider tokens are
// fixed-width (a Cloudflare token is always 40 characters), so a length would
// make a rotated credential indistinguishable from the old one and the
// reconciler would keep using the retired token until the next restart.
// Zones are not part of it: they are passed per call rather than baked into the
// provider, so changing the zone list needs no rebuild.
func (c DNSConfig) fingerprint() string {
	sum := sha256.Sum256([]byte(c.Token))
	return fmt.Sprintf("%t|%s|%s", c.Enabled, c.Provider, hex.EncodeToString(sum[:8]))
}

// DNSEnvConfig is what the process environment supplies, passed in from config
// so this package does not read env itself.
type DNSEnvConfig struct {
	Enabled  bool
	Provider string
	Token    string
	// Zones comes from DNS_ZONE (one zone) or DNS_ZONES (comma-separated).
	Zones []string
}

// DNSConfigResolver resolves the effective DNS configuration on every call, so a
// change made in the panel is picked up without a restart.
type DNSConfigResolver struct {
	env   DNSEnvConfig
	store settingsReader
}

func NewDNSConfigResolver(env DNSEnvConfig, store settingsReader) *DNSConfigResolver {
	return &DNSConfigResolver{env: env, store: store}
}

// EnvHasToken reports whether the environment supplies the credential. The panel
// uses this to render its token field read-only instead of letting an admin type
// a value that would never be used.
func (r *DNSConfigResolver) EnvHasToken() bool {
	return strings.TrimSpace(r.env.Token) != ""
}

// Resolve returns the effective configuration. Env credentials win; everything
// else falls back to the panel field by field, so an operator can supply the
// token by secret and still pick the zone on screen.
func (r *DNSConfigResolver) Resolve() DNSConfig {
	cfg := DNSConfig{Source: DNSSourceNone}

	envToken := strings.TrimSpace(r.env.Token)
	if envToken != "" {
		cfg.Token = envToken
		cfg.Source = DNSSourceEnv
	} else if v := r.setting(DNSTokenSettingKey); v != "" {
		cfg.Token = v
		cfg.Source = DNSSourcePanel
	}

	cfg.Provider = firstNonEmpty(strings.TrimSpace(r.env.Provider), r.setting(DNSProviderSettingKey))
	if cfg.Provider == "" && cfg.Token != "" {
		// A token with no named provider is Cloudflare, the only provider that
		// ever existed here. Keeps a pre-panel deployment working untouched.
		cfg.Provider = "cloudflare"
	}
	// Zones: the env list wins as a whole when set, rather than merging. A merge
	// would make it impossible to remove a zone from the panel while the env
	// still names it, and the operator could not tell which list was in effect.
	if envZones := dedupeNames(r.env.Zones); len(envZones) > 0 {
		cfg.Zones = envZones
	} else {
		cfg.Zones = dedupeNames(parseDNSZonesSetting(r.setting(DNSZonesSettingKey)))
	}

	cfg.RegionNames = parseRegionNamesSetting(r.setting(DNSRegionNamesSettingKey))
	cfg.OrphanGrace = parseGraceSetting(r.setting(DNSGraceSettingKey))

	// The env switch is a hard on: DNS_UPDATER_ENABLED=true keeps working exactly
	// as before the panel existed. Otherwise the panel toggle decides.
	cfg.Enabled = r.env.Enabled || r.setting(DNSEnabledSettingKey) == "true"

	return cfg
}

// parseDNSZonesSetting accepts the stored JSON array. A plain string is accepted
// too so a value written by an older build (a single zone) still reads.
func parseDNSZonesSetting(raw string) []string {
	if raw == "" {
		return nil
	}
	var list []string
	if err := json.Unmarshal([]byte(raw), &list); err == nil {
		return list
	}
	var single string
	if err := json.Unmarshal([]byte(raw), &single); err == nil {
		return []string{single}
	}
	return []string{raw}
}

// parseRegionNamesSetting reads region -> names. A malformed value yields no
// selection, which falls back to the edges' own EDGE_WILDCARD rather than
// dropping every record.
func parseRegionNamesSetting(raw string) map[string][]string {
	if raw == "" {
		return nil
	}
	var m map[string][]string
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return nil
	}
	out := make(map[string][]string, len(m))
	for region, names := range m {
		if r := normalizeDNSName(region); r != "" {
			if n := dedupeNames(names); len(n) > 0 {
				out[r] = n
			}
		}
	}
	return out
}

// parseGraceSetting clamps to a floor: a zero or negative grace would delete a
// name the moment one heartbeat is missed, which is what the grace exists to
// prevent.
func parseGraceSetting(raw string) time.Duration {
	minutes := DefaultDNSOrphanGraceMinutes
	if raw != "" {
		if v, err := strconv.Atoi(raw); err == nil {
			minutes = v
		}
	}
	if minutes < MinDNSOrphanGraceMinutes {
		minutes = MinDNSOrphanGraceMinutes
	}
	return time.Duration(minutes) * time.Minute
}

// setting reads one key, treating any error (missing row, no DB) as unset. A
// resolver that failed loudly here would take down a reconciler tick over a
// setting that has simply never been saved.
func (r *DNSConfigResolver) setting(key string) string {
	if r.store == nil {
		return ""
	}
	v, err := r.store.GetSetting(key)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(v)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
