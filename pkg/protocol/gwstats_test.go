package protocol

import (
	"errors"
	"strings"
	"testing"
)

func TestMarshalGatewayStatsStampsVersion(t *testing.T) {
	b, err := MarshalGatewayStats(GatewayStats{Component: "warp", ID: "eu-1"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(b)
	// Frozen wire shape: the version tag and the core fields must be present.
	for _, want := range []string{`"v":1`, `"component":"warp"`, `"id":"eu-1"`, `"rx_bps":0`, `"tx_bps":0`} {
		if !strings.Contains(s, want) {
			t.Errorf("payload %s missing %q", s, want)
		}
	}
}

func TestParseGatewayStatsVersionHandling(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		wantOK  bool
	}{
		{"current version", `{"v":1,"component":"edge","id":"e1","rx_bps":10}`, true},
		{"future version ignored", `{"v":2,"component":"edge","id":"e1"}`, false},
		{"zero version ignored", `{"component":"edge","id":"e1"}`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, ok, err := ParseGatewayStats([]byte(tt.payload))
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if ok != tt.wantOK {
				t.Errorf("ok = %v, want %v", ok, tt.wantOK)
			}
			if tt.wantOK && s.Component != "edge" {
				t.Errorf("component = %q, want edge", s.Component)
			}
		})
	}
}

func TestParseGatewayStatsBadJSON(t *testing.T) {
	if _, _, err := ParseGatewayStats([]byte("{not json")); err == nil {
		t.Fatal("expected a JSON error")
	}
}

func TestResolveHostPrefersEnv(t *testing.T) {
	get := func(k string) string {
		if k == "NODE_HOSTNAME" {
			return "web-eu-1"
		}
		return ""
	}
	if got := ResolveHost(get, func() (string, error) { return "container-abc", nil }); got != "web-eu-1" {
		t.Errorf("got %q, want web-eu-1", got)
	}
	// Empty env falls back to the OS hostname.
	empty := func(string) string { return "" }
	if got := ResolveHost(empty, func() (string, error) { return "container-abc", nil }); got != "container-abc" {
		t.Errorf("fallback got %q, want container-abc", got)
	}
	// OS hostname error yields "".
	if got := ResolveHost(empty, func() (string, error) { return "", errors.New("nope") }); got != "" {
		t.Errorf("error case got %q, want empty", got)
	}
}
