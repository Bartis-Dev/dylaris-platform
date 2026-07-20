package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"dylaris-core/services"
	"dylaris-core/storage"
	"dylaris-core/store"
)

// These cover the single-Core constraint on the filesystem backend: a host
// path stores files on ONE machine's disk, so with a second Core online each
// instance serves only what it wrote itself - and nothing fails loudly,
// because every Core reads its own writes back perfectly.

// multiCoreFakeStore serves settings from a map and records writes, so a test
// can assert that a refused save persisted nothing.
type multiCoreFakeStore struct {
	store.Store
	values map[string]string
	writes int
}

func (f *multiCoreFakeStore) GetSetting(key string) (string, error) { return f.values[key], nil }

func (f *multiCoreFakeStore) SetSetting(key, value string) error {
	if f.values == nil {
		f.values = map[string]string{}
	}
	f.values[key] = value
	f.writes++
	return nil
}

func multiCoreState(t *testing.T, values map[string]string) *multiCoreFakeStore {
	t.Helper()
	return &multiCoreFakeStore{values: values}
}

// multiCoreRedis returns a client whose keyspace holds exactly the heartbeats
// named by ids, written the way CoreHeartbeatService writes them. No ids means
// a live Redis with an empty keyspace; use downRedis for the unreachable case.
func multiCoreRedis(t *testing.T, ids ...string) *redis.Client {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(mr.Close)
	for _, id := range ids {
		data, err := json.Marshal(services.CoreHeartbeat{ID: id})
		if err != nil {
			t.Fatalf("marshal heartbeat: %v", err)
		}
		mr.Set("dylaris:core:"+id, string(data))
	}
	return redis.NewClient(&redis.Options{Addr: mr.Addr()})
}

// downRedis returns a client pointed at a server that has been shut down, so
// every command fails.
func downRedis(t *testing.T) *redis.Client {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	addr := mr.Addr()
	mr.Close()
	return redis.NewClient(&redis.Options{Addr: addr})
}

func TestGuardHostPathBackend(t *testing.T) {
	tests := []struct {
		name       string
		cfg        CoreStorageConfig
		cores      []string
		redisDown  bool
		wantOK     bool
		wantStatus int
		wantMsg    string // substring
	}{
		{
			name:   "s3 is never gated",
			cfg:    CoreStorageConfig{Backend: "s3", S3Bucket: "b"},
			cores:  []string{"core-a", "core-b", "core-c"},
			wantOK: true,
		},
		{
			// s3 must not be blocked by a Redis outage either: the count is
			// irrelevant to it, so it must not even be attempted.
			name:      "s3 is not gated even when the count cannot be taken",
			cfg:       CoreStorageConfig{Backend: "s3", S3Bucket: "b"},
			redisDown: true,
			wantOK:    true,
		},
		{
			name:   "a host path on a single Core is allowed",
			cfg:    CoreStorageConfig{Backend: "path", Path: "/mnt/shared"},
			cores:  []string{"core-a"},
			wantOK: true,
		},
		{
			// A count of 0 means this Core's own heartbeat has not landed yet
			// (or failed to write), not that no Core is running. Treating it
			// as a refusal would make the setting unsavable on a fresh install
			// whose first heartbeat has not gone out.
			name:   "a count of zero is treated as one, not as a refusal",
			cfg:    CoreStorageConfig{Backend: "path", Path: "/mnt/shared"},
			cores:  nil,
			wantOK: true,
		},
		{
			name:       "a host path on two Cores is refused",
			cfg:        CoreStorageConfig{Backend: "path", Path: "/mnt/shared"},
			cores:      []string{"core-a", "core-b"},
			wantOK:     false,
			wantStatus: http.StatusConflict,
			wantMsg:    "2 Core instances are online",
		},
		{
			// "local" is the historical spelling of the same backend and is
			// still present in stored configs. Gating only "path" would leave
			// it as a way around the check.
			name:       "the historical local spelling is gated too",
			cfg:        CoreStorageConfig{Backend: "local", Path: "/mnt/shared"},
			cores:      []string{"core-a", "core-b"},
			wantOK:     false,
			wantStatus: http.StatusConflict,
		},
		{
			name:       "an unverifiable count refuses rather than waves through",
			cfg:        CoreStorageConfig{Backend: "path", Path: "/mnt/shared"},
			redisDown:  true,
			wantOK:     false,
			wantStatus: http.StatusServiceUnavailable,
			wantMsg:    "Could not verify",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rdb := multiCoreRedis(t, tc.cores...)
			if tc.redisDown {
				rdb = downRedis(t)
			}
			h := &CoreStorageHandler{state: &AppState{
				Store: multiCoreState(t, map[string]string{}),
				Redis: rdb,
			}}

			ok, status, msg := h.guardHostPathBackend(context.Background(), tc.cfg)

			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v (status %d, msg %q)", ok, tc.wantOK, status, msg)
			}
			if !tc.wantOK && status != tc.wantStatus {
				t.Errorf("status = %d, want %d", status, tc.wantStatus)
			}
			if tc.wantMsg != "" && !strings.Contains(msg, tc.wantMsg) {
				t.Errorf("message = %q, want it to contain %q", msg, tc.wantMsg)
			}
			if tc.wantOK && msg != "" {
				t.Errorf("an allowed config carried a message: %q", msg)
			}
		})
	}
}

