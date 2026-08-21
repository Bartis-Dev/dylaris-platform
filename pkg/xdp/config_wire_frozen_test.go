package xdp

import (
	"encoding/json"
	"testing"
)

// TestConfig_WireFormatIsFrozen is the WRITER half of a cross-repo contract.
//
// dylaris:xdp:config is one Redis string with two ends in two repos that ship
// independently: Core (this repo) writes it from the panel's XDP settings, and
// every Edge replica in the gateway repo reads it on startup and again every
// 30s. Each repo compiles its own copy of Config, and this file's own package
// doc says "Keep the struct field-for-field aligned with the gateway-side
// definition". Nothing enforced that.
//
// The pre-existing TestConfig_JSONRoundTrip cannot: it marshals and unmarshals
// with the SAME struct, so renaming a tag on this side keeps it green while the
// payload Core writes stops matching what Edge parses. Its doc comment claims
// it confirms "the wire format the gateway side also depends on" - it confirms
// only that this struct agrees with itself.
//
// Drift is silent and it fails OPEN. Edge's applySharedXDP assigns every field
// from the parsed payload unconditionally, so a tag Edge does not recognise
// leaves the Go zero value and overwrites whatever the env had seeded. Drift on
// "enabled" alone turns DDoS protection OFF on every replica while the panel
// keeps showing it on, on startup and on every reload after.
//
// Golden bytes here, and the reader-side mirror in gateway/pkg/xdp, are what
// actually freeze it. If this test fails the wire format changed: either revert
// it, or change BOTH repos in lockstep - the field is the contract, the struct
// is not.
func TestConfig_WireFormatIsFrozen(t *testing.T) {
	// Every field set to a distinct non-zero value, so a dropped or misnamed
	// one cannot hide behind a coincidental zero.
	c := Config{
		Enabled:                true,
		HostMode:               true,
		Interface:              "wg0",
		ProtectedPorts:         "25565,25566",
		RateLimit:              1000,
		RateWindowMs:           1000,
		BanDurationMin:         30,
		MCMalformedLimit:       20,
		MCMalformedWindowMin:   2,
		MCInvalidHostLimit:     100,
		MCInvalidHostWindowMin: 3,
		MCBanDurationMin:       5,
		Whitelist:              "192.168.1.0/24,10.0.0.1",
	}

	const want = `{"enabled":true,"host_mode":true,"interface":"wg0",` +
		`"protected_ports":"25565,25566","rate_limit":1000,"rate_window_ms":1000,` +
		`"ban_duration_min":30,"mc_malformed_limit":20,"mc_malformed_window_min":2,` +
		`"mc_invalid_host_limit":100,"mc_invalid_host_window_min":3,` +
		`"mc_ban_duration_min":5,"whitelist":"192.168.1.0/24,10.0.0.1"}`

	got, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(got) != want {
		t.Errorf("wire payload changed.\n got: %s\nwant: %s", got, want)
	}
}

// TestConfig_OmitemptyFieldsStayOmitempty pins the two fields that may be
// absent. Edge treats an absent interface as "auto-detect all non-loopback
// IPv4" and an absent whitelist as "no whitelist"; dropping omitempty would
// start writing "" for both, which parses to the same thing today but makes the
// payload's meaning depend on a distinction the readers do not draw. Pinned so
// a future field that DOES distinguish them cannot be added silently.
func TestConfig_OmitemptyFieldsStayOmitempty(t *testing.T) {
	got, err := json.Marshal(Config{})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	const want = `{"enabled":false,"host_mode":false,"protected_ports":"",` +
		`"rate_limit":0,"rate_window_ms":0,"ban_duration_min":0,` +
		`"mc_malformed_limit":0,"mc_malformed_window_min":0,` +
		`"mc_invalid_host_limit":0,"mc_invalid_host_window_min":0,` +
		`"mc_ban_duration_min":0}`
	if string(got) != want {
		t.Errorf("empty-config payload changed.\n got: %s\nwant: %s", got, want)
	}
}
