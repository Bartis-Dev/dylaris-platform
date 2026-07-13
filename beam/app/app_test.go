package main

import (
	"encoding/hex"
	"os"
	"testing"
)

func TestNewAppMintsShellToken(t *testing.T) {
	a := NewApp()
	// 32 random bytes hex-encoded = 64 hex chars.
	if len(a.shellToken) != 64 {
		t.Fatalf("shellToken len = %d, want 64", len(a.shellToken))
	}
	if _, err := hex.DecodeString(a.shellToken); err != nil {
		t.Errorf("shellToken is not hex: %v", err)
	}
	// Two independent mints must differ (proves it is random, not fixed).
	if b := NewApp(); a.shellToken == b.shellToken {
		t.Error("two NewApp() calls produced the same shellToken - not random")
	}
}

func TestCheckShellToken(t *testing.T) {
	a := &App{shellToken: "abcdef0123456789"}
	cases := []struct {
		name string
		tok  string
		want bool
	}{
		{"exact match", "abcdef0123456789", true},
		{"mismatch same length", "0000000000000000", false},
		{"empty", "", false},
		{"prefix only", "abcdef", false},
		{"longer", "abcdef0123456789x", false},
	}
	for _, c := range cases {
		if got := a.checkShellToken(c.tok); got != c.want {
			t.Errorf("%s: checkShellToken(%q) = %v, want %v", c.name, c.tok, got, c.want)
		}
	}
}

func TestSavePanelURLTokenGate(t *testing.T) {
	// Redirect the settings path into a temp dir so the happy-path write does
	// not touch the real user config. os.UserConfigDir reads AppData on
	// Windows and XDG_CONFIG_HOME / HOME on Unix, so set all three.
	tmp := t.TempDir()
	t.Setenv("AppData", tmp)         // Windows
	t.Setenv("XDG_CONFIG_HOME", tmp) // Linux
	t.Setenv("HOME", tmp)            // macOS / Linux fallback

	a := &App{shellToken: "good-token"}

	// Bad token: rejected as "unauthorized" BEFORE any disk write.
	if err := a.SavePanelURL("wrong-token", "https://evil.test"); err == nil {
		t.Fatal("SavePanelURL accepted a bad token - must reject")
	}
	if _, err := os.Stat(settingsPath()); err == nil {
		t.Error("SavePanelURL wrote config despite a bad token")
	}

	// Good token: accepted, writes into the temp dir.
	if err := a.SavePanelURL("good-token", "https://panel.example.test"); err != nil {
		t.Fatalf("SavePanelURL rejected a good token: %v", err)
	}
	if got := loadSettings().PanelURL; got != "https://panel.example.test" {
		t.Errorf("saved PanelURL = %q, want https://panel.example.test", got)
	}
}

func TestApplyUpdateTokenGate(t *testing.T) {
	a := &App{shellToken: "good"}
	// Bad token must return before touching updateInFlight (no run started,
	// no network goroutine spawned).
	a.ApplyUpdate("bad")
	if a.updateInFlight.Load() {
		t.Error("ApplyUpdate started a run for a bad token - must reject first")
	}
}

func TestOpenUpdateDownloadTokenGate(t *testing.T) {
	// Smoke gate: a bad token must return immediately (before GetUpdateInfo's
	// network fetch and before BrowserOpenURL). We cannot intercept the native
	// browser-open here, so the deterministic gate coverage lives in
	// TestCheckShellToken; this only asserts the bad-token path is a no-op that
	// does not panic. ctx is nil, so even the token-valid path would no-op.
	a := &App{shellToken: "good"}
	a.OpenUpdateDownload("bad")
}