// TestSaveConfig_RefusesHostPathOnMultipleCores drives the real handler,
// because the guard being correct is worth nothing if the save path does not
// consult it. It asserts nothing was persisted as well as the status: the
// writes run as an unguarded loop, so a refusal landing late would leave a
// half-applied config behind.
func TestSaveConfig_RefusesHostPathOnMultipleCores(t *testing.T) {
	st := multiCoreState(t, map[string]string{})
	h := &CoreStorageHandler{state: &AppState{Store: st, Redis: multiCoreRedis(t, "core-a", "core-b")}}

	body := `{"backend":"path","path":"/mnt/shared","pathConfirmed":true}`
	rec := httptest.NewRecorder()
	h.SaveConfig(rec, httptest.NewRequest(http.MethodPost, "/api/settings/core-storage", strings.NewReader(body)))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusConflict, rec.Body.String())
	}
	if st.writes != 0 {
		t.Errorf("the refused save persisted %d settings; it must persist none", st.writes)
	}
}

// TestSaveConfig_AllowsS3OnMultipleCores is the positive control: the guard
// must not have become a blanket refusal on multi-Core deployments, which are
// exactly the deployments S3 exists for.
func TestSaveConfig_AllowsS3OnMultipleCores(t *testing.T) {
	st := multiCoreState(t, map[string]string{})
	h := &CoreStorageHandler{state: &AppState{
		Store:       st,
		Redis:       multiCoreRedis(t, "core-a", "core-b"),
		StorageGate: storage.NewGate(),
		StorageS3:   storage.NewS3Resilience(),
	}}

	body := `{"backend":"s3","s3Bucket":"b","s3AccessKey":"AKIA","s3SecretKey":"secret"}`
	rec := httptest.NewRecorder()
	h.SaveConfig(rec, httptest.NewRequest(http.MethodPost, "/api/settings/core-storage", strings.NewReader(body)))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if st.values[keyCoreStorageBackend] != "s3" {
		t.Errorf("backend persisted as %q, want %q", st.values[keyCoreStorageBackend], "s3")
	}
}

func TestHostPathMultiCoreWarning(t *testing.T) {
	tests := []struct {
		name     string
		cfg      CoreStorageConfig
		online   int
		wantWarn bool
	}{
		{name: "a host path on one Core is fine", cfg: CoreStorageConfig{Backend: "path"}, online: 1},
		{name: "a host path on an uncounted deployment is fine", cfg: CoreStorageConfig{Backend: "path"}, online: 0},
		{name: "a host path on two Cores warns", cfg: CoreStorageConfig{Backend: "path"}, online: 2, wantWarn: true},
		{name: "the local spelling warns too", cfg: CoreStorageConfig{Backend: "local"}, online: 2, wantWarn: true},
		{name: "s3 on many Cores never warns", cfg: CoreStorageConfig{Backend: "s3"}, online: 5},
		{name: "an unconfigured backend never warns", cfg: CoreStorageConfig{}, online: 5},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := hostPathMultiCoreWarning(tc.cfg, tc.online)
			if (got != "") != tc.wantWarn {
				t.Fatalf("warning = %q, wantWarn = %v", got, tc.wantWarn)
			}
		})
	}
}

