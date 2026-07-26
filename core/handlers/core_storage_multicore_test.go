package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"dylaris-core/services"
	"dylaris-core/services/storagereach"
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
	rdb, _ := multiCoreRedisServer(t, ids...)
	return rdb
}

// multiCoreRedisServer is multiCoreRedis plus the server behind it, for the one
// test that has to inject a per-command fault (SetPreHook) rather than just
// seed keys.
func multiCoreRedisServer(t *testing.T, ids ...string) (*redis.Client, *miniredis.Miniredis) {
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
	return redis.NewClient(&redis.Options{Addr: mr.Addr()}), mr
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

// sharedLocalFactory hands every participant a LocalProvider on the same
// root, mirroring services/storagereach/round_test.go's sharedFactory: what a
// genuinely shared mount looks like from the verifier's point of view. It
// ignores cfg entirely (backend, path, s3 fields) on purpose - the tests below
// are about SaveConfig's wiring of the round, not about proving a real
// filesystem or S3 endpoint, exactly like round_test.go's own use of it with
// an s3-flavoured Config.
func sharedLocalFactory(root string) storagereach.ProviderFactory {
	return func(storagereach.Config) (storage.StorageProvider, error) {
		return &storage.LocalProvider{BasePath: root}, nil
	}
}

// TestSaveConfig_RefusesHostPathOnMultipleCores drives the real handler,
// because the guard being correct is worth nothing if the save path does not
// consult it. Two Cores heartbeat but only core-a (the handler under test)
// ever participates in the round, so core-b is fail-closed no-response - the
// same "a peer never proves it" case checkSharedStorageReachable's own unit
// tests cover, exercised here through the actual SaveConfig HTTP handler. It
// asserts nothing was persisted as well as the status: the writes run as an
// unguarded loop, so a refusal landing late would leave a half-applied config
// behind.
func TestSaveConfig_RefusesHostPathOnMultipleCores(t *testing.T) {
	st := multiCoreState(t, map[string]string{})
	rdb := multiCoreRedis(t, "core-a", "core-b")
	h := &CoreStorageHandler{state: &AppState{
		Store: st,
		Redis: rdb,
		StorageReach: storagereach.NewService(storagereach.ServiceDeps{
			Redis: rdb, CoreID: "core-a", NewProvider: sharedLocalFactory(t.TempDir()),
		}),
		reachRoundDeadline: 150 * time.Millisecond,
	}}

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

// TestSaveConfig_AllowsS3OnMultipleCores is the positive control, updated for
// the round-based guard: s3 on multiple Cores is no longer an unconditional
// bypass (the old count-only guard's "s3 is never gated" - it must now pass
// the SAME proof a host path does. core-b runs a real participant goroutine,
// exactly like the service loop does in production, so the round can actually
// confirm both Cores and the save succeeds.
func TestSaveConfig_AllowsS3OnMultipleCores(t *testing.T) {
	st := multiCoreState(t, map[string]string{})
	rdb := multiCoreRedis(t, "core-a", "core-b")
	factory := sharedLocalFactory(t.TempDir())
	h := &CoreStorageHandler{state: &AppState{
		Store:       st,
		Redis:       rdb,
		StorageGate: storage.NewGate(),
		StorageS3:   storage.NewS3Resilience(),
		StorageReach: storagereach.NewService(storagereach.ServiceDeps{
			Redis: rdb, CoreID: "core-a", NewProvider: factory,
		}),
		// Shortened from the 15s default so a lost discovery race below fails
		// fast, but deliberately ABOVE the round's 1s poll cadence, unlike the
		// refusal tests' 150ms.
		//
		// checkSharedStorageReachable leaves RoundOptions.PollEvery unset, so it
		// is the production 1s, and PollEvery is also the retry cadence of the
		// coordinator's own Probe. A sub-second deadline therefore buys the
		// coordinator exactly ONE listing, taken microseconds after the round is
		// published - before any peer could physically have written its beacon -
		// so core-a would report not-shared no matter how fast core-b is. This
		// is the mismatch RoundOptions.PollEvery's own doc comment warns about.
		// A deadline past one poll gives the coordinator a second listing, which
		// is what this positive control needs; the round still returns as soon
		// as both reports are in, so the real cost is ~1s, not 1.2s.
		reachRoundDeadline: 1200 * time.Millisecond,
	}}

	done := make(chan error, 1)
	go func() {
		ctx := context.Background()
		for i := 0; i < 300; i++ {
			id, err := storagereach.PendingRoundID(ctx, rdb)
			if err == nil && id != "" {
				done <- storagereach.RunParticipant(ctx, rdb, "core-b", id, factory)
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		done <- fmt.Errorf("core-b never saw a round")
	}()

	body := `{"backend":"s3","s3Bucket":"b","s3AccessKey":"AKIA","s3SecretKey":"secret"}`
	rec := httptest.NewRecorder()
	h.SaveConfig(rec, httptest.NewRequest(http.MethodPost, "/api/settings/core-storage", strings.NewReader(body)))

	if perr := <-done; perr != nil {
		t.Fatalf("core-b participant: %v", perr)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if st.values[keyCoreStorageBackend] != "s3" {
		t.Errorf("backend persisted as %q, want %q", st.values[keyCoreStorageBackend], "s3")
	}
}

// TestSaveConfig_RefusesWhenTheVerifierIsNotRunning covers the "could not
// verify = refuse" rule at the SaveConfig level: a Core with no StorageReach
// service (e.g. built by tooling, or before boot wiring completes) must not
// let a multi-Core save through just because it cannot check it.
func TestSaveConfig_RefusesWhenTheVerifierIsNotRunning(t *testing.T) {
	st := multiCoreState(t, map[string]string{})
	h := &CoreStorageHandler{state: &AppState{Store: st, Redis: multiCoreRedis(t, "core-a", "core-b")}}

	body := `{"backend":"s3","s3Bucket":"b","s3AccessKey":"AKIA","s3SecretKey":"secret"}`
	rec := httptest.NewRecorder()
	h.SaveConfig(rec, httptest.NewRequest(http.MethodPost, "/api/settings/core-storage", strings.NewReader(body)))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}
	if st.writes != 0 {
		t.Errorf("the refused save persisted %d settings; it must persist none", st.writes)
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
