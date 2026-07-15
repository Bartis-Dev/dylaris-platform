package services

import (
	"errors"
	"testing"
)

// TestValidateFixedWGIP pins the reserved-address semantics: a fixed IP is accepted
// iff NextFreeIP could have produced it (a host inside the subnet, not network, not
// the leader-reserved first host, not broadcast).
func TestValidateFixedWGIP(t *testing.T) {
	tests := []struct {
		name    string
		ip      string
		subnet  string
		wantErr bool
	}{
		{"valid low host", "10.0.99.2", "10.0.99.0/24", false},
		{"valid high host", "10.0.99.254", "10.0.99.0/24", false},
		{"network address", "10.0.99.0", "10.0.99.0/24", true},
		{"leader reserved first host", "10.0.99.1", "10.0.99.0/24", true},
		{"broadcast", "10.0.99.255", "10.0.99.0/24", true},
		{"outside subnet", "10.0.100.5", "10.0.99.0/24", true},
		{"not an ip", "nonsense", "10.0.99.0/24", true},
		{"ipv6 ip", "fe80::1", "10.0.99.0/24", true},
		{"bad subnet", "10.0.99.2", "not-a-cidr", true},
		{"ipv6 subnet", "10.0.99.2", "fd00::/64", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateFixedWGIP(tt.ip, tt.subnet)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateFixedWGIP(%q, %q) err = %v, wantErr = %v", tt.ip, tt.subnet, err, tt.wantErr)
			}
			if err != nil && !errors.Is(err, ErrInvalidFixedWGIP) {
				t.Errorf("error %v does not wrap ErrInvalidFixedWGIP", err)
			}
		})
	}
}
