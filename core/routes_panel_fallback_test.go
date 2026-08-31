package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The one decision that stands between "the panel is reachable" and "every page
// is a JSON error": what an unmatched request gets.
//
// Both directions are wrong in a way that is easy to miss. Send /api/nope to the
// panel and an SDK gets HTML where it expected an error object, so it reports a
// parse failure instead of a 404. Send /servers/42 to the JSON handler and the
// panel is simply unreachable at every URL a user actually navigates to.
func TestNotFoundRoutesPagesToThePanelAndApiToJSON(t *testing.T) {
	panel := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("PANEL"))
	})
	h := notFoundHandler(panel)

	panelPaths := []string{
		"/", "/login", "/servers/42", "/servers/42/files", "/tickets/9",
		// Not an API path: the prefix has to be a whole segment. A page called
		// "apidocs" belongs to the panel.
		"/apidocs", "/api-keys",
		"/config.js", "/_next/static/chunks/abc.js",
	}
	for _, p := range panelPaths {
		t.Run("panel "+p, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h(rec, httptest.NewRequest(http.MethodGet, p, nil))
			if rec.Body.String() != "PANEL" {
				t.Errorf("%s did not reach the panel: %q", p, rec.Body.String())
			}
		})
	}

	apiPaths := []string{"/api", "/api/", "/api/nope", "/api/servers/42/does-not-exist"}
	for _, p := range apiPaths {
		t.Run("api "+p, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h(rec, httptest.NewRequest(http.MethodGet, p, nil))
			if rec.Code != http.StatusNotFound {
				t.Errorf("%s -> %d, want 404", p, rec.Code)
			}
			var body map[string]string
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("%s did not answer JSON: %q", p, rec.Body.String())
			}
			if !strings.Contains(body["error"], p) {
				t.Errorf("%s: the error does not name the path: %q", p, body["error"])
			}
		})
	}
}

// A build with no bundle answers JSON for everything, exactly as this did before
// the panel moved in. Nothing about mounting a panel may change what an API
// client sees when there is none.
func TestNotFoundWithoutAPanelStaysJSON(t *testing.T) {
	h := notFoundHandler(nil)
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/servers/42", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status %d, want 404", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
}
