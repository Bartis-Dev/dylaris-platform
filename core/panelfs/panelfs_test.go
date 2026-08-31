package panelfs

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

// exportLike is the shape `next build` with output: export actually produces,
// taken from a real run: flat <route>.html files, directories for nesting, and
// the placeholder segment for every dynamic route.
func exportLike() fstest.MapFS {
	page := func(name string) *fstest.MapFile {
		return &fstest.MapFile{Data: []byte(
			`<!doctype html><html><head><script nonce="` + NoncePlaceholder + `" src="/_next/x.js"></script>` +
				`</head><body>` + name + `<script nonce="` + NoncePlaceholder + `">self.__x=1</script></body></html>`)}
	}
	return fstest.MapFS{
		"index.html":                             page("home"),
		"404.html":                               page("notfound"),
		"login.html":                             page("login"),
		"servers.html":                           page("servers"),
		"servers/" + ExportParam + ".html":       page("server"),
		"servers/" + ExportParam + "/audit.html": page("audit"),
		"servers/" + ExportParam + "/t/" + ExportParam + ".html": page("tab"),
		"settings/backups.html":                                  page("settings-backups"),
		"tickets.html":                                           page("tickets"),
		"tickets/new.html":                                       page("ticket-new"),
		"tickets/" + ExportParam + "/history.html":               page("ticket-history"),
		"tickets/" + ExportParam + ".html":                       page("ticket"),
		// Next writes an RSC segment payload beside every page, and asks for it
		// during a client-side navigation. Under a dynamic route it lives beside
		// the wildcard, so only the route tree can find it.
		"servers/" + ExportParam + "/__next._tree.txt": {Data: []byte("tree-payload")},
		"servers/" + ExportParam + "/console.txt":      {Data: []byte("console-payload")},
		"servers.txt":                {Data: []byte("servers-payload")},
		"_next/static/chunks/abc.js": {Data: []byte("chunk")},
		"config.js":                  {Data: []byte("window.__DYLARIS_CONFIG__={apiUrl:\"stale\"}")},
	}
}

func testHandler(t *testing.T, apiURL, tabSuffix string) *Handler {
	t.Helper()
	fsys := exportLike()
	tree, err := buildRouteTree(fsys)
	if err != nil {
		t.Fatalf("buildRouteTree: %v", err)
	}
	return &Handler{
		files:    fsys,
		routes:   tree,
		configJS: renderConfigJS(apiURL),
		csp:      cspConfig{apiOrigin: originOf(apiURL), tabSuffix: normalizeTabSuffix(tabSuffix)},
		Built:    true,
	}
}

func get(t *testing.T, h *Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

// The routing table the panel's file layout implies, including the case the
// whole design turns on: a real page sitting beside a dynamic one.
func TestRouteResolution(t *testing.T) {
	h := testHandler(t, "", "")
	tests := []struct {
		path, want string
	}{
		{"/", "home"},
		{"/login", "login"},
		{"/servers", "servers"},
		{"/servers/42", "server"},
		{"/servers/42/audit", "audit"},
		{"/servers/42/t/console", "tab"},
		{"/settings/backups", "settings-backups"},
		{"/tickets", "tickets"},
		// The one that a naive matcher gets wrong. "new" is a real page and
		// "tickets/[id]" is a dynamic route at the same depth: an exact segment
		// has to beat the wildcard, or /tickets/new opens a ticket whose id is
		// the word "new".
		{"/tickets/new", "ticket-new"},
		{"/tickets/915", "ticket"},
		// The case backtracking exists for. "new" matches an exact child, but that
		// child has no "history" below it - the answer is one level back, down the
		// wildcard. A matcher that commits to the first exact segment returns 404
		// here while every other route in this table still works.
		{"/tickets/new/history", "ticket-history"},
		// Trailing slashes and dot segments are normalised rather than 404ing.
		{"/servers/42/audit/", "audit"},
		{"/login/", "login"},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			rec := get(t, h, tt.path)
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d, want 200", rec.Code)
			}
			if !strings.Contains(rec.Body.String(), tt.want) {
				t.Errorf("served the wrong page for %s: want %q", tt.path, tt.want)
			}
		})
	}
}

