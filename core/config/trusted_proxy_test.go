package config

import (
	"net"
	"testing"
)

// contains reports whether any parsed net contains ip. Used to assert the
// parser produced networks that actually match, rather than checking CIDR
// strings, which would just restate the input.
func contains(nets []*net.IPNet, ipStr string) bool {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}
	for _, n := range nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

func TestParseTrustedProxyCIDRs(t *testing.T) {
	tests := []struct {
		name       string
		raw        string
		mustMatch  []string
		mustReject []string
		wantEmpty  bool
	}{
		{
			name:      "unset defaults to the private ranges",
			raw:       "",
			mustMatch: []string{"10.1.2.3", "172.16.0.1", "192.168.1.1", "127.0.0.1", "::1", "fc00::1"},
			// A public address must NOT be trusted by default, or the spoof this
			// whole feature closes would still be open.
			mustReject: []string{"8.8.8.8", "1.2.3.4"},
		},
		{
			name:      "whitespace-only is still unset",
			raw:       "   ",
			mustMatch: []string{"10.0.0.1"},
		},
		{
			name:      "none trusts nothing",
			raw:       "none",
			wantEmpty: true,
		},
		{
			name:      "off trusts nothing",
			raw:       "off",
			wantEmpty: true,
		},
		{
			name:       "explicit CIDRs replace the default",
			raw:        "203.0.113.0/24",
			mustMatch:  []string{"203.0.113.7"},
			mustReject: []string{"10.0.0.1"}, // private is NOT trusted once overridden
		},
		{
			name:       "a bare IP is a single host",
			raw:        "203.0.113.9",
			mustMatch:  []string{"203.0.113.9"},
			mustReject: []string{"203.0.113.10"},
		},
		{
			name:       "a typo is skipped and the rest survive",
			raw:        "not-a-cidr, 203.0.113.0/24",
			mustMatch:  []string{"203.0.113.1"},
			mustReject: []string{"8.8.8.8"},
		},
		{
			name:      "an all-garbage list yields an empty set rather than a crash",
			raw:       "nonsense, also-bad",
			wantEmpty: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseTrustedProxyCIDRs(tt.raw)
			if tt.wantEmpty {
				if len(got) != 0 {
					t.Fatalf("parsed %d networks, want none", len(got))
				}
				return
			}
			for _, ip := range tt.mustMatch {
				if !contains(got, ip) {
					t.Errorf("%q should be trusted but was not", ip)
				}
			}
			for _, ip := range tt.mustReject {
				if contains(got, ip) {
					t.Errorf("%q should NOT be trusted but was", ip)
				}
			}
		})
	}
}
