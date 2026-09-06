package main

import "testing"

func TestNormalizeImageRef(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			// The case that was live in production: a container created before
			// the move kept this reference through every restart.
			name: "legacy mc image is rewritten",
			in:   "ghcr.io/bartis-dev/dylaris-platform-mc-java21:latest",
			want: "ghcr.io/dylaris-dev/platform-mc-java21:latest",
		},
		{
			name: "digest pin survives the rewrite",
			in:   "ghcr.io/bartis-dev/dylaris-platform-mc-java8@sha256:abc123",
			want: "ghcr.io/dylaris-dev/platform-mc-java8@sha256:abc123",
		},
		{
			name: "already moved is left alone",
			in:   "ghcr.io/dylaris-dev/platform-mc-java17:latest",
			want: "ghcr.io/dylaris-dev/platform-mc-java17:latest",
		},
		{
			// A customer may run any image they like. Only OUR old prefix moves.
			name: "third-party image is untouched",
			in:   "eclipse-temurin:21-jre",
			want: "eclipse-temurin:21-jre",
		},
		{
			// Same owner, different project: the prefix requires the
			// "dylaris-" segment, so an unrelated package is not captured.
			name: "same owner without the dylaris prefix is untouched",
			in:   "ghcr.io/bartis-dev/flipby:latest",
			want: "ghcr.io/bartis-dev/flipby:latest",
		},
		{
			name: "empty stays empty",
			in:   "",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeImageRef(tt.in); got != tt.want {
				t.Errorf("normalizeImageRef(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
