package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"dylaris-core/models"
)

// fakeTelemetryStore is a minimal in-memory telemetryStore for these tests.
type fakeTelemetryStore struct {
	settings map[string]string
	servers  []models.Server
	players  int
}

func (f *fakeTelemetryStore) GetSetting(key string) (string, error) { return f.settings[key], nil }
func (f *fakeTelemetryStore) GetAllActiveServers() ([]models.Server, error) {
	return f.servers, nil
}
func (f *fakeTelemetryStore) SumLatestPlayerCounts() (int, error) { return f.players, nil }

// fakeElection (type fakeElection bool) is declared in ticket_autoclose_test.go
// in this same package; used here as fakeElection(true) / fakeElection(false).

// capture records what the receiver saw. Guarded because the httptest handler
// runs in a separate goroutine from the test.
type capture struct {
	mu   sync.Mutex
	hits int
	body []byte
	hdr  http.Header
}

func newCaptureServer(cap *capture) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		cap.mu.Lock()
		cap.hits++
		cap.body = b
		cap.hdr = r.Header.Clone()
		cap.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
}

type wirePayload struct {
	InstanceID string `json:"instanceId"`
	Type       string `json:"type"`
	Servers    int    `json:"servers"`
	Online     int    `json:"online"`
	Players    int    `json:"players"`
	Version    string `json:"version"`
}

// TestDeriveInstanceID pins the deployment-id scheme: deterministic, distinct
// per secret, 16 lowercase hex chars, and computed from the salt + secret only
// (independent of hostname, which was the bug being fixed).
func TestDeriveInstanceID(t *testing.T) {
	// Re-derive with the salt as a literal so an accidental change to the
	// scheme (salt string, separator, truncation) breaks this test. The value
	// is a cross-repo wire contract with the website.
	golden := func(secret string) string {
		sum := sha256.Sum256([]byte("dylaris-telemetry-instance-v1:" + secret))
		return hex.EncodeToString(sum[:])[:16]
	}

	secrets := []string{"", "a", "test-cluster-secret", "another-secret-value"}
	seen := map[string]string{}
	for _, s := range secrets {
		got := deriveInstanceID(s)

		if want := golden(s); got != want {
			t.Errorf("deriveInstanceID(%q) = %q, want %q", s, got, want)
		}
		if deriveInstanceID(s) != got {
			t.Errorf("deriveInstanceID(%q) not deterministic", s)
		}
		if len(got) != 16 {
			t.Errorf("deriveInstanceID(%q) len = %d, want 16", s, len(got))
		}
		for _, c := range got {
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
				t.Errorf("deriveInstanceID(%q) = %q has non-hex char %q", s, got, c)
			}
		}
		if prev, ok := seen[got]; ok {
			t.Errorf("deriveInstanceID collision: %q and %q both -> %q", prev, s, got)
		}
		seen[got] = s
	}
}

// helper: build a heartbeat pointed at srv, with the given store, no leader set.
func newTestHeartbeat(store telemetryStore, endpoint string) *TelemetryHeartbeat {
	if store.(*fakeTelemetryStore).settings == nil {
		store.(*fakeTelemetryStore).settings = map[string]string{}
	}
	store.(*fakeTelemetryStore).settings["telemetry_endpoint"] = endpoint
	return NewTelemetryHeartbeat(store, "test-cluster-secret", "eu-central")
}

