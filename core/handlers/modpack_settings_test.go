package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"dylaris-core/services"
)

// newModpackSettingsTestHandler wires a ModpackSettingsHandler against
// coreStorageHTTPFakeStore (core_storage_http_test.go), which records
// SetSetting writes into a map so a rejected request can be asserted to
// never reach the store. FeatureFlags/Events are wired the same way
// core_storage_gate_test.go does for FeatureSettingsHandler, since Set()
// unconditionally invalidates flags and publishes events on success.
func newModpackSettingsTestHandler(fs *coreStorageHTTPFakeStore) *ModpackSettingsHandler {
	st := &AppState{
		Store:  fs,
		Events: services.NewSystemEventsPublisher(nil),
	}
	st.FeatureFlags = services.NewFeatureFlags(st.Store)
	return NewModpackSettingsHandler(st)
}

// TestModpackSettingsHandler_Set_ProviderValidation is the regression guard
// for the dead core-storage branch: "core-storage" must be persistable, and
// an unknown value must be rejected with 400 before it ever reaches the
// store.
func TestModpackSettingsHandler_Set_ProviderValidation(t *testing.T) {
	cases := []struct {
		name     string
		provider string
		wantCode int
	}{
		{"local is accepted", "local", http.StatusOK},
		{"s3 is accepted", "s3", http.StatusOK},
		{"core-storage is accepted", "core-storage", http.StatusOK},
		{"empty defaults to local and is accepted", "", http.StatusOK},
		{"unknown provider is rejected", "ftp", http.StatusBadRequest},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fs := newCoreStorageHTTPFakeStore()
			h := newModpackSettingsTestHandler(fs)

			body, _ := json.Marshal(modpackSettings{Provider: c.provider})
			rw := httptest.NewRecorder()
			h.Set(rw, httptest.NewRequest(http.MethodPut, "/api/admin/settings/modpacks", bytes.NewReader(body)))

			if rw.Code != c.wantCode {
				t.Fatalf("status = %d, want %d (%s)", rw.Code, c.wantCode, rw.Body.String())
			}

			if c.wantCode == http.StatusBadRequest {
				if _, ok := fs.kv["modpack_storage_provider"]; ok {
					t.Errorf("rejected provider %q reached the store, kv = %v", c.provider, fs.kv)
				}
				return
			}
			wantStored := c.provider
			if wantStored == "" {
				wantStored = "local"
			}
			if fs.kv["modpack_storage_provider"] != wantStored {
				t.Errorf("stored provider = %q, want %q", fs.kv["modpack_storage_provider"], wantStored)
			}
		})
	}
}

// TestModpackSettingsHandler_Set_AllowsMovingFromAnyStoredValueToValid
// guards the "never trap an operator" constraint: a row that already holds
// some other value (including a stale/unrecognized one) must still be
// updatable to any currently-valid provider, since Set only validates the
// incoming request and never conditions on what is already stored.
func TestModpackSettingsHandler_Set_AllowsMovingFromAnyStoredValueToValid(t *testing.T) {
	fs := newCoreStorageHTTPFakeStore()
	fs.kv["modpack_storage_provider"] = "some-legacy-value"
	h := newModpackSettingsTestHandler(fs)

	body, _ := json.Marshal(modpackSettings{Provider: "core-storage"})
	rw := httptest.NewRecorder()
	h.Set(rw, httptest.NewRequest(http.MethodPut, "/api/admin/settings/modpacks", bytes.NewReader(body)))

	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rw.Code, rw.Body.String())
	}
	if fs.kv["modpack_storage_provider"] != "core-storage" {
		t.Errorf("stored provider = %q, want core-storage", fs.kv["modpack_storage_provider"])
	}
}
