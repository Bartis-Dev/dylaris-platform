package handlers

import (
	"encoding/json"
	"testing"
)

// The rule these decide: max_routes and max_nodes disagree about what zero
// means, and the store sends them side by side in one payload.
//
//	user_billing (max_nodes, max_links): 0 = UNLIMITED
//	gateway_route_limits (user scope):   0 = DISABLED
//
// parseEntitlement reasons from the first and clears on any non-positive value.
// Applied to the pool that was "delete the row", which falls through
// user_default -> global -> no limit at all. The one number meaning "no
// addresses" produced unlimited addresses.
func TestParseRoutePoolKeepsAMeaningfulZero(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		wantSet bool
		wantVal *int64
	}{
		{"absent leaves the column alone", "", false, nil},
		{"explicit null falls back to the platform default", "null", true, nil},
		{"zero means no addresses, and is written as such", "0", true, i64(0)},
		{"a real allowance is written", "10", true, i64(10)},
		{"a negative is nonsense; none is the safe reading", "-5", true, i64(0)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, set, err := parseRoutePool(json.RawMessage(c.raw))
			if err != nil {
				t.Fatalf("parseRoutePool: %v", err)
			}
			if set != c.wantSet {
				t.Fatalf("set = %v, want %v", set, c.wantSet)
			}
			switch {
			case c.wantVal == nil && got != nil:
				t.Fatalf("value = %d, want nil (clear the override)", *got)
			case c.wantVal != nil && got == nil:
				t.Fatalf("value = nil (the row would be DELETED and the tenant falls through to unlimited), want %d", *c.wantVal)
			case c.wantVal != nil && *got != *c.wantVal:
				t.Fatalf("value = %d, want %d", *got, *c.wantVal)
			}
		})
	}
}

// The sibling must NOT change: in user_billing a zero really does mean
// unlimited, so clearing there is right and is why the two cannot share a
// parser.
func TestParseEntitlementStillClearsOnZero(t *testing.T) {
	got, set, err := parseEntitlement(json.RawMessage("0"))
	if err != nil {
		t.Fatalf("parseEntitlement: %v", err)
	}
	if !set || got != nil {
		t.Errorf("parseEntitlement(0) = (%v, %v), want (nil, true): a 0 node count must clear the override, not be written as an unlimited cap", got, set)
	}
}

func i64(v int64) *int64 { return &v }
