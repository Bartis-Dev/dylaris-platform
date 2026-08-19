package main

import "testing"

func TestResolveNodeManagesLink(t *testing.T) {
	const image = "ghcr.io/example/dylaris-gateway-link:latest"

	tests := []struct {
		name      string
		envValue  string
		linkImage string
		external  bool
		want      bool
	}{
		{
			// The case an older default got wrong: an in-cluster gateway node
			// configured with an image came up with no Link and no error.
			name:      "image set and flag unset manages the link",
			linkImage: image,
			want:      true,
		},
		{
			name: "nothing configured manages nothing",
			want: false,
		},
		{
			// Nobody else deploys a Link onto a customer's machine, so a BYON
			// node needs neither env line to get one. This is what lets both
			// drop out of the deploy snippet.
			name:     "an external node manages the link with nothing configured",
			external: true,
			want:     true,
		},
		{
			// The escape hatch: image configured, Link deployed separately.
			name:      "explicit false wins over a set image",
			envValue:  "false",
			linkImage: image,
			want:      false,
		},
		{
			// The same hatch has to work for an external node, or an operator
			// running their own Link on a BYON box has no way to say so.
			name:     "explicit false wins over external",
			envValue: "false",
			external: true,
			want:     false,
		},
		{
			name:     "explicit true wins over an empty image",
			envValue: "true",
			want:     true,
		},
		{
			// Anything that is not exactly "true" is off, matching how the flag
			// was always parsed.
			name:      "a non-boolean value is not true",
			envValue:  "yes",
			linkImage: image,
			want:      false,
		},
		{
			// The image env is inspected as a raw string, so whitespace is not
			// an opt-in - otherwise a stray blank would enrol the whole fleet.
			name:      "a whitespace-only image is not configured",
			linkImage: "   ",
			want:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveNodeManagesLink(tt.envValue, tt.linkImage, tt.external); got != tt.want {
				t.Errorf("resolveNodeManagesLink(%q, image=%t, external=%t) = %t, want %t",
					tt.envValue, tt.linkImage != "", tt.external, got, tt.want)
			}
		})
	}
}

// The image default exists so the BYON snippet does not have to carry a registry
// path. It must never be what decides that a node manages a Link - that is the
// raw env value's job, and confusing the two would start a sidecar on every node
// in the cluster.
func TestResolveLinkImage(t *testing.T) {
	if got := resolveLinkImage(""); got != defaultLinkImage {
		t.Errorf("unset = %q, want the default %q", got, defaultLinkImage)
	}
	if got := resolveLinkImage("  "); got != defaultLinkImage {
		t.Errorf("whitespace = %q, want the default %q", got, defaultLinkImage)
	}
	const custom = "registry.example/link:v2"
	if got := resolveLinkImage(custom); got != custom {
		t.Errorf("explicit = %q, want %q", got, custom)
	}
	// The pairing that matters: an in-cluster node with no image env must not
	// start managing a Link just because a default now exists.
	if resolveNodeManagesLink("", "", false) {
		t.Error("the built-in image default must not opt an in-cluster node into managing a Link")
	}
}
