package main

import (
	"strings"
	"testing"
)

// directive returns the single CSP directive whose name matches, trimmed, or ""
// if absent. Used to assert on script-src in isolation (style-src legitimately
// carries 'unsafe-inline').
func directive(csp, name string) string {
	for _, d := range strings.Split(csp, ";") {
		d = strings.TrimSpace(d)
		if d == name || strings.HasPrefix(d, name+" ") {
			return d
		}
	}
	return ""
}

func TestExtractScriptNonce(t *testing.T) {
	cases := []struct {
		name, csp, want string
	}{
		{"present", "script-src 'self' 'nonce-AbC123==' 'strict-dynamic'", "AbC123=="},
		{"absent", "script-src 'self' 'unsafe-inline'", ""},
		{"empty", "", ""},
		{"multi-directive picks the script nonce", "default-src 'self'; script-src 'self' 'nonce-Xy-_9' 'strict-dynamic'; style-src 'self'", "Xy-_9"},
		{"base64url and std chars", "script-src 'nonce-aB+/=_-9'", "aB+/=_-9"},
	}
	for _, c := range cases {
		if got := extractScriptNonce(c.csp); got != c.want {
			t.Errorf("%s: extractScriptNonce(%q) = %q, want %q", c.name, c.csp, got, c.want)
		}
	}
}

func TestBeamConnectExtra(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"https://panel.example.com", " https://panel.example.com"},
		{"https://panel.example.com/", " https://panel.example.com"},
		{"http://192.168.1.5:25510", " http://192.168.1.5:25510"},
		{"  https://p.example.com  ", " https://p.example.com"}, // trimmed
		{"", ""},
		{"not a url", ""},
		{"/relative/path", ""},
	}
	for _, c := range cases {
		if got := beamConnectExtra(c.in); got != c.want {
			t.Errorf("beamConnectExtra(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestBeamNonceCSP(t *testing.T) {
	csp := beamNonceCSP("N0nCe", " https://panel.example.com")
	// Beam's desktop-context directives must survive, and connect-src carries
	// the configured Panel origin - never a hardcoded vendor host.
	for _, want := range []string{
		"'nonce-N0nCe'",
		"'strict-dynamic'",
		"frame-src https: http:",
		"frame-ancestors 'self'",
		"connect-src 'self' https://panel.example.com",
		"img-src 'self' data: blob: https://cravatar.eu https://cdn.modrinth.com",
	} {
		if !strings.Contains(csp, want) {
			t.Errorf("beamNonceCSP missing %q in %q", want, csp)
		}
	}
	if strings.Contains(csp, "dylaris.com") {
		t.Errorf("beamNonceCSP must not hardcode a vendor host: %q", csp)
	}
	// Empty connectExtra -> connect-src is self-only.
	if got := directive(beamNonceCSP("N", ""), "connect-src"); got != "connect-src 'self'" {
		t.Errorf("empty connectExtra: connect-src = %q, want \"connect-src 'self'\"", got)
	}
	// script-src must be strict: nonce + strict-dynamic, never 'unsafe-inline'.
	scriptSrc := directive(csp, "script-src")
	if strings.Contains(scriptSrc, "'unsafe-inline'") {
		t.Errorf("script-src carries unsafe-inline: %q", scriptSrc)
	}
	if !strings.Contains(scriptSrc, "'nonce-N0nCe'") || !strings.Contains(scriptSrc, "'strict-dynamic'") {
		t.Errorf("script-src not strict: %q", scriptSrc)
	}
}

func TestBeamCSPForPanel(t *testing.T) {
	// Nonce present -> nonce CSP + the returned nonce, strict script-src, and the
	// passed-through connect-src origin.
	csp, nonce := beamCSPForPanel("default-src 'self'; script-src 'self' 'nonce-AbC' 'strict-dynamic'", " https://panel.example.com")
	if nonce != "AbC" {
		t.Fatalf("nonce = %q, want AbC", nonce)
	}
	if !strings.Contains(csp, "'nonce-AbC'") || !strings.Contains(csp, "'strict-dynamic'") {
		t.Errorf("nonce CSP not built: %q", csp)
	}
	if directive(csp, "connect-src") != "connect-src 'self' https://panel.example.com" {
		t.Errorf("connect-src not passed through: %q", directive(csp, "connect-src"))
	}
	if strings.Contains(directive(csp, "script-src"), "'unsafe-inline'") {
		t.Errorf("nonce script-src must not carry unsafe-inline: %q", csp)
	}

	// No nonce -> exact fallback, byte-identical to beamPanelCSP with the same
	// connectExtra.
	csp2, nonce2 := beamCSPForPanel("", " https://panel.example.com")
	if nonce2 != "" {
		t.Errorf("fallback nonce = %q, want empty", nonce2)
	}
	if csp2 != beamPanelCSP(" https://panel.example.com") {
		t.Errorf("fallback CSP = %q, want beamPanelCSP(...)", csp2)
	}
}

func TestInjectWailsRuntimeNonce(t *testing.T) {
	html := []byte("<html><head></head><body></body></html>")

	// With a nonce: both /wails tags carry nonce="N".
	out := string(injectWailsRuntime(html, "N0nCe"))
	if strings.Count(out, `nonce="N0nCe"`) != 2 {
		t.Errorf("expected 2 nonced tags, got: %q", out)
	}
	if !strings.Contains(out, `<script nonce="N0nCe" src="/wails/runtime.js"></script>`) {
		t.Errorf("runtime tag not nonced: %q", out)
	}
	if !strings.Contains(out, `<script nonce="N0nCe" src="/wails/ipc.js"></script>`) {
		t.Errorf("ipc tag not nonced: %q", out)
	}

	// Without a nonce: byte-identical to the pre-nonce injection.
	out2 := string(injectWailsRuntime(html, ""))
	if strings.Contains(out2, "nonce=") {
		t.Errorf("fallback tags must not carry a nonce: %q", out2)
	}
	if !strings.Contains(out2, wailsRuntimeInjection) {
		t.Errorf("fallback tags changed from wailsRuntimeInjection: %q", out2)
	}
}
