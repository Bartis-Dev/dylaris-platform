package main

import (
	"reflect"
	"testing"
)

func TestBeamConnectPlan(t *testing.T) {
	tests := []struct {
		name           string
		hasFingerprint bool
		lanIPs         []string
		relayAddr      string
		publicAddr     string
		want           []string
	}{
		{
			name:           "fingerprint + LAN IPs + relay -> lan then relay",
			hasFingerprint: true,
			lanIPs:         []string{"10.0.0.5"},
			relayAddr:      "beam.example.com:25550",
			publicAddr:     "203.0.113.7:25523",
			want:           []string{"lan", "relay"},
		},
		{
			name:           "fingerprint + LAN IPs + no relay + public -> lan then public",
			hasFingerprint: true,
			lanIPs:         []string{"10.0.0.5", "192.168.1.9"},
			publicAddr:     "203.0.113.7:25523",
			want:           []string{"lan", "public"},
		},
		{
			name:           "fingerprint + no LAN IPs + relay -> relay only",
			hasFingerprint: true,
			relayAddr:      "beam.example.com:25550",
			want:           []string{"relay"},
		},
		{
			name:           "fingerprint + no LAN IPs + no relay + public -> public only",
			hasFingerprint: true,
			publicAddr:     "203.0.113.7:25523",
			want:           []string{"public"},
		},
		{
			name:      "no fingerprint + relay -> relay only, no unpinnable direct",
			relayAddr: "beam.example.com:25550",
			lanIPs:    []string{"10.0.0.5"},
			want:      []string{"relay"},
		},
		{
			name:           "no fingerprint + no relay -> empty",
			hasFingerprint: false,
			lanIPs:         []string{"10.0.0.5"},
			publicAddr:     "203.0.113.7:25523",
			want:           []string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := beamConnectPlan(tt.hasFingerprint, tt.lanIPs, tt.relayAddr, tt.publicAddr)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("beamConnectPlan() = %v, want %v", got, tt.want)
			}
		})
	}
}