// An unknown path is a 404 with the panel's own page, not a silent redirect to
// the dashboard: a dead link should say it is dead.
func TestUnknownPathIsA404(t *testing.T) {
	rec := get(t, testHandler(t, "", ""), "/nope/nothing/here")
	if rec.Code != http.StatusNotFound {
		t.Errorf("status %d, want 404", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "notfound") {
		t.Errorf("body is not the 404 page: %q", rec.Body.String())
	}
}

// Every response gets its OWN nonce, and the header and the body carry the same
// one. A shared or stale nonce is a page whose own scripts the browser refuses.
func TestNonceIsPerResponseAndMatchesTheHeader(t *testing.T) {
	h := testHandler(t, "", "")
	first := get(t, h, "/login")
	second := get(t, h, "/login")

	nonce := func(rec *httptest.ResponseRecorder) string {
		csp := rec.Header().Get("Content-Security-Policy")
		i := strings.Index(csp, "'nonce-")
		if i < 0 {
			t.Fatalf("no nonce in the policy: %q", csp)
		}
		rest := csp[i+len("'nonce-"):]
		return rest[:strings.IndexByte(rest, '\'')]
	}
	n1, n2 := nonce(first), nonce(second)
	if n1 == n2 {
		t.Error("two responses shared a nonce; it must be minted per response")
	}
	if n1 == "" || len(n1) < 16 {
		t.Errorf("nonce %q is too short to be worth having", n1)
	}
	for _, tc := range []struct {
		rec   *httptest.ResponseRecorder
		nonce string
	}{{first, n1}, {second, n2}} {
		body := tc.rec.Body.String()
		if strings.Contains(body, NoncePlaceholder) {
			t.Error("the placeholder survived into the response; nothing would run")
		}
		// Every script tag in the response, the injected config included, and
		// no tag without one: a single un-nonced tag is a script the browser
		// refuses while the rest of the page looks fine.
		tags := strings.Count(body, "<script")
		if got := strings.Count(body, `nonce="`+tc.nonce+`"`); got != tags {
			t.Errorf("%d of %d script tags carry the header's nonce: %q", got, tags, body)
		}
	}
}

// HTML must never be cached: the nonce in the body and the nonce in the header
// have to be the same value, and a cached copy of one without the other is a
// page that blocks its own scripts.
func TestHTMLIsNeverCachedAndChunksAreCachedForever(t *testing.T) {
	h := testHandler(t, "", "")
	if got := get(t, h, "/login").Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("HTML Cache-Control = %q, want no-store", got)
	}
	chunk := get(t, h, "/_next/static/chunks/abc.js")
	if chunk.Code != http.StatusOK {
		t.Fatalf("chunk status %d", chunk.Code)
	}
	if got := chunk.Header().Get("Cache-Control"); !strings.Contains(got, "immutable") {
		t.Errorf("hashed chunk Cache-Control = %q, want immutable", got)
	}
	if chunk.Header().Get("Content-Security-Policy") != "" {
		t.Error("a static chunk carries a CSP it does not need")
	}
}

