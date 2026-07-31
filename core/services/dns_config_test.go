package services

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// fakeDNSSettings is a settings table for the resolver. A key that is absent
// returns an error, exactly as PostgresStore.GetSetting does for a missing row -
// the resolver must treat that as "unset", not as a failure.
type fakeDNSSettings struct {
	values map[string]string
	err    error
}

func (f *fakeDNSSettings) GetSetting(key string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	v, ok := f.values[key]
	if !ok {
		return "", errors.New("no rows")
	}
	return v, nil
}

func TestDNSResolve_EnvWinsOverPanel(t *testing.T) {
	// The security-relevant case: an operator supplying the credential by
	// environment must not have it silently replaced by a panel value.
	store := &fakeDNSSettings{values: map[string]string{
		DNSTokenSettingKey:    "panel-token",
		DNSProviderSettingKey: "cloudflare",
		DNSZonesSettingKey:    `["panel.example"]`,
		DNSEnabledSettingKey:  "true",
	}}
	r := NewDNSConfigResolver(DNSEnvConfig{
		Enabled: true, Provider: "cloudflare", Token: "env-token", Zones: []string{"env.example"},
	}, store)

	cfg := r.Resolve()
	if cfg.Token != "env-token" {
		t.Errorf("Token = %q, want the env token", cfg.Token)
	}
	if len(cfg.Zones) != 1 || cfg.Zones[0] != "env.example" {
		t.Errorf("Zones = %v, want the env zone", cfg.Zones)
	}
	if cfg.Source != DNSSourceEnv {
		t.Errorf("Source = %q, want %q", cfg.Source, DNSSourceEnv)
	}
	if !r.EnvHasToken() {
		t.Error("EnvHasToken() = false, want true")
	}
}

func TestDNSResolve_PanelFallback(t *testing.T) {
	store := &fakeDNSSettings{values: map[string]string{
		DNSTokenSettingKey:    "panel-token",
		DNSProviderSettingKey: "cloudflare",
		DNSZonesSettingKey:    `["example.com"]`,
		DNSEnabledSettingKey:  "true",
	}}
	r := NewDNSConfigResolver(DNSEnvConfig{}, store)

	cfg := r.Resolve()
	if cfg.Token != "panel-token" || len(cfg.Zones) != 1 || cfg.Zones[0] != "example.com" {
		t.Fatalf("Resolve() = %+v, want the panel values", cfg)
	}
	if cfg.Source != DNSSourcePanel {
		t.Errorf("Source = %q, want %q", cfg.Source, DNSSourcePanel)
	}
	if !cfg.Complete() {
		t.Error("Complete() = false, want true for a fully configured panel setup")
	}
	if r.EnvHasToken() {
		t.Error("EnvHasToken() = true with no env token")
	}
}

// The env can supply the credential while the zone is picked in the panel, so
// an operator keeping the token in a Docker secret still configures the rest on
// screen. Precedence is per field, not all-or-nothing.
func TestDNSResolve_MixedSources(t *testing.T) {
	store := &fakeDNSSettings{values: map[string]string{
		DNSZonesSettingKey:   `["panel.example"]`,
		DNSEnabledSettingKey: "true",
	}}
	r := NewDNSConfigResolver(DNSEnvConfig{Token: "env-token"}, store)

	cfg := r.Resolve()
	if cfg.Token != "env-token" {
		t.Errorf("Token = %q, want the env token", cfg.Token)
	}
	if len(cfg.Zones) != 1 || cfg.Zones[0] != "panel.example" {
		t.Errorf("Zones = %v, want the panel zone", cfg.Zones)
	}
	// No provider anywhere: a bare token means Cloudflare, the only provider
	// that ever existed here, so a pre-panel deployment keeps working.
	if cfg.Provider != "cloudflare" {
		t.Errorf("Provider = %q, want the cloudflare default", cfg.Provider)
	}
}

