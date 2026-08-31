package config

import (
	"bytes"
	"log"
	"strings"
	"testing"
)

// The tab-proxy design rests on one browser rule that nothing in the code could
// otherwise enforce: the ticket cookie is set on the tab's host and read back
// from it, and the browser only allows that same-SITE. A suffix on another
// registrable domain leaves every proxied tab failing to authorize with no
// diagnostic at all - which is exactly the shape of failure a boot warning
// exists for.
func TestWarnTabProxySuffixNotSameSite(t *testing.T) {
	cases := []struct {
		name        string
		frontendURL string
		suffix      string
		wantWarn    bool
	}{
		{"same site", "https://panel.dylaris.com", "share.dylaris.com", false},
		{"same site, deeper suffix", "https://panel.dylaris.com", "a.b.dylaris.com", false},
		{"same site, apex panel", "https://dylaris.com", "share.dylaris.com", false},
		{"different registrable domain", "https://panel.example.com", "tabs.example.net", true},
		{"a lookalike is not the same site", "https://panel.example.com", "tabs.example.com.evil.test", true},
		{"public suffix handled: same", "https://panel.example.co.uk", "tabs.example.co.uk", false},
		{"public suffix handled: different", "https://panel.example.co.uk", "tabs.other.co.uk", true},
		// Browsers have their own rules for these and a developer meets them
		// daily; a warning on every dev boot is a warning nobody reads.
		{"localhost is not warned about", "http://localhost:25510", "tabs.localhost", false},
		{"bare IP is not warned about", "http://10.0.0.5:25510", "tabs.internal", false},
		{"unset suffix says nothing", "https://panel.example.com", "", false},
		{"unparseable frontend says nothing", "://bad", "tabs.example.com", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var buf bytes.Buffer
			old := log.Writer()
			log.SetOutput(&buf)
			defer log.SetOutput(old)

			warnTabProxySuffixNotSameSite(c.frontendURL, c.suffix)

			warned := strings.Contains(buf.String(), "TAB_PROXY_HOST_SUFFIX")
			if warned != c.wantWarn {
				t.Errorf("warned = %v, want %v (log=%q)", warned, c.wantWarn, buf.String())
			}
			if c.wantWarn && !strings.Contains(buf.String(), "fail to authorize") {
				t.Errorf("the warning does not name the consequence: %q", buf.String())
			}
		})
	}
}

// The split-hostname layout is documented as still supported, and for a
// non-browser client it is. For a BROWSER it stopped working the moment the
// session became a cookie: the cookie is host-only to whichever host issued it,
// and a cross-origin fetch neither sends nor stores one here.
//
// The failure looks exactly like a broken server - the panel loads, the login
// form submits, everything after is 401 - so the only thing standing between an
// operator and an afternoon is this line at boot. The test is on the predicate
// rather than the wording: it must fire on a real split and stay quiet on every
// shape that is not one.
func TestPanelAPIURLSplitDetection(t *testing.T) {
	cases := []struct {
		name, frontend, api string
		split               bool
	}{
		{"unset - the normal deployment", "https://panel.example.com", "", false},
		{"same origin, spelled with the path", "https://panel.example.com", "https://panel.example.com/api", false},
		{"same origin, different case", "https://panel.example.com", "https://Panel.Example.com/api", false},
		{"a second hostname", "https://panel.example.com", "https://api.example.com/api", true},
		// A scheme split is a split: http://panel and https://panel are
		// different origins to a cookie as much as to CORS.
		{"same host, other scheme", "https://panel.example.com", "http://panel.example.com/api", true},
		{"a different port", "http://localhost:25510", "http://localhost:25500/api", true},
		{"unparseable - say nothing rather than guess", "https://panel.example.com", "not a url", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := panelAPIURLIsAnotherOrigin(tc.frontend, tc.api); got != tc.split {
				t.Errorf("split = %v, want %v", got, tc.split)
			}
		})
	}
}
