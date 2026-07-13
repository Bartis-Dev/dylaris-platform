package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestInjectShellToken(t *testing.T) {
	token := "deadbeefcafe0000"
	want := `<script>window.__beamShellToken="deadbeefcafe0000"</script>`

	// Spliced immediately BEFORE </head>.
	out := string(injectShellToken([]byte("<html><head><title>x</title></head><body></body></html>"), token))
	if !strings.Contains(out, want) {
		t.Fatalf("token script not spliced: %q", out)
	}
	if strings.Index(out, want) > strings.Index(out, "</head>") {
		t.Errorf("token script must be before </head>: %q", out)
	}

	// The token is Go-quoted (%q): a value with a quote in it would be escaped.
	// A hex token has none, so the exact literal above is what we expect.

	// No </head>/<body> anchor: falls back to prepend.
	out2 := string(injectShellToken([]byte("<div>no head</div>"), token))
	if !strings.HasPrefix(out2, want) {
		t.Errorf("fallback prepend failed: %q", out2)
	}
}

func TestServeBeamIndexInjectsTokenAndFraming(t *testing.T) {
	app := &App{shellToken: "abc123token"}
	// Fake asset server: returns a minimal HTML doc (stands in for the Wails
	// asset server's auto-injected /__beam/ index.html).
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/index.html" {
			t.Errorf("serveEmbedded rewrote path to %q, want /index.html", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<html><head></head><body>shell</body></html>"))
	})

	req := httptest.NewRequest(http.MethodGet, "/__beam/", nil)
	rec := httptest.NewRecorder()
	serveBeamIndex(app, next, rec, req)

	res := rec.Result()
	body, _ := io.ReadAll(res.Body)
	if !strings.Contains(string(body), `window.__beamShellToken="abc123token"`) {
		t.Errorf("token not injected into /__beam/: %s", body)
	}
	if got := res.Header.Get("X-Frame-Options"); got != "DENY" {
		t.Errorf("X-Frame-Options = %q, want DENY", got)
	}
	if got := res.Header.Get("Content-Security-Policy"); got != "frame-ancestors 'none'" {
		t.Errorf("CSP = %q, want frame-ancestors 'none'", got)
	}
	if got := res.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
}

func TestServeBeamIndexTokenGatedByFetchDest(t *testing.T) {
	newNext := func() http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte("<html><head></head><body>shell</body></html>"))
		})
	}

	assertFramingHeaders := func(t *testing.T, res *http.Response) {
		t.Helper()
		if got := res.Header.Get("X-Frame-Options"); got != "DENY" {
			t.Errorf("X-Frame-Options = %q, want DENY", got)
		}
		if got := res.Header.Get("Content-Security-Policy"); got != "frame-ancestors 'none'" {
			t.Errorf("CSP = %q, want frame-ancestors 'none'", got)
		}
	}

	t.Run("document navigation gets the token", func(t *testing.T) {
		app := &App{shellToken: "abc123token"}
		req := httptest.NewRequest(http.MethodGet, "/__beam/", nil)
		req.Header.Set("Sec-Fetch-Dest", "document")
		rec := httptest.NewRecorder()
		serveBeamIndex(app, newNext(), rec, req)

		res := rec.Result()
		body, _ := io.ReadAll(res.Body)
		if !strings.Contains(string(body), `window.__beamShellToken="abc123token"`) {
			t.Errorf("token not injected for Sec-Fetch-Dest: document: %s", body)
		}
		assertFramingHeaders(t, res)
	})

	t.Run("fetch/XHR gets a token-less page", func(t *testing.T) {
		app := &App{shellToken: "abc123token"}
		req := httptest.NewRequest(http.MethodGet, "/__beam/", nil)
		req.Header.Set("Sec-Fetch-Dest", "empty")
		rec := httptest.NewRecorder()
		serveBeamIndex(app, newNext(), rec, req)

		res := rec.Result()
		body, _ := io.ReadAll(res.Body)
		if strings.Contains(string(body), "__beamShellToken") {
			t.Errorf("token must NOT be injected for Sec-Fetch-Dest: empty (compromised same-origin fetch): %s", body)
		}
		// The page must still be served, just without the token, and framing
		// defenses must still apply.
		if res.StatusCode != http.StatusOK {
			t.Errorf("status = %d, want 200 (page still served, just token-less)", res.StatusCode)
		}
		assertFramingHeaders(t, res)
	})

	t.Run("absent header is a safe fallback that still delivers the token", func(t *testing.T) {
		app := &App{shellToken: "abc123token"}
		req := httptest.NewRequest(http.MethodGet, "/__beam/", nil)
		// No Sec-Fetch-Dest header set: some webviews don't send Fetch Metadata.
		rec := httptest.NewRecorder()
		serveBeamIndex(app, newNext(), rec, req)

		res := rec.Result()
		body, _ := io.ReadAll(res.Body)
		if !strings.Contains(string(body), `window.__beamShellToken="abc123token"`) {
			t.Errorf("token not injected when Sec-Fetch-Dest is absent (safe fallback): %s", body)
		}
		assertFramingHeaders(t, res)
	})
}