// The runtime config is INJECTED into the page, not fetched as a file.
//
// A separate /config.js was the first shape, and it did not survive contact with
// the browser: Next loads a beforeInteractive script from its client runtime,
// which has no nonce to give it, so the strict policy blocked it and the panel
// fell back to a same-origin API URL with only a console message to say so.
func TestRuntimeConfigIsInjectedIntoTheHead(t *testing.T) {
	h := testHandler(t, "https://api.example.com/api", "")
	rec := get(t, h, "/login")
	body := rec.Body.String()

	if !strings.Contains(body, "https://api.example.com/api") {
		t.Errorf("the page does not carry the configured API URL: %q", body)
	}
	// Immediately after <head>, so it runs before any bundle script resolves the
	// URL - position is what guarantees the ordering now, not a strategy flag.
	head := strings.Index(body, "<head>")
	cfg := strings.Index(body, "__DYLARIS_CONFIG__")
	firstSrc := strings.Index(body, "<script src=")
	if head < 0 || cfg < head {
		t.Fatalf("the config script is not inside <head>: head=%d cfg=%d", head, cfg)
	}
	if firstSrc >= 0 && cfg > firstSrc {
		t.Error("the config runs after a bundle script; the API URL would already be resolved")
	}
	// And it carries the response's nonce, or the browser refuses to run it.
	csp := rec.Header().Get("Content-Security-Policy")
	k := strings.Index(csp, "'nonce-")
	nonce := csp[k+len("'nonce-"):]
	nonce = nonce[:strings.IndexByte(nonce, 0x27)] // 0x27 = apostrophe, written numerically
	from := cfg - 120
	if from < 0 {
		from = 0
	}
	if !strings.Contains(body[from:cfg], nonce) {
		t.Error("the injected config script has no nonce")
	}
}

// A page with no <head> is left alone rather than patched at a guessed
// position: same-origin is the right answer for almost every deployment, and a
// script injected in the wrong place would fail for all of them silently.
func TestConfigInjectionSkipsAPageWithNoHead(t *testing.T) {
	in := []byte("<html><body>nope</body></html>")
	if got := injectConfig(in, "N", []byte("X")); string(got) != string(in) {
		t.Errorf("page was modified: %q", got)
	}
}

// A URL an operator typed cannot break out of the string it is rendered into.
func TestConfigJSEscapesTheOperatorValue(t *testing.T) {
	nasty := "https://x.example/\"; window.evil=1; //"
	body := string(renderConfigJS(nasty))
	if strings.Contains(body, `"; window.evil`) {
		t.Errorf("the value escaped its string literal: %q", body)
	}
	if !strings.Contains(body, `u0022`) {
		t.Errorf("the quote was not escaped: %q", body)
	}
}

// The policy carries the API origin only when there IS one, and never a path.
func TestCSPCarriesTheApiOriginWithoutItsPath(t *testing.T) {
	same := testHandler(t, "", "").csp.build("N")
	if strings.Contains(same, "connect-src 'self' http") {
		t.Errorf("a same-origin deployment named an API host: %q", same)
	}
	split := testHandler(t, "https://api.example.com/api", "share.example.com").csp.build("N")
	if !strings.Contains(split, "connect-src 'self' https://api.example.com *.share.example.com") {
		t.Errorf("connect-src = %q", split)
	}
	if strings.Contains(split, "api.example.com/api") {
		t.Error("connect-src carries a path, which is not a valid host-source")
	}
	// img-src needs it too: the server-icon preview is an <img> at Core.
	if !strings.Contains(split, "img-src 'self' data: blob: https://api.example.com") {
		t.Errorf("img-src = %q", split)
	}
}

