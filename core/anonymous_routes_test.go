package main

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// A route registered without auth middleware and without a rate limiter can be
// called by anyone, as often as they like. Most of Core's session-less routes
// are fine that way - a health probe, a settings read, an in-memory list - and
// the ones that are not are limited: the solder mirror, /api/share/{token},
// the auth and store routes all carry an IPRateLimiter.
//
// /api/beam/download was the sibling that did not. It is the only session-less
// route that makes Core fetch a whole file from an external host and stream it
// out, so one anonymous request cost the full binary inbound plus the same
// again outbound, with nothing bounding how many could run at once.
//
// This test does not try to judge which handlers are expensive - it cannot.
// It freezes the set instead, so a new anonymous route with no limiter has to
// be argued for here rather than appearing by accident.

// Path literal as written at the registration, mapped to why it needs no limit.
var anonymousUnlimitedRoutes = map[string]string{
	"/healthz":                      "liveness probe; no DB, no external call",
	"/status":                       "token presence check, no work",
	"/system/capabilities":          "static capability catalog",
	"/system/core-info":             "static build/region info",
	"/setup/status":                 "one COUNT(); the wizard cannot be reached otherwise",
	"/maintenance":                  "public banner state; deliberately never blocked",
	"/versions/software":            "in-memory map of software names, no upstream call",
	"/auth/registration-status":     "one settings read",
	"/auth/security-questions/pool": "one settings read; the pool is public by design",
	"/tools/beam":                   "emits a Location header and nothing else",
	"/node/connect":                 "node bootstrap; the enroll token / node secret is the credential",

	// The tab proxy is reached only with an unguessable token, which is the
	// credential. Bounding it per IP would throttle the legitimate embedded
	// session, which issues many requests by design.
	"/api/servers/{id:[0-9]+}/tabs/{tabId:[0-9]+}/proxy":           "token-gated tab proxy",
	"/api/servers/{id:[0-9]+}/tabs/{tabId:[0-9]+}/proxy/{rest:.*}": "token-gated tab proxy",
	"/api/tabproxy/{token}":                                        "token-gated public tab proxy",
	"/api/tabproxy/{token}/{rest:.*}":                              "token-gated public tab proxy",

	// The Solder API answers the Technic Launcher, which carries no session,
	// and sits outside all middleware on purpose. These four are metadata
	// queries; the sibling that serves the actual files, /solder/mirror/, is
	// the one that carries a limiter.
	"/api/":                       "solder: API banner",
	"/api/modpack":                "solder: published pack list",
	"/api/modpack/{slug}":         "solder: pack metadata",
	"/api/modpack/{slug}/{build}": "solder: build metadata",
	"/api/verify/{key}":           "solder: API key check",
}

var handleFuncRe = regexp.MustCompile(`HandleFunc\("([^"]*)"`)

// callArgs returns the text between HandleFunc's parentheses, matching them so
// a registration that wraps its handler over several lines stays intact.
//
// The first version of this took everything up to the next HandleFunc instead,
// and /healthz came out looking guarded: the api.Use(MaintenanceMuxMiddleware)
// block sits between it and the next registration, dozens of lines away. A
// matcher that reaches past the call it is describing reports on the wrong
// code - the same trap as asserting on an identifier instead of a call.
func callArgs(src string, openParen int) string {
	depth := 0
	for i := openParen; i < len(src); i++ {
		switch src[i] {
		case '"':
			for i++; i < len(src) && src[i] != '"'; i++ {
				if src[i] == '\\' {
					i++
				}
			}
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return src[openParen : i+1]
			}
		}
	}
	return src[openParen:]
}

func anonymousUnlimited(t *testing.T) map[string]bool {
	t.Helper()
	b, err := os.ReadFile("routes.go")
	if err != nil {
		t.Fatalf("read routes.go: %v", err)
	}
	// Comment lines go first: a comment naming a middleware must not make a
	// route look guarded.
	var kept []string
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue
		}
		kept = append(kept, line)
	}
	src := strings.Join(kept, "\n")

	locs := handleFuncRe.FindAllStringSubmatchIndex(src, -1)
	if len(locs) == 0 {
		t.Fatal("no route registrations found; the matcher is broken, not the code")
	}

	found := map[string]bool{}
	for _, loc := range locs {
		// loc[0] is the "H" of HandleFunc; the paren is just before the path.
		args := callArgs(src, strings.Index(src[loc[0]:], "(")+loc[0])
		path := src[loc[2]:loc[3]]
		if strings.Contains(args, "Middleware") || strings.Contains(args, ".Limit(") {
			continue
		}
		found[path] = true
	}
	return found
}

func TestAnonymousUnlimitedRouteSurfaceIsFrozen(t *testing.T) {
	found := anonymousUnlimited(t)

	var added, stale []string
	for path := range found {
		if _, ok := anonymousUnlimitedRoutes[path]; !ok {
			added = append(added, path)
		}
	}
	for path := range anonymousUnlimitedRoutes {
		if !found[path] {
			stale = append(stale, path)
		}
	}

	if len(added) > 0 {
		sort.Strings(added)
		t.Errorf("new route(s) answering with neither auth nor a rate limit:\n  %s\n"+
			"If the handler does real work - a DB scan, an external fetch, anything that streams -"+
			" wrap it in an IPRateLimiter. If it is genuinely cheap, add it to"+
			" anonymousUnlimitedRoutes with the reason.", strings.Join(added, "\n  "))
	}
	if len(stale) > 0 {
		sort.Strings(stale)
		t.Errorf("anonymousUnlimitedRoutes lists route(s) that are no longer registered"+
			" that way; drop them so the list keeps meaning something:\n  %s", strings.Join(stale, "\n  "))
	}
}

// The specific regression: the one session-less route that pulls a file from
// an external host must stay limited.
func TestBeamDownloadStaysRateLimited(t *testing.T) {
	if anonymousUnlimited(t)["/beam/download"] {
		t.Error("/api/beam/download answers anonymously with no rate limit again: " +
			"each request makes Core fetch the whole Beam binary from its upstream " +
			"and stream it out, so an unbounded caller amplifies through Core")
	}
}
