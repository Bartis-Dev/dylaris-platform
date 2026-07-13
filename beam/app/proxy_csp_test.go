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

func TestBeamNonceCSP(t *testing.T) {
	csp := beamNonceCSP("N0nCe")
	// Beam's desktop-context directives must survive.
	for _, want := range []string{
		"'nonce-N0nCe'",
		"'strict-dynamic'",
		"frame-src https: http:",
		"frame-ancestors 'self'",
		"connect-src 'self' https://api.dylaris.com",
		"img-src 'self' data: blob: https://cravatar.eu https://cdn.modrinth.com",
	} {
		if !strings.Contains(csp, want) {
			t.Errorf("beamNonceCSP missing %q in %q", want, csp)
		}
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
	// Nonce present -> nonce CSP + the returned nonce, strict script-src.
	csp, nonce := beamCSPForPanel("default-src 'self'; script-src 'self' 'nonce-AbC' 'strict-dynamic'")
	if nonce != "AbC" {
		t.Fatalf("nonce = %q, want AbC", nonce)
	}
	if !strings.Contains(csp, "'nonce-AbC'") || !strings.Contains(csp, "'strict-dynamic'") {
		t.Errorf("nonce CSP not built: %q", csp)
	}
	if strings.Contains(directive(csp, "script-src"), "'unsafe-inline'") {
		t.Errorf("nonce script-src must not carry unsafe-inline: %q", csp)
	}

	// No nonce -> exact fallback, byte-identical to today's beamPanelCSP.
	csp2, nonce2 := beamCSPForPanel("")
	if nonce2 != "" {
		t.Errorf("fallback nonce = %q, want empty", nonce2)
	}
	if csp2 != beamPanelCSP {
		t.Errorf("fallback CSP = %q, want beamPanelCSP", csp2)
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
