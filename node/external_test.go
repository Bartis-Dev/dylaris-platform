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

// The BYON deploy snippet sets NODE_EXTERNAL and no NODE_TAGS, so before this
// every external node reported empty tags and Core's Node.IsExternal() answered
// false for all of them - which also silently disabled the "requires gateway"
// badge, guarded by that same method.
func TestTagsWithExternal(t *testing.T) {
	cases := []struct {
		name     string
		tags     string
		external bool
		want     string
	}{
		{name: "the BYON case: no tags at all", tags: "", external: true, want: "external"},
		{name: "appended to existing tags", tags: "eu,ssd", external: true, want: "eu,ssd,external"},
		{name: "not duplicated", tags: "eu,external", external: true, want: "eu,external"},
		{name: "not duplicated with spacing", tags: "eu, external ", external: true, want: "eu, external "},
		{name: "a swarm node keeps its tags", tags: "eu,ssd", external: false, want: "eu,ssd"},
		{name: "a swarm node with no tags stays untagged", tags: "", external: false, want: ""},
		{name: "whitespace-only tags are treated as empty", tags: "  ", external: true, want: "external"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := tagsWithExternal(c.tags, c.external); got != c.want {
				t.Errorf("tagsWithExternal(%q, %v) = %q, want %q", c.tags, c.external, got, c.want)
			}
		})
	}
}
