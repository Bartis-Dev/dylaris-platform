package main

import "testing"

func TestHasTag(t *testing.T) {
	cases := []struct {
		tags, want string
		expect     bool
	}{
		{"external", "external", true},
		{"eu,external,fast", "external", true},
		{" external ", "external", true},
		{"eu,fast", "external", false},
		{"", "external", false},
		{"externalish", "external", false},
	}
	for _, c := range cases {
		if got := hasTag(c.tags, c.want); got != c.expect {
			t.Fatalf("hasTag(%q,%q)=%v want %v", c.tags, c.want, got, c.expect)
		}
	}
}

func TestApplyExternalOverride(t *testing.T) {
	rm, fm := applyExternalOverride("ip_port", "sftp", true)
	if rm != "gateway" || fm != "beam" {
		t.Fatalf("external override = %q/%q, want gateway/beam", rm, fm)
	}
	rm, fm = applyExternalOverride("both", "sftp", false)
	if rm != "both" || fm != "sftp" {
		t.Fatalf("non-external changed modes: %q/%q", rm, fm)
	}
}
