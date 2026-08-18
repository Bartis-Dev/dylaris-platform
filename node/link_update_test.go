package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// An external node must never be talked out of updating its Link. There is no
// operator on that machine, and a stale Link does not fail loudly - it misbehaves
// subtly against the edges, which only the platform owner can diagnose.
func TestExternalNodeAlwaysForcesAuto(t *testing.T) {
	for _, setting := range []string{linkUpdateNotify, linkUpdateAutoIdle, linkUpdateAuto, "", "garbage"} {
		if got := resolveLinkUpdatePolicy(setting, true); got != linkUpdateAuto {
			t.Errorf("external node with setting %q resolved to %q, want %q", setting, got, linkUpdateAuto)
		}
	}
}

func TestDCNodeHonoursTheSettingAndDefaultsToAutoIdle(t *testing.T) {
	tests := []struct {
		setting string
		want    string
	}{
		{linkUpdateNotify, linkUpdateNotify},
		{linkUpdateAuto, linkUpdateAuto},
		{linkUpdateAutoIdle, linkUpdateAutoIdle},
		{"", linkUpdateAutoIdle},
		{"nonsense", linkUpdateAutoIdle},
	}
	for _, tt := range tests {
		if got := resolveLinkUpdatePolicy(tt.setting, false); got != tt.want {
			t.Errorf("setting %q resolved to %q, want %q", tt.setting, got, tt.want)
		}
	}
}

func TestLinkUpdateDecision(t *testing.T) {
	tests := []struct {
		name          string
		policy        string
		drifted       bool
		sessions      int
		sessionsKnown bool
		wantApply     bool
	}{
		{"no drift never applies", linkUpdateAuto, false, 0, true, false},
		{"notify never applies", linkUpdateNotify, true, 0, true, false},
		{"auto applies with players online", linkUpdateAuto, true, 42, true, true},
		{"auto_idle applies when empty", linkUpdateAutoIdle, true, 0, true, true},
		{"auto_idle waits while players are on", linkUpdateAutoIdle, true, 1, true, false},
		// A Link that will not report its count is not serving anyone reliably.
		// Refusing here would make a wedged Link permanently un-updatable, which
		// is the state an update is most likely to fix.
		{"auto_idle applies when the count is unknown", linkUpdateAutoIdle, true, 0, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			apply, reason := linkUpdateDecision(tt.policy, tt.drifted, tt.sessions, tt.sessionsKnown)
			if apply != tt.wantApply {
				t.Errorf("apply = %v, want %v (reason: %q)", apply, tt.wantApply, reason)
			}
			if tt.drifted && reason == "" {
				t.Error("a drifted image must always produce a reason for the log")
			}
		})
	}
}

func TestLinkImageCheckInterval(t *testing.T) {
	if got := linkImageCheckInterval(0); got != linkImageCheckIntervalDefault {
		t.Errorf("unset interval = %v, want %v", got, linkImageCheckIntervalDefault)
	}
	if got := linkImageCheckInterval(-5); got != linkImageCheckIntervalDefault {
		t.Errorf("negative interval = %v, want the default", got)
	}
	// Below the floor this is registry traffic for nothing.
	if got := linkImageCheckInterval(1); got != time.Minute {
		t.Errorf("1 minute = %v, want 1m", got)
	}
	if got := linkImageCheckInterval(30); got != 30*time.Minute {
		t.Errorf("30 minutes = %v, want 30m", got)
	}
}

func TestLinkSessionCount(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		body      any
		wantCount int
		wantOK    bool
	}{
		{"reports its sessions", 200, map[string]any{"online": true, "sessions": 3}, 3, true},
		{"idle", 200, map[string]any{"online": true, "sessions": 0}, 0, true},
		// The Link reports 503 while Redis is down but can still be carrying
		// players, so the count is read regardless of the status code.
		{"unhealthy but still carrying players", 503, map[string]any{"online": false, "sessions": 7}, 7, true},
		// An older Link has no such field. Unknown, NOT zero - reading a missing
		// field as idle would restart it mid-game.
		{"older link without the field", 200, map[string]any{"online": true}, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				json.NewEncoder(w).Encode(tt.body)
			}))
			defer srv.Close()

			got, ok := linkSessionCount(srv.Listener.Addr().String())
			if ok != tt.wantOK || got != tt.wantCount {
				t.Errorf("got (%d, %v), want (%d, %v)", got, ok, tt.wantCount, tt.wantOK)
			}
		})
	}
}

func TestLinkSessionCountUnreachableIsUnknown(t *testing.T) {
	if _, ok := linkSessionCount(""); ok {
		t.Error("an empty endpoint must be unknown, not zero")
	}
	// A closed port: unknown, so auto_idle applies rather than waiting forever.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	addr := srv.Listener.Addr().String()
	srv.Close()
	if _, ok := linkSessionCount(addr); ok {
		t.Error("an unreachable Link must be unknown, not zero")
	}
}

// Drift needs BOTH sides known. A failed pull leaves the available image empty,
// and treating that as drift would recreate the Link on every tick.
func TestLinkImageStateNeedsBothSides(t *testing.T) {
	tests := []struct {
		running, available string
		want               bool
	}{
		{"sha256:aaa", "sha256:bbb", true},
		{"sha256:aaa", "sha256:aaa", false},
		{"", "sha256:bbb", false},
		{"sha256:aaa", "", false},
		{"", "", false},
	}
	for _, tt := range tests {
		setLinkImageState(tt.running, tt.available)
		if _, _, got := GetLinkImageState(); got != tt.want {
			t.Errorf("running=%q available=%q -> updateAvailable=%v, want %v",
				tt.running, tt.available, got, tt.want)
		}
	}
}
