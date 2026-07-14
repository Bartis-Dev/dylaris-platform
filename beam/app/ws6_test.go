package main

import "testing"

// TestIsBrowserOpenableURL pins the WS2/BC3 http-https re-check that
// OpenUpdateDownload applies to the manifest DownloadURL before handing it to
// the native browser-open. This is the one WS2 token-gate invariant NOT already
// covered by the existing tests: the empty/wrong-token rejection lives in
// TestCheckShellToken / TestSavePanelURLTokenGate / TestApplyUpdateTokenGate /
// TestOpenUpdateDownloadTokenGate, and the deliberately un-gated passive getter
// lives in connmode_test.go TestConnectionModeRoundTrip. The protection is
// platform-agnostic, so Windows/WebView2 inherits it unchanged.
func TestIsBrowserOpenableURL(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want bool
	}{
		{"http allowed", "http://example.test/x", true},
		{"https allowed", "https://github.com/Bartis-Dev/dylaris-platform/releases/latest/download/DylarisBeam-windows-amd64", true},
		{"file scheme rejected", "file:///C:/Windows/System32/calc.exe", false},
		{"unc path rejected", `\\attacker\share\payload.exe`, false},
		{"javascript scheme rejected", "javascript:alert(1)", false},
		{"mailto scheme rejected", "mailto:a@b.test", false},
		{"scheme-relative rejected", "//evil.test/x", false},
		{"empty rejected", "", false},
		{"ftp scheme rejected", "ftp://host/x", false},
	}
	for _, c := range cases {
		if got := isBrowserOpenableURL(c.raw); got != c.want {
			t.Errorf("%s: isBrowserOpenableURL(%q) = %v, want %v", c.name, c.raw, got, c.want)
		}
	}
}