// The tab suffix is cleaned the way Core cleans it, because an operator pastes
// it out of a proxy config.
func TestTabSuffixNormalisation(t *testing.T) {
	for _, tt := range []struct{ in, want string }{
		{"share.example.com", "share.example.com"},
		{"https://share.example.com/", "share.example.com"},
		{".share.example.com.", "share.example.com"},
		{"share.example.com:443", "share.example.com"},
		{"SHARE.Example.COM", "share.example.com"},
		// A single label cannot be a suffix under which per-tab hosts live, and
		// "*.localhost" would be a wide, silent allowance.
		{"localhost", ""},
		{"", ""},
	} {
		if got := normalizeTabSuffix(tt.in); got != tt.want {
			t.Errorf("normalizeTabSuffix(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// The placeholder bundle has to be distinguishable from a real one, or a Core
// built without the panel serves a page nobody wants and says nothing.
func TestBuiltFlagDetectsThePlaceholder(t *testing.T) {
	h, err := New("", "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// The repo carries the placeholder; CI overwrites dist/ with the export.
	// Whichever is embedded here, Built must agree with whether _next exists.
	if h.Built {
		if _, err := h.files.Open("_next"); err != nil {
			t.Error("Built is true but there is no _next directory")
		}
	} else if h.Routes() == 0 {
		t.Error("even the placeholder must answer at least one route")
	}
}

// script-src stays nonce-strict, which is the property the whole nonce
// machinery exists to hold up.
//
// Ported from the panel's csp.test.ts when the policy moved into Go, minus its
// dev case: there is no dev mode here, because the panel's dev server serves its
// own pages and never reaches this handler. A dev switch could only ever loosen
// the policy in production.
func TestScriptSrcIsNonceStrict(t *testing.T) {
	csp := cspConfig{}.build("abc123")
	var scriptSrc string
	for _, d := range strings.Split(csp, "; ") {
		if strings.HasPrefix(d, "script-src ") {
			scriptSrc = d
		}
	}
	if scriptSrc == "" {
		t.Fatalf("no script-src in %q", csp)
	}
	if !strings.Contains(scriptSrc, "'nonce-abc123'") {
		t.Errorf("the nonce is not in script-src: %q", scriptSrc)
	}
	if !strings.Contains(scriptSrc, "'strict-dynamic'") {
		t.Errorf("strict-dynamic is missing: %q", scriptSrc)
	}
	for _, forbidden := range []string{"'unsafe-inline'", "'unsafe-eval'"} {
		if strings.Contains(scriptSrc, forbidden) {
			t.Errorf("script-src carries %s: %q", forbidden, scriptSrc)
		}
	}
	// frame-ancestors, not X-Frame-Options: the panel is never framed, and the
	// tab hosts that DO frame things never reach this handler.
	if !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("frame-ancestors is missing: %q", csp)
	}
}

// Next's client router fetches an RSC segment payload on every soft navigation,
// and under a dynamic route that payload is exported beside the wildcard:
// servers/__param__/console.txt answers /servers/7/console.txt.
//
// The route tree indexed only .html, so those 404'd - 232 files in a real
// export. The app still worked, because Next falls back to a full document load
// when the payload is missing, so the only symptom was a console 404 and every
// navigation into a server behaving like a page reload. Exactly the kind of
// thing that survives a green test suite and a working-looking click-through.
func TestSegmentPayloadsResolveThroughTheWildcard(t *testing.T) {
	h := testHandler(t, "", "")

	cases := []struct{ path, want string }{
		{"/servers/7/__next._tree.txt", "tree-payload"},
		{"/servers/7/console.txt", "console-payload"},
		// A static route's payload is a real file at its literal path and was
		// always served; asserted so a change here cannot quietly break it.
		{"/servers.txt", "servers-payload"},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			rec := get(t, h, tc.path)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			if body := rec.Body.String(); !strings.Contains(body, tc.want) {
				t.Errorf("body = %q, want it to contain %q", body, tc.want)
			}
			// It is NOT an HTML document: served as HTML it would be handed the
			// config script and a CSP, and Next would fail to parse it.
			if ct := rec.Header().Get("Content-Type"); strings.HasPrefix(ct, "text/html") {
				t.Errorf("Content-Type = %q; a segment payload is not a document", ct)
			}
			if strings.Contains(rec.Body.String(), "__DYLARIS_CONFIG__") {
				t.Error("the config script was injected into a segment payload")
			}
		})
	}
}

// And a miss is still a miss: an unknown .txt under a dynamic route must not be
// answered by some other segment's payload.
func TestUnknownSegmentPayloadIsNotSubstituted(t *testing.T) {
	h := testHandler(t, "", "")
	rec := get(t, h, "/servers/7/nosuchtab.txt")
	if rec.Code == http.StatusOK && strings.Contains(rec.Body.String(), "payload") {
		t.Fatalf("an unknown segment was answered with %q", rec.Body.String())
	}
}
