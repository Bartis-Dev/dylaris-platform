package main

import (
	"os"
	"strconv"
	"testing"
)

const testBoolKey = "DYLARIS_TEST_BOOL"

// GRPC_TLS_ENABLED is the one flag that is worthless unless Core and every node
// read the SAME truth out of the same string: Core serving TLS while a node
// dials plaintext is not a degraded channel, it is a refused connection and a
// dead management plane. Core parses it with strconv.ParseBool; the node used a
// bare == "true", so GRPC_TLS_ENABLED=1 turned TLS on at Core only - with both
// config files looking correct to whoever wrote them.
//
// This pins the parity against strconv.ParseBool itself rather than against a
// hand-copied list, so it keeps holding if Core's accepted set ever widens.
func TestParseBoolEnvDefaultMatchesCoreParsing(t *testing.T) {
	for _, raw := range []string{
		"true", "TRUE", "True", "t", "T", "1",
		"false", "FALSE", "False", "f", "F", "0",
	} {
		t.Run("value="+raw, func(t *testing.T) {
			want, err := strconv.ParseBool(raw)
			if err != nil {
				t.Fatalf("test bug: %q is not a ParseBool input", raw)
			}
			t.Setenv(testBoolKey, raw)
			// The default must not influence a value that parses, in either
			// direction - otherwise "false" could not switch a default-on flag off.
			for _, def := range []bool{true, false} {
				if got := parseBoolEnvDefault(testBoolKey, def); got != want {
					t.Fatalf("parseBoolEnvDefault(%q, def=%v) = %v, want %v", raw, def, got, want)
				}
			}
		})
	}
}

// An UNPARSEABLE value keeps the default rather than collapsing to false. That
// distinction only matters once a flag defaults ON, which GRPC_TLS_ENABLED now
// does: `v, _ := strconv.ParseBool(...)` would read GRPC_TLS_ENABLED=yes as
// false and silently drop transport security while the operator's file says
// they enabled it. Wrong in the safe direction, and invisible.
func TestParseBoolEnvDefaultKeepsDefaultOnGarbage(t *testing.T) {
	for _, raw := range []string{"yes", "no", "on", "off", "2", "tru", "!", " "} {
		t.Run("value="+raw, func(t *testing.T) {
			t.Setenv(testBoolKey, raw)
			if !parseBoolEnvDefault(testBoolKey, true) {
				t.Fatalf("parseBoolEnvDefault(%q, def=true) = false; a typo must never disable TLS", raw)
			}
			if parseBoolEnvDefault(testBoolKey, false) {
				t.Fatalf("parseBoolEnvDefault(%q, def=false) = true; garbage must not enable anything either", raw)
			}
		})
	}
}

// Surrounding whitespace is how a copy-pasted value diverges: a hand-edited
// compose hands over exactly what was typed.
func TestParseBoolEnvDefaultTrimsWhitespace(t *testing.T) {
	t.Setenv(testBoolKey, " false ")
	if parseBoolEnvDefault(testBoolKey, true) {
		t.Fatal(`parseBoolEnvDefault(" false ", def=true) = true; a stray space must not ignore an explicit opt-out`)
	}
}

// Unset and empty both mean "the operator said nothing", which is the default.
// Empty matters on its own: compose expands an absent .env key to an empty
// string, so `GRPC_TLS_ENABLED: "${GRPC_TLS_ENABLED}"` reaches the process set
// but blank, and treating that as false would silently opt every such
// deployment out.
func TestParseBoolEnvDefaultUnsetAndEmptyKeepDefault(t *testing.T) {
	os.Unsetenv(testBoolKey)
	if !parseBoolEnvDefault(testBoolKey, true) {
		t.Fatal("unset must keep def=true")
	}
	if parseBoolEnvDefault(testBoolKey, false) {
		t.Fatal("unset must keep def=false")
	}

	t.Setenv(testBoolKey, "")
	if !parseBoolEnvDefault(testBoolKey, true) {
		t.Fatal("empty must keep def=true, not read as false")
	}
}
