package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestValidatePublicBaseURL pins the input rules for the two mirror-base
// settings. The value is handed to third-party launchers AND feeds
// isSnapshotFetchHostAllowed's SSRF allowlist, so a scheme-less or hostless
// string must not persist.
func TestValidatePublicBaseURL(t *testing.T) {
	cases := []struct {
		name string
		in   string
		ok   bool
	}{
		{"empty is allowed (feature simply unconfigured)", "", true},
		{"https origin", "https://panel.example.com", true},
		{"http origin", "http://10.0.0.5:25500", true},
		{"origin with a path", "https://cdn.example.com/modpacks", true},
		{"trailing slash", "https://cdn.example.com/modpacks/", true},
		{"no scheme", "panel.example.com", false},
		{"scheme-relative", "//panel.example.com", false},
		{"unsupported scheme", "ftp://panel.example.com", false},
		{"file scheme", "file:///etc/passwd", false},
		{"host missing", "https://", false},
		{"embedded credentials", "https://user:pw@panel.example.com", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validatePublicBaseURL("core public URL", c.in)
			if c.ok && err != nil {
				t.Fatalf("validatePublicBaseURL(%q) = %v, want nil", c.in, err)
			}
			if !c.ok && err == nil {
				t.Fatalf("validatePublicBaseURL(%q) = nil, want an error", c.in)
			}
		})
	}
}

// TestModpackSettingsHandler_Set_PersistsMirrorBases is the regression guard
// for the shipped defect: both keys were READ by solderMirrorBase and written
// by nothing at all, so the public Solder pack list answered 500 on every
// install with no supported way to fix it.
func TestModpackSettingsHandler_Set_PersistsMirrorBases(t *testing.T) {
	fs := newCoreStorageHTTPFakeStore()
	h := newModpackSettingsTestHandler(fs)

	body, _ := json.Marshal(modpackSettings{
		Provider:        "local",
		CorePublicURL:   "https://panel.example.com/",
		SolderMirrorURL: "https://cdn.example.com/packs",
	})
	rw := httptest.NewRecorder()
	h.Set(rw, httptest.NewRequest(http.MethodPut, "/api/admin/settings/modpacks", bytes.NewReader(body)))
	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rw.Code, rw.Body.String())
	}

	for k, want := range map[string]string{
		"core_public_url":   "https://panel.example.com/",
		"solder_mirror_url": "https://cdn.example.com/packs",
	} {
		if got := fs.kv[k]; got != want {
			t.Errorf("settings[%q] = %q, want %q", k, got, want)
		}
	}

	// And GET must echo them back, or the panel form loads blank and the next
	// save silently clears what was just configured.
	rw = httptest.NewRecorder()
	h.Get(rw, httptest.NewRequest(http.MethodGet, "/api/admin/settings/modpacks", nil))
	var out struct {
		Settings modpackSettings `json:"settings"`
	}
	if err := json.Unmarshal(rw.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode GET: %v", err)
	}
	if out.Settings.CorePublicURL != "https://panel.example.com/" {
		t.Errorf("GET corePublicUrl = %q, want the saved value", out.Settings.CorePublicURL)
	}
	if out.Settings.SolderMirrorURL != "https://cdn.example.com/packs" {
		t.Errorf("GET solderMirrorUrl = %q, want the saved value", out.Settings.SolderMirrorURL)
	}
}

func TestModpackSettingsHandler_Set_RejectsBadMirrorBase(t *testing.T) {
	for _, c := range []struct{ name, core, mirror string }{
		{"bad core public url", "panel.example.com", ""},
		{"bad solder mirror url", "", "ftp://cdn.example.com"},
	} {
		t.Run(c.name, func(t *testing.T) {
			fs := newCoreStorageHTTPFakeStore()
			h := newModpackSettingsTestHandler(fs)

			body, _ := json.Marshal(modpackSettings{
				Provider:        "local",
				CorePublicURL:   c.core,
				SolderMirrorURL: c.mirror,
			})
			rw := httptest.NewRecorder()
			h.Set(rw, httptest.NewRequest(http.MethodPut, "/api/admin/settings/modpacks", bytes.NewReader(body)))

			if rw.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (%s)", rw.Code, rw.Body.String())
			}
			if _, ok := fs.kv["modpack_storage_provider"]; ok {
				t.Errorf("a rejected save still reached the store, kv = %v", fs.kv)
			}
		})
	}
}
