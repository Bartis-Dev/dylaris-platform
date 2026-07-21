package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestSaveBeamSettings_PersistsUploadLimits guards the two new upload-limit
// settings: SaveBeamSettings must persist them under the beam.* DB keys the
// node-side Redis publish mirrors, and LoadBeamSettings must round-trip them.
// Redis is nil here (the publish block is guarded), so this covers only the
// durable DB path; the live Redis publish is exercised on a real instance.
func TestSaveBeamSettings_PersistsUploadLimits(t *testing.T) {
	fs := newCoreStorageHTTPFakeStore()
	h := &SettingsHandler{state: &AppState{Store: fs}}

	const maxUpload = int64(5) << 30    // 5 GiB
	const dailyUpload = int64(20) << 30 // 20 GiB

	body, _ := json.Marshal(BeamSettings{
		MaxUploadBytes:   maxUpload,
		DailyUploadBytes: dailyUpload,
	})
	rw := httptest.NewRecorder()
	h.SaveBeamSettings(rw, httptest.NewRequest(http.MethodPost, "/api/settings/beam", bytes.NewReader(body)))

	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rw.Code, rw.Body.String())
	}

	if got := fs.kv["beam.max_upload_bytes"]; got != "5368709120" {
		t.Errorf("stored beam.max_upload_bytes = %q, want %q", got, "5368709120")
	}
	if got := fs.kv["beam.daily_upload_bytes"]; got != "21474836480" {
		t.Errorf("stored beam.daily_upload_bytes = %q, want %q", got, "21474836480")
	}

	loaded := h.LoadBeamSettings()
	if loaded.MaxUploadBytes != maxUpload {
		t.Errorf("loaded MaxUploadBytes = %d, want %d", loaded.MaxUploadBytes, maxUpload)
	}
	if loaded.DailyUploadBytes != dailyUpload {
		t.Errorf("loaded DailyUploadBytes = %d, want %d", loaded.DailyUploadBytes, dailyUpload)
	}
}

// TestSaveBeamSettings_UploadLimitsDefaultZero pins that an unset limit stays 0
// (unlimited) through a round-trip, so the feature is inert until an admin sets
// a value.
func TestSaveBeamSettings_UploadLimitsDefaultZero(t *testing.T) {
	fs := newCoreStorageHTTPFakeStore()
	h := &SettingsHandler{state: &AppState{Store: fs}}

	body, _ := json.Marshal(BeamSettings{})
	rw := httptest.NewRecorder()
	h.SaveBeamSettings(rw, httptest.NewRequest(http.MethodPost, "/api/settings/beam", bytes.NewReader(body)))
	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rw.Code, rw.Body.String())
	}

	loaded := h.LoadBeamSettings()
	if loaded.MaxUploadBytes != 0 || loaded.DailyUploadBytes != 0 {
		t.Errorf("defaults = (%d, %d), want (0, 0)", loaded.MaxUploadBytes, loaded.DailyUploadBytes)
	}
}
