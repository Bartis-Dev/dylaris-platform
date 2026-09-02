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

// The whole reason Counters and Gauges are maps is that they let a component
// publish something new without either repo touching this contract. That only
// holds if a record carrying them is still VERSION 1 - the moment someone
// "helpfully" bumps for an added field, every consumer built from the other
// repo discards that component entirely until both are deployed.
func TestAddedMapsDoNotChangeTheVersion(t *testing.T) {
	b, err := MarshalGatewayStats(GatewayStats{
		Component: "splice", ID: "e1",
		Counters:  map[string]int64{"players_dropped": 2},
		Gauges:    map[string]float64{"active_sessions": 7},
		UptimeSec: 31,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(b), `"v":1`) {
		t.Fatalf("a record with the new fields is not version 1: %s", b)
	}
	got, ok, err := ParseGatewayStats(b)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v; a version-1 consumer must accept it", ok, err)
	}
	if got.Counters["players_dropped"] != 2 || got.Gauges["active_sessions"] != 7 || got.UptimeSec != 31 {
		t.Fatalf("round trip lost data: %+v", got)
	}
}

// The other half of the same property: a producer that predates the maps sends
// no such keys, and that must decode to empty rather than to an error - which
// is what lets one side deploy before the other.
func TestARecordWithoutTheNewFieldsStillParses(t *testing.T) {
	got, ok, err := ParseGatewayStats([]byte(`{"v":1,"component":"edge","id":"e1","rx_bps":10}`))
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if got.Counters != nil || got.Gauges != nil || got.UptimeSec != 0 {
		t.Fatalf("absent fields did not decode to zero: %+v", got)
	}
}

func TestValidMetricName(t *testing.T) {
	// The rejections are the point. Every accepted name becomes a stored series
	// that lives as long as the record does, so a name carrying a session id, an
	// address or a player name would turn a fixed set of series into one per
	// connection - unbounded storage driven by traffic rather than by code.
	good := []string{"players_dropped", "handovers", "rx_bps", "a", "resume_refused_2"}
	bad := []string{
		"",                      // no name
		"Players",               // upper case: two spellings of one series
		"session-9f2a",          // a dash, and an id folded into the name
		"player.notch",          // a dot, and a player name
		"10.0.0.4_bytes",        // an address
		"_leading",              // trims to nothing meaningful
		"trailing_",             //
		"a b",                   // whitespace
		strings.Repeat("x", 41), // longer than the cap
	}
	for _, n := range good {
		if !ValidMetricName(n) {
			t.Errorf("ValidMetricName(%q) = false, want true", n)
		}
	}
	for _, n := range bad {
		if ValidMetricName(n) {
			t.Errorf("ValidMetricName(%q) = true, want false", n)
		}
	}
}