func TestDNSResolve_EnabledSources(t *testing.T) {
	tests := []struct {
		name       string
		envEnabled bool
		setting    string
		want       bool
	}{
		{"env switch alone enables", true, "", true},
		{"panel switch alone enables", false, "true", true},
		{"neither leaves it off", false, "false", false},
		{"unset setting leaves it off", false, "", false},
		// The env switch is a hard on: DNS_UPDATER_ENABLED=true keeps behaving
		// exactly as it did before the panel existed.
		{"env on wins over panel off", true, "false", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values := map[string]string{}
			if tt.setting != "" {
				values[DNSEnabledSettingKey] = tt.setting
			}
			r := NewDNSConfigResolver(DNSEnvConfig{Enabled: tt.envEnabled}, &fakeDNSSettings{values: values})
			if got := r.Resolve().Enabled; got != tt.want {
				t.Errorf("Enabled = %v, want %v", got, tt.want)
			}
		})
	}
}

// A failing settings read must degrade to "unset" rather than propagate. The
// resolver runs on every reconciler tick; a transient DB error taking down the
// loop would be a worse outcome than one tick that finds nothing configured.
func TestDNSResolve_StoreErrorIsUnset(t *testing.T) {
	r := NewDNSConfigResolver(DNSEnvConfig{}, &fakeDNSSettings{err: errors.New("db down")})
	cfg := r.Resolve()
	if cfg.Complete() {
		t.Error("Complete() = true despite an unreadable settings store")
	}
	if cfg.Source != DNSSourceNone {
		t.Errorf("Source = %q, want %q", cfg.Source, DNSSourceNone)
	}
}

func TestDNSResolve_NilStore(t *testing.T) {
	r := NewDNSConfigResolver(DNSEnvConfig{Token: "t", Zones: []string{"z"}, Enabled: true}, nil)
	if cfg := r.Resolve(); !cfg.Complete() {
		t.Errorf("Resolve() = %+v, want a complete env-only config with no store", cfg)
	}
}

func TestDNSConfigComplete(t *testing.T) {
	full := DNSConfig{Enabled: true, Provider: "cloudflare", Token: "t", Zones: []string{"example.com"}}
	if !full.Complete() {
		t.Fatal("a fully populated config is not Complete()")
	}
	tests := []struct {
		name string
		cfg  DNSConfig
	}{
		{"disabled", DNSConfig{Provider: "cloudflare", Token: "t", Zones: []string{"example.com"}}},
		{"no provider", DNSConfig{Enabled: true, Token: "t", Zones: []string{"example.com"}}},
		{"no token", DNSConfig{Enabled: true, Provider: "cloudflare", Zones: []string{"example.com"}}},
		// libdns addresses a zone by NAME; without one every call would go to an
		// empty zone and write nothing, forever, without an error.
		{"no zone", DNSConfig{Enabled: true, Provider: "cloudflare", Token: "t"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.cfg.Complete() {
				t.Error("Complete() = true, want false")
			}
		})
	}
}

// The fingerprint is what makes a panel edit take effect without a restart: the
// reconciler rebuilds its provider only when this value changes.
func TestDNSConfigFingerprint(t *testing.T) {
	base := DNSConfig{Enabled: true, Provider: "cloudflare", Token: "token-a", Zones: []string{"example.com"}}
	if base.fingerprint() != base.fingerprint() {
		t.Fatal("fingerprint is not stable across calls")
	}
	changed := []struct {
		name string
		cfg  DNSConfig
	}{
		{"provider", DNSConfig{Enabled: true, Provider: "route53", Token: "token-a", Zones: []string{"example.com"}}},
		{"enabled", DNSConfig{Provider: "cloudflare", Token: "token-a", Zones: []string{"example.com"}}},
		{"token length", DNSConfig{Enabled: true, Provider: "cloudflare", Token: "token-a-longer", Zones: []string{"example.com"}}},
		// Provider tokens are fixed-width - a Cloudflare token is always 40
		// characters. If the fingerprint tracked length, a rotated credential
		// would look identical and the reconciler would keep using the retired
		// token until the next restart.
		{"same-length token", DNSConfig{Enabled: true, Provider: "cloudflare", Token: "token-b", Zones: []string{"example.com"}}},
	}
	for _, tt := range changed {
		t.Run(tt.name, func(t *testing.T) {
			if tt.cfg.fingerprint() == base.fingerprint() {
				t.Errorf("fingerprint unchanged after the %s changed", tt.name)
			}
		})
	}
}

