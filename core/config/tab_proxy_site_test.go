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
