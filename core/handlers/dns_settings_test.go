package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"dylaris-core/services"
	"dylaris-core/store"
)

// dnsFakeStore embeds store.Store (nil) so it satisfies the full interface at
// compile time; only the settings methods this handler touches are overridden.
type dnsFakeStore struct {
	store.Store
	values map[string]string
	writes []string // keys written, in order, so a test can assert what was NOT written
	setErr error
}

func newDNSFakeStore(values map[string]string) *dnsFakeStore {
	if values == nil {
		values = map[string]string{}
	}
	return &dnsFakeStore{values: values}
}

func (f *dnsFakeStore) GetSetting(key string) (string, error) {
	v, ok := f.values[key]
	if !ok {
		return "", errors.New("no rows")
	}
	return v, nil
}

func (f *dnsFakeStore) SetSetting(key, value string) error {
	if f.setErr != nil {
		return f.setErr
	}
	f.writes = append(f.writes, key)
	f.values[key] = value
	return nil
}

func (f *dnsFakeStore) wrote(key string) bool {
	for _, k := range f.writes {
		if k == key {
			return true
		}
	}
	return false
}

// dnsHandler wires a handler over a fake store plus the given environment.
func dnsHandler(env services.DNSEnvConfig, fs *dnsFakeStore) *DNSSettingsHandler {
	return NewDNSSettingsHandler(&AppState{
		Store:     fs,
		DNSConfig: services.NewDNSConfigResolver(env, fs),
	})
}

func dnsSaveReq(body any) *http.Request {
	raw, _ := json.Marshal(body)
	return httptest.NewRequest(http.MethodPost, "/api/settings/dns", bytes.NewReader(raw))
}