func TestGetConfig_ReportsHostPathAvailability(t *testing.T) {
	tests := []struct {
		name            string
		values          map[string]string
		cores           []string
		redisDown       bool
		wantAllowed     bool
		wantOnlineCores float64
		wantWarning     bool
	}{
		{
			name:            "one Core leaves the host path selectable",
			values:          map[string]string{},
			cores:           []string{"core-a"},
			wantAllowed:     true,
			wantOnlineCores: 1,
		},
		{
			name:            "two Cores take the host path off the table",
			values:          map[string]string{},
			cores:           []string{"core-a", "core-b"},
			wantAllowed:     false,
			wantOnlineCores: 2,
		},
		{
			// The warning is only for a config already SAVED as a host path.
			// An s3 deployment with many Cores is correct and must stay silent.
			name:            "a saved host path on two Cores also warns",
			values:          map[string]string{keyCoreStorageBackend: "path", keyCoreStoragePath: "/mnt/shared"},
			cores:           []string{"core-a", "core-b"},
			wantAllowed:     false,
			wantOnlineCores: 2,
			wantWarning:     true,
		},
		{
			name:            "a saved s3 config on two Cores does not warn",
			values:          map[string]string{keyCoreStorageBackend: "s3", keyCoreStorageS3Bucket: "b"},
			cores:           []string{"core-a", "core-b"},
			wantAllowed:     false,
			wantOnlineCores: 2,
		},
		{
			// A hint the server could not compute must not grey out a valid
			// option. The save path stays the enforcement point.
			name:            "an unverifiable count leaves the form usable",
			values:          map[string]string{},
			redisDown:       true,
			wantAllowed:     true,
			wantOnlineCores: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rdb := multiCoreRedis(t, tc.cores...)
			if tc.redisDown {
				rdb = downRedis(t)
			}
			h := &CoreStorageHandler{state: &AppState{Store: multiCoreState(t, tc.values), Redis: rdb}}

			rec := httptest.NewRecorder()
			h.GetConfig(rec, httptest.NewRequest(http.MethodGet, "/api/settings/core-storage", nil))

			var body map[string]interface{}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("body is not JSON: %v", err)
			}
			if body["hostPathAllowed"] != tc.wantAllowed {
				t.Errorf("hostPathAllowed = %v, want %v", body["hostPathAllowed"], tc.wantAllowed)
			}
			if body["onlineCores"] != tc.wantOnlineCores {
				t.Errorf("onlineCores = %v, want %v", body["onlineCores"], tc.wantOnlineCores)
			}
			warning, _ := body["hostPathWarning"].(string)
			if (warning != "") != tc.wantWarning {
				t.Errorf("hostPathWarning = %q, wantWarning = %v", warning, tc.wantWarning)
			}
		})
	}
}

// TestGetConfig_LeaksNoInstanceIdentities: the ids are hostnames and the count
// is all the form needs, so they must not ride along in the response.
func TestGetConfig_LeaksNoInstanceIdentities(t *testing.T) {
	h := &CoreStorageHandler{state: &AppState{
		Store: multiCoreState(t, map[string]string{}),
		Redis: multiCoreRedis(t, "core-prod-fra-01", "core-prod-fra-02"),
	}}

	rec := httptest.NewRecorder()
	h.GetConfig(rec, httptest.NewRequest(http.MethodGet, "/api/settings/core-storage", nil))

	for _, id := range []string{"core-prod-fra-01", "core-prod-fra-02"} {
		if strings.Contains(rec.Body.String(), id) {
			t.Errorf("the response named instance %q; body: %s", id, rec.Body.String())
		}
	}
}

func TestWarnAboutHostPathAtBoot(t *testing.T) {
	tests := []struct {
		name      string
		values    map[string]string
		cores     []string
		redisDown bool
		wantWarn  bool
	}{
		{
			// The case this exists for: a second Core joining a deployment
			// that already stores files on a host path.
			name:     "joining a host-path deployment warns",
			values:   map[string]string{keyCoreStorageBackend: "path", keyCoreStoragePath: "/mnt/shared"},
			cores:    []string{"core-a", "core-b"},
			wantWarn: true,
		},
		{
			name:   "a single Core on a host path is silent",
			values: map[string]string{keyCoreStorageBackend: "path", keyCoreStoragePath: "/mnt/shared"},
			cores:  []string{"core-a"},
		},
		{
			name:   "s3 is silent at any scale",
			values: map[string]string{keyCoreStorageBackend: "s3", keyCoreStorageS3Bucket: "b"},
			cores:  []string{"core-a", "core-b"},
		},
		{
			// Boot must never be blocked or made noisy by an unreachable
			// Redis; that is already loud elsewhere.
			name:      "an unverifiable count is silent",
			values:    map[string]string{keyCoreStorageBackend: "path", keyCoreStoragePath: "/mnt/shared"},
			redisDown: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rdb := multiCoreRedis(t, tc.cores...)
			if tc.redisDown {
				rdb = downRedis(t)
			}
			st := &AppState{Store: multiCoreState(t, tc.values), Redis: rdb}

			got := st.WarnAboutHostPathAtBoot(context.Background())

			if (got != "") != tc.wantWarn {
				t.Fatalf("warning = %q, wantWarn = %v", got, tc.wantWarn)
			}
		})
	}
}
