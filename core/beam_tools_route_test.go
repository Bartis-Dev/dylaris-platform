package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// /api/tools/beam is the public, session-less "download Beam" link. It used to
// build its own redirect to the beam-relay; see beamToolsRedirect for what was
// wrong with that. What is worth pinning here is that it hands off to
// GetBeamDownload and cannot grow a relay host back.
//
// The handler is tested directly rather than through stubRouter: every /api/*
// route sits behind the setup-lock and maintenance middlewares, both of which
// hit the store, so that router can be walked but not served.
func TestBeamToolsRedirect(t *testing.T) {
	const winUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64)"

	get := func(t *testing.T, target, ua string) string {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, target, nil)
		if ua != "" {
			req.Header.Set("User-Agent", ua)
		}
		rec := httptest.NewRecorder()
		beamToolsRedirect(rec, req)
		if rec.Code != http.StatusFound {
			t.Fatalf("%s: status = %d, want %d", target, rec.Code, http.StatusFound)
		}
		return rec.Header().Get("Location")
	}

	t.Run("a bare link detects the platform from the User-Agent", func(t *testing.T) {
		// Without the detection GetBeamDownload answers a platform-less
		// request with its platform index (JSON), not a download - so the
		// human-facing link would render a JSON blob in the browser.
		if got, want := get(t, "/api/tools/beam", winUA), "/api/beam/download?platform=windows-amd64"; got != want {
			t.Errorf("Location = %q, want %q", got, want)
		}
	})

	t.Run("an explicit platform is carried through", func(t *testing.T) {
		if got, want := get(t, "/api/tools/beam?platform=linux-arm64", winUA), "/api/beam/download?platform=linux-arm64"; got != want {
			t.Errorf("Location = %q, want %q", got, want)
		}
	})

	t.Run("the redirect never names a host", func(t *testing.T) {
		// The reason GetBeamDownload streams the binary instead of redirecting
		// is that the browser must never learn the upstream's address, which is
		// typically an overlay IP. A scheme in the Location means that is gone.
		for _, target := range []string{
			"/api/tools/beam",
			"/api/tools/beam?platform=linux-amd64",
			"/api/tools/beam?platform=https%3A%2F%2Fevil.example%2Fx",
		} {
			loc := get(t, target, winUA)
			if !strings.HasPrefix(loc, "/api/beam/download?platform=") {
				t.Errorf("%s: Location = %q, want a same-origin /api/beam/download", target, loc)
			}
			if strings.Contains(loc, "://") {
				t.Errorf("%s: Location = %q leaks an absolute URL", target, loc)
			}
		}
	})

	t.Run("a hostile platform cannot break out of the query value", func(t *testing.T) {
		// The platform whitelist itself lives in GetBeamDownload. All this has
		// to guarantee is that the value arrives there as one parameter instead
		// of being spliced into the URL.
		loc := get(t, "/api/tools/beam?platform=x%26admin%3D1", winUA)
		if strings.Contains(loc, "&") {
			t.Errorf("Location = %q: the platform must not be able to add a parameter", loc)
		}
		if !strings.Contains(loc, "%26") {
			t.Errorf("Location = %q, want the ampersand escaped", loc)
		}
	})
}
