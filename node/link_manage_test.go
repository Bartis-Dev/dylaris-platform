package main

import "testing"

func TestResolveNodeManagesLink(t *testing.T) {
	const image = "ghcr.io/example/dylaris-gateway-link:latest"

	tests := []struct {
		name      string
		envValue  string
		linkImage string
		want      bool
	}{
		{
			// The case the old default got wrong: an in-cluster gateway node
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
			// The escape hatch: image configured, Link deployed separately.
			name:      "explicit false wins over a set image",
			envValue:  "false",
			linkImage: image,
			want:      false,
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveNodeManagesLink(tt.envValue, tt.linkImage); got != tt.want {
				t.Errorf("resolveNodeManagesLink(%q, image=%t) = %t, want %t",
					tt.envValue, tt.linkImage != "", got, tt.want)
			}
		})
	}
}