// The credential must never come back out of the API, in any field.
func TestDNSGet_NeverReturnsToken(t *testing.T) {
	fs := newDNSFakeStore(map[string]string{
		services.DNSTokenSettingKey:    "super-secret-token",
		services.DNSProviderSettingKey: "cloudflare",
		services.DNSZonesSettingKey:    `["example.com"]`,
		services.DNSEnabledSettingKey:  "true",
	})
	rec := httptest.NewRecorder()
	dnsHandler(services.DNSEnvConfig{}, fs).Get(rec, httptest.NewRequest(http.MethodGet, "/api/settings/dns", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if bytes.Contains(rec.Body.Bytes(), []byte("super-secret-token")) {
		t.Fatalf("response leaked the token: %s", rec.Body.String())
	}
	var out struct {
		TokenSet   bool     `json:"tokenSet"`
		Source     string   `json:"source"`
		EnvManaged bool     `json:"envManaged"`
		Zones      []string `json:"zones"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !out.TokenSet || out.Source != "panel" || out.EnvManaged {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
	if len(out.Zones) != 1 || out.Zones[0] != "example.com" {
		t.Fatalf("zones = %v, want the stored zone", out.Zones)
	}
}

// An env-supplied credential is authoritative. Storing a panel token beside it
// would produce a setting that looks applied and is never read.
func TestDNSSave_RejectsTokenWhenEnvManaged(t *testing.T) {
	fs := newDNSFakeStore(nil)
	h := dnsHandler(services.DNSEnvConfig{Token: "env-token"}, fs)

	rec := httptest.NewRecorder()
	h.Save(rec, dnsSaveReq(map[string]any{"provider": "cloudflare", "zones": []string{"example.com"}, "token": "panel-token"}))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
	if len(fs.writes) != 0 {
		t.Errorf("rejected save still wrote %v", fs.writes)
	}
}

func TestDNSSave_RejectsClearWhenEnvManaged(t *testing.T) {
	fs := newDNSFakeStore(nil)
	h := dnsHandler(services.DNSEnvConfig{Token: "env-token"}, fs)

	rec := httptest.NewRecorder()
	h.Save(rec, dnsSaveReq(map[string]any{"provider": "cloudflare", "zones": []string{"example.com"}, "clearToken": true}))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
}

// An empty token field means "leave the stored credential alone", so an admin
// can change the zone without re-typing it. Writing "" here would silently
// disable the updater.
func TestDNSSave_EmptyTokenKeepsStoredCredential(t *testing.T) {
	fs := newDNSFakeStore(map[string]string{services.DNSTokenSettingKey: "stored-token"})
	h := dnsHandler(services.DNSEnvConfig{}, fs)

	rec := httptest.NewRecorder()
	// enabled:false so the save does not attempt a live provider probe.
	h.Save(rec, dnsSaveReq(map[string]any{"provider": "cloudflare", "zones": []string{"new.example"}, "enabled": false}))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if fs.wrote(services.DNSTokenSettingKey) {
		t.Error("an empty token field overwrote the stored credential")
	}
	if fs.values[services.DNSTokenSettingKey] != "stored-token" {
		t.Errorf("stored token = %q, want it untouched", fs.values[services.DNSTokenSettingKey])
	}
	if got := fs.values[services.DNSZonesSettingKey]; got != `["new.example"]` {
		t.Errorf("zones = %q, want it saved", got)
	}
}

// clearToken is the only way to express removal, which an empty string cannot.
func TestDNSSave_ClearTokenRemovesCredential(t *testing.T) {
	fs := newDNSFakeStore(map[string]string{services.DNSTokenSettingKey: "stored-token"})
	h := dnsHandler(services.DNSEnvConfig{}, fs)

	rec := httptest.NewRecorder()
	h.Save(rec, dnsSaveReq(map[string]any{"provider": "cloudflare", "zones": []string{"example.com"}, "clearToken": true}))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if fs.values[services.DNSTokenSettingKey] != "" {
		t.Errorf("token = %q, want it cleared", fs.values[services.DNSTokenSettingKey])
	}
}

func TestDNSSave_Validation(t *testing.T) {
	tests := []struct {
		name string
		body map[string]any
		want int
	}{
		{
			// An unknown provider would be rejected at construction later; catching
			// it here keeps an unusable value out of the settings table.
			name: "unknown provider",
			body: map[string]any{"provider": "route53", "zones": []string{"example.com"}},
			want: http.StatusBadRequest,
		},
		{
			name: "enabling without a token",
			body: map[string]any{"provider": "cloudflare", "zones": []string{"example.com"}, "enabled": true},
			want: http.StatusBadRequest,
		},
		{
			// libdns addresses a zone by NAME; enabling without one would write
			// nothing forever without ever erroring.
			name: "enabling without a zone",
			body: map[string]any{"provider": "cloudflare", "token": "t", "enabled": true},
			want: http.StatusBadRequest,
		},
		{
			// A disabled configuration is allowed to be incomplete: that is how an
			// admin saves the zone now and adds the credential later.
			name: "incomplete but disabled is accepted",
			body: map[string]any{"provider": "cloudflare", "zones": []string{"example.com"}, "enabled": false},
			want: http.StatusOK,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := newDNSFakeStore(nil)
			rec := httptest.NewRecorder()
			dnsHandler(services.DNSEnvConfig{}, fs).Save(rec, dnsSaveReq(tt.body))
			if rec.Code != tt.want {
				t.Errorf("status = %d, want %d: %s", rec.Code, tt.want, rec.Body.String())
			}
		})
	}
}

func TestDNSSave_InvalidJSON(t *testing.T) {
	fs := newDNSFakeStore(nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/settings/dns", bytes.NewReader([]byte("{not json")))
	dnsHandler(services.DNSEnvConfig{}, fs).Save(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// A trailing dot is valid zone notation but libdns takes the bare name. Two
// spellings of one zone must collapse, or a name would resolve against one entry
// while the operator looks at the other.
func TestDNSSave_NormalizesZones(t *testing.T) {
	fs := newDNSFakeStore(nil)
	rec := httptest.NewRecorder()
	dnsHandler(services.DNSEnvConfig{}, fs).Save(rec, dnsSaveReq(map[string]any{
		"provider": "cloudflare", "zones": []string{"  Example.com.  ", "example.com"},
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if got := fs.values[services.DNSZonesSettingKey]; got != `["example.com"]` {
		t.Errorf("stored zones = %q, want a single normalised entry", got)
	}
}

// Multi-zone is the point of the feature: one credential managing several zones.
func TestDNSSave_MultipleZones(t *testing.T) {
	fs := newDNSFakeStore(nil)
	rec := httptest.NewRecorder()
	dnsHandler(services.DNSEnvConfig{}, fs).Save(rec, dnsSaveReq(map[string]any{
		"provider": "cloudflare", "zones": []string{"brand-b.net", "brand-a.com"},
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if got := fs.values[services.DNSZonesSettingKey]; got != `["brand-a.com","brand-b.net"]` {
		t.Errorf("stored zones = %q", got)
	}
}

// A per-region name outside every managed zone is rejected at save time. Storing
// it would leave a name that is advertised and silently never written - the
// invisible failure this screen exists to prevent.
func TestDNSSave_RejectsNameOutsideManagedZones(t *testing.T) {
	fs := newDNSFakeStore(nil)
	rec := httptest.NewRecorder()
	dnsHandler(services.DNSEnvConfig{}, fs).Save(rec, dnsSaveReq(map[string]any{
		"provider":    "cloudflare",
		"zones":       []string{"brand-a.com"},
		"regionNames": map[string][]string{"eu": {"*.eu.brand-a.com", "*.eu.elsewhere.net"}},
	}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if len(fs.writes) != 0 {
		t.Errorf("rejected save still wrote %v", fs.writes)
	}
}

func TestDNSSave_StoresRegionNames(t *testing.T) {
	fs := newDNSFakeStore(nil)
	rec := httptest.NewRecorder()
	dnsHandler(services.DNSEnvConfig{}, fs).Save(rec, dnsSaveReq(map[string]any{
		"provider":    "cloudflare",
		"zones":       []string{"brand-a.com", "brand-b.net"},
		"regionNames": map[string][]string{"EU": {"*.eu.brand-b.net", "*.eu.brand-a.com"}},
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	got := fs.values[services.DNSRegionNamesSettingKey]
	if got != `{"eu":["*.eu.brand-a.com","*.eu.brand-b.net"]}` {
		t.Errorf("stored region names = %q", got)
	}
}

// The grace period is what keeps a rolling edge restart from deleting a live
// region's records, so a client that omits the field must not reset it.
func TestDNSSave_GraceMinutes(t *testing.T) {
	tests := []struct {
		name    string
		payload map[string]any
		want    string // "" means the key must not be written at all
	}{
		{"omitted keeps the current value", map[string]any{"provider": "cloudflare"}, ""},
		{"zero keeps the current value", map[string]any{"provider": "cloudflare", "graceMinutes": 0}, ""},
		{"a real value is stored", map[string]any{"provider": "cloudflare", "graceMinutes": 30}, "30"},
		// Below the floor a single missed heartbeat could delete a live record.
		{"below the floor is clamped", map[string]any{"provider": "cloudflare", "graceMinutes": -5}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := newDNSFakeStore(nil)
			rec := httptest.NewRecorder()
			dnsHandler(services.DNSEnvConfig{}, fs).Save(rec, dnsSaveReq(tt.payload))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
			}
			if tt.want == "" {
				if fs.wrote(services.DNSGraceSettingKey) {
					t.Errorf("grace was written as %q, want untouched", fs.values[services.DNSGraceSettingKey])
				}
				return
			}
			if got := fs.values[services.DNSGraceSettingKey]; got != tt.want {
				t.Errorf("grace = %q, want %q", got, tt.want)
			}
		})
	}
}

// Zone discovery must stay a 200 with a distinguishable state: the four
// outcomes lead to four different remedies, and the panel switches on this
// field. With no credential there is nothing to ask, which is the error state
// rather than an empty result.
func TestDNSZones_NoTokenIsErrorState(t *testing.T) {
	fs := newDNSFakeStore(nil)
	rec := httptest.NewRecorder()
	dnsHandler(services.DNSEnvConfig{}, fs).Zones(rec, httptest.NewRequest(http.MethodGet, "/api/settings/dns/zones", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var out struct {
		State string   `json:"state"`
		Zones []string `json:"zones"`
		Error string   `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.State != "error" {
		t.Errorf("state = %q, want %q", out.State, "error")
	}
	if out.Error == "" {
		t.Error("error state carries no message")
	}
	// Never null: the panel maps over this array directly.
	if out.Zones == nil {
		t.Error("zones = null, want an empty array")
	}
}