func TestPostPayload(t *testing.T) {
	cap := &capture{}
	srv := newCaptureServer(cap)
	defer srv.Close()

	store := &fakeTelemetryStore{
		settings: map[string]string{"routing_mode": "gateway"},
		servers: []models.Server{
			{Status: "online"},
			{Status: "online"},
			{Status: "stopped"},
		},
		players: 42,
	}
	hb := newTestHeartbeat(store, srv.URL)
	hb.post(context.Background())

	cap.mu.Lock()
	defer cap.mu.Unlock()
	if cap.hits != 1 {
		t.Fatalf("expected 1 heartbeat, got %d", cap.hits)
	}

	var p wirePayload
	if err := json.Unmarshal(cap.body, &p); err != nil {
		t.Fatalf("unmarshal payload: %v (body=%s)", err, cap.body)
	}
	if want := deriveInstanceID("test-cluster-secret"); p.InstanceID != want {
		t.Errorf("instanceId = %q, want %q", p.InstanceID, want)
	}
	if p.Type != "gateway" {
		t.Errorf("type = %q, want gateway", p.Type)
	}
	if p.Servers != 3 {
		t.Errorf("servers = %d, want 3", p.Servers)
	}
	if p.Online != 2 {
		t.Errorf("online = %d, want 2", p.Online)
	}
	if p.Players != 42 {
		t.Errorf("players = %d, want 42", p.Players)
	}
	if p.Version != TelemetryCoreVersion {
		t.Errorf("version = %q, want %q", p.Version, TelemetryCoreVersion)
	}

	if ct := cap.hdr.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	if ua := cap.hdr.Get("User-Agent"); ua != "Dylaris-Telemetry/"+TelemetryCoreVersion {
		t.Errorf("User-Agent = %q", ua)
	}
}

func TestPostRoutingModeMapsType(t *testing.T) {
	cases := []struct {
		routing string
		want    string
	}{
		{"", "platform"},
		{"ip_port", "platform"},
		{"gateway", "gateway"},
		{"both", "gateway"},
	}
	for _, tc := range cases {
		t.Run(tc.routing, func(t *testing.T) {
			cap := &capture{}
			srv := newCaptureServer(cap)
			defer srv.Close()

			store := &fakeTelemetryStore{settings: map[string]string{"routing_mode": tc.routing}}
			hb := newTestHeartbeat(store, srv.URL)
			hb.post(context.Background())

			cap.mu.Lock()
			defer cap.mu.Unlock()
			var p wirePayload
			if err := json.Unmarshal(cap.body, &p); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if p.Type != tc.want {
				t.Errorf("routing %q -> type %q, want %q", tc.routing, p.Type, tc.want)
			}
		})
	}
}

func TestPostLeaderGating(t *testing.T) {
	cases := []struct {
		name      string
		setLeader bool
		isLeader  bool
		wantHits  int
	}{
		{"no leader set posts", false, false, 1},
		{"leader posts", true, true, 1},
		{"non-leader stays silent", true, false, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cap := &capture{}
			srv := newCaptureServer(cap)
			defer srv.Close()

			store := &fakeTelemetryStore{}
			hb := newTestHeartbeat(store, srv.URL)
			if tc.setLeader {
				hb.SetLeader(fakeElection(tc.isLeader))
			}
			hb.post(context.Background())

			cap.mu.Lock()
			defer cap.mu.Unlock()
			if cap.hits != tc.wantHits {
				t.Errorf("hits = %d, want %d", cap.hits, tc.wantHits)
			}
		})
	}
}

func TestPostDisabledSetting(t *testing.T) {
	for _, val := range []string{"false", "False", "FALSE"} {
		t.Run(val, func(t *testing.T) {
			cap := &capture{}
			srv := newCaptureServer(cap)
			defer srv.Close()

			store := &fakeTelemetryStore{settings: map[string]string{"telemetry_enabled": val}}
			hb := newTestHeartbeat(store, srv.URL)
			hb.post(context.Background())

			cap.mu.Lock()
			defer cap.mu.Unlock()
			if cap.hits != 0 {
				t.Errorf("telemetry_enabled=%q still posted (hits=%d)", val, cap.hits)
			}
		})
	}
}

func TestPostAuthHeader(t *testing.T) {
	cases := []struct {
		name     string
		key      string
		wantAuth string
	}{
		{"key set", "shhh", "Bearer shhh"},
		{"empty key omits header", "", ""},
		{"whitespace key omits header", "   ", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("DYLARIS_TELEMETRY_KEY", tc.key)

			cap := &capture{}
			srv := newCaptureServer(cap)
			defer srv.Close()

			store := &fakeTelemetryStore{}
			hb := newTestHeartbeat(store, srv.URL)
			hb.post(context.Background())

			cap.mu.Lock()
			defer cap.mu.Unlock()
			if got := cap.hdr.Get("Authorization"); got != tc.wantAuth {
				t.Errorf("Authorization = %q, want %q", got, tc.wantAuth)
			}
		})
	}
}