// The credential itself must not sit in a string this code compares on every
// tick; only its length participates.
func TestDNSConfigFingerprintOmitsToken(t *testing.T) {
	cfg := DNSConfig{Enabled: true, Provider: "cloudflare", Token: "super-secret", Zones: []string{"example.com"}}
	if got := cfg.fingerprint(); strings.Contains(got, "super-secret") {
		t.Errorf("fingerprint %q contains the token", got)
	}
}

// Zones are deliberately NOT part of the fingerprint: they are passed per call
// rather than baked into the provider, so adding a zone must not force a
// needless rebuild of a perfectly good client.
func TestDNSConfigFingerprintIgnoresZones(t *testing.T) {
	a := DNSConfig{Enabled: true, Provider: "cloudflare", Token: "t", Zones: []string{"example.com"}}
	b := DNSConfig{Enabled: true, Provider: "cloudflare", Token: "t", Zones: []string{"example.com", "other.com"}}
	if a.fingerprint() != b.fingerprint() {
		t.Error("fingerprint changed when only the zone list changed")
	}
}

func TestParseDNSZonesSetting(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want []string
	}{
		{"json array", `["a.com","b.com"]`, []string{"a.com", "b.com"}},
		{"empty", "", nil},
		{"empty array", `[]`, []string{}},
		// A value written by an earlier single-zone build must still read, or an
		// upgrade would silently drop the operator's zone.
		{"legacy json string", `"a.com"`, []string{"a.com"}},
		{"legacy bare string", `a.com`, []string{"a.com"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseDNSZonesSetting(tt.raw)
			if len(got) != len(tt.want) {
				t.Fatalf("parseDNSZonesSetting(%q) = %v, want %v", tt.raw, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestParseRegionNamesSetting(t *testing.T) {
	got := parseRegionNamesSetting(`{"EU":["*.eu.a.com","*.eu.a.com"],"us":[],"":["x.com"]}`)
	if len(got) != 1 {
		t.Fatalf("parseRegionNamesSetting = %v, want only the eu entry", got)
	}
	// Regions are normalised, duplicates collapsed, and empty selections dropped
	// so they fall back to the edge wildcard rather than becoming an empty list.
	if len(got["eu"]) != 1 || got["eu"][0] != "*.eu.a.com" {
		t.Errorf("eu = %v", got["eu"])
	}
}

// A malformed value must fall back to the edge wildcards, not to an empty
// selection that would orphan every record.
func TestParseRegionNamesSetting_Malformed(t *testing.T) {
	if got := parseRegionNamesSetting(`{not json`); got != nil {
		t.Errorf("parseRegionNamesSetting = %v, want nil", got)
	}
}

func TestParseGraceSetting(t *testing.T) {
	tests := []struct {
		raw  string
		want time.Duration
	}{
		{"", DefaultDNSOrphanGraceMinutes * time.Minute},
		{"30", 30 * time.Minute},
		{"garbage", DefaultDNSOrphanGraceMinutes * time.Minute},
		// A zero or negative grace would delete a name the moment one heartbeat
		// is missed, which is what the grace period exists to prevent.
		{"0", MinDNSOrphanGraceMinutes * time.Minute},
		{"-5", MinDNSOrphanGraceMinutes * time.Minute},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			if got := parseGraceSetting(tt.raw); got != tt.want {
				t.Errorf("parseGraceSetting(%q) = %v, want %v", tt.raw, got, tt.want)
			}
		})
	}
}
