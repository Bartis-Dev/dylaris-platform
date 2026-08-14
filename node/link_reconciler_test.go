package main

import "testing"

// TestLinkWanted pins the routing-mode gate for the node's Link sidecar. The
// "both with creds -> true" case is the regression guard: the Link used to be
// gated on "gateway" only, so a domain route created while routing was "both"
// was silently dead (no Link, so the route was never published).
func TestLinkWanted(t *testing.T) {
	const (
		sec = "secret"
		prf = "proof"
		img = "ghcr.io/x/link:latest"
	)
	tests := []struct {
		name                       string
		mode, secret, proof, image string
		want                       bool
	}{
		{"gateway with creds", "gateway", sec, prf, img, true},
		{"both with creds", "both", sec, prf, img, true},
		{"ip_port with creds", "ip_port", sec, prf, img, false},
		{"gateway missing secret", "gateway", "", prf, img, false},
		{"both missing proof", "both", sec, "", img, false},
		{"gateway missing image", "gateway", sec, prf, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := linkWanted(tt.mode, tt.secret, tt.proof, tt.image); got != tt.want {
				t.Errorf("linkWanted(%q, secret=%q, proof=%q, image=%q) = %v, want %v",
					tt.mode, tt.secret, tt.proof, tt.image, got, tt.want)
			}
		})
	}
}
