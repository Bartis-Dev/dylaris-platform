package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"dylaris-core/services"
	"dylaris-core/store"
)

// gateFakeStore serves GetSetting from a map; embeds store.Store for the rest.
type gateFakeStore struct {
	store.Store
	kv map[string]string
}

func (f *gateFakeStore) GetSetting(key string) (string, error) { return f.kv[key], nil }

// SetSetting persists into the same map GetSetting reads from. Needed once a
// test drives FeatureSettingsHandler.Set() past its guards into the actual
// writes loop (e.g. the tickets:false success path), which calls SetSetting
// for every toggle in the bundle.
func (f *gateFakeStore) SetSetting(key, value string) error {
	if f.kv == nil {
		f.kv = map[string]string{}
	}
	f.kv[key] = value
	return nil
}

func TestRequireCoreStorageConfigured(t *testing.T) {
	called := false
	inner := func(w http.ResponseWriter, r *http.Request) { called = true; w.WriteHeader(http.StatusOK) }

	t.Run("blocks when unconfigured", func(t *testing.T) {
		called = false
		st := &AppState{Store: &gateFakeStore{kv: map[string]string{}}}
		rw := httptest.NewRecorder()
		st.RequireCoreStorageConfigured(inner)(rw, httptest.NewRequest(http.MethodPost, "/x", nil))
		if called {
			t.Fatal("inner ran despite unconfigured storage")
		}
		if rw.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503", rw.Code)
		}
	})

	t.Run("passes when configured (path)", func(t *testing.T) {
		called = false
		st := &AppState{Store: &gateFakeStore{kv: map[string]string{
			keyCoreStorageBackend:     "path",
			keyCoreStoragePath:        "/mnt/shared",
			keyCoreStoragePathConfirm: "true",
		}}}
		rw := httptest.NewRecorder()
		st.RequireCoreStorageConfigured(inner)(rw, httptest.NewRequest(http.MethodPost, "/x", nil))
		if !called {
			t.Fatalf("inner did not run; status = %d body %s", rw.Code, rw.Body.String())
		}
	})
}

func TestFeatureSettings_RefusesTicketsWhenStorageUnconfigured(t *testing.T) {
	newHandler := func() *FeatureSettingsHandler {
		st := &AppState{
			Store:  &gateFakeStore{kv: map[string]string{}},
			Events: services.NewSystemEventsPublisher(nil),
		}
		st.FeatureFlags = services.NewFeatureFlags(st.Store)
		return NewFeatureSettingsHandler(st)
	}

	t.Run("tickets true is refused when storage unconfigured", func(t *testing.T) {
		h := newHandler()
		rw := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPut, "/api/admin/settings/features", strings.NewReader(`{"tickets":true,"modpacks":true,"autoMove":false}`))
		h.Set(rw, req)
		if rw.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409 when enabling tickets without storage (%s)", rw.Code, rw.Body.String())
		}
	})

	// The other half of the ON/OFF asymmetry: an operator must never be
	// trapped in an enabled-but-broken state, so turning Tickets OFF (or
	// leaving it off) must always succeed regardless of storage config.
	t.Run("tickets false always succeeds when storage unconfigured", func(t *testing.T) {
		h := newHandler()
		rw := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPut, "/api/admin/settings/features", strings.NewReader(`{"tickets":false,"modpacks":false,"autoMove":false}`))
		h.Set(rw, req)
		if rw.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: disabling tickets must never be blocked by the storage gate (%s)", rw.Code, rw.Body.String())
		}
	})
}
