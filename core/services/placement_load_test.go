package services

import "testing"

func TestNodeRAMLoad(t *testing.T) {
	// 8 GB allocated of 16 GB physical at 1.0x overcommit = 50%.
	if got := NodeRAMLoad(8192, 16384, 1.0); got != 0.5 {
		t.Errorf("NodeRAMLoad = %v, want 0.5", got)
	}
	// Overcommit doubles effective capacity -> halves load.
	if got := NodeRAMLoad(8192, 16384, 2.0); got != 0.25 {
		t.Errorf("NodeRAMLoad(2x) = %v, want 0.25", got)
	}
	// Unknown physical total -> 0 (heartbeat not yet seen).
	if got := NodeRAMLoad(8192, 0, 1.0); got != 0 {
		t.Errorf("NodeRAMLoad(no total) = %v, want 0", got)
	}
}

func TestNodeCPULoad(t *testing.T) {
	if got := NodeCPULoad(4, 8, 1.0); got != 0.5 {
		t.Errorf("NodeCPULoad = %v, want 0.5", got)
	}
	if got := NodeCPULoad(4, 0, 1.0); got != 0 {
		t.Errorf("NodeCPULoad(no total) = %v, want 0", got)
	}
}

func TestNodeLoadPercent_TakesBindingResource(t *testing.T) {
	// RAM 25%, CPU 75% -> binding is CPU -> 75%.
	got := nodeLoadPercent(4096, 6, 16384, 8, 1.0, 1.0)
	if got != 75 {
		t.Errorf("nodeLoadPercent = %v, want 75", got)
	}
}

func TestNodeHasAllTagsCSV(t *testing.T) {
	cases := []struct {
		have, required string
		want           bool
	}{
		{"eu, ssd, premium", "ssd", true},
		{"eu, ssd", "ssd, premium", false},
		{"eu,ssd", "", true},        // no requirement is satisfied by any node
		{"", "ssd", false},          // no tags can't satisfy a requirement
		{"SSD", "ssd", true},        // case-insensitive
		{"a , b , c", "c , a", true}, // whitespace tolerant, order independent
	}
	for _, c := range cases {
		if got := nodeHasAllTagsCSV(c.have, c.required); got != c.want {
			t.Errorf("nodeHasAllTagsCSV(%q, %q) = %v, want %v", c.have, c.required, got, c.want)
		}
	}
}

func TestEqualRegion(t *testing.T) {
	if !equalRegion("eu-central", "EU-Central") {
		t.Error("equalRegion should be case-insensitive")
	}
	if !equalRegion(" eu ", "eu") {
		t.Error("equalRegion should trim whitespace")
	}
	if equalRegion("eu", "us") {
		t.Error("different regions must not be equal")
	}
}
