package storageplacement

import (
	"slices"
	"testing"
)

func TestOrderPaths(t *testing.T) {
	tests := []struct {
		name      string
		available []string
		order     []string
		want      []string
	}{
		{
			name:      "no order keeps the node's own order",
			available: []string{"/a", "/b", "/c"},
			want:      []string{"/a", "/b", "/c"},
		},
		{
			name:      "a full order is honoured",
			available: []string{"/a", "/b", "/c"},
			order:     []string{"/c", "/a", "/b"},
			want:      []string{"/c", "/a", "/b"},
		},
		{
			// The reason a disk added later needs no configuration change.
			name:      "unlisted paths come last in their original order",
			available: []string{"/a", "/b", "/c"},
			order:     []string{"/c"},
			want:      []string{"/c", "/a", "/b"},
		},
		{
			name:      "paths the node no longer has are dropped",
			available: []string{"/a", "/b"},
			order:     []string{"/gone", "/b", "/also-gone"},
			want:      []string{"/b", "/a"},
		},
		{
			name:      "duplicates in the order are collapsed",
			available: []string{"/a", "/b", "/c"},
			order:     []string{"/b", "/b", "/a"},
			want:      []string{"/b", "/a", "/c"},
		},
		{
			name:  "no paths at all yields nothing",
			order: []string{"/a"},
			want:  []string{},
		},
		{
			name:      "an order of only unknown paths changes nothing",
			available: []string{"/a", "/b"},
			order:     []string{"/x", "/y"},
			want:      []string{"/a", "/b"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := OrderPaths(tt.available, tt.order)
			if !slices.Equal(got, tt.want) {
				t.Errorf("OrderPaths(%v, %v) = %v, want %v", tt.available, tt.order, got, tt.want)
			}
		})
	}
}

// Losing or duplicating a path would either strand capacity or let two callers
// disagree about which disk a server is on.
func TestOrderPathsIsAPermutation(t *testing.T) {
	available := []string{"/a", "/b", "/c", "/d"}
	got := OrderPaths(available, []string{"/d", "/gone", "/b", "/b"})

	if len(got) != len(available) {
		t.Fatalf("OrderPaths returned %d paths, want %d", len(got), len(available))
	}
	sorted := slices.Clone(got)
	slices.Sort(sorted)
	wantSorted := slices.Clone(available)
	slices.Sort(wantSorted)
	if !slices.Equal(sorted, wantSorted) {
		t.Errorf("OrderPaths returned %v, which is not a permutation of %v", got, available)
	}
}

func TestNormalizeMode(t *testing.T) {
	for _, mode := range []string{"", "auto", "MANUAL", "nonsense", "Manual"} {
		if got := NormalizeMode(mode); got != ModeAuto {
			t.Errorf("NormalizeMode(%q) = %q, want %q", mode, got, ModeAuto)
		}
	}
	if got := NormalizeMode(ModeManual); got != ModeManual {
		t.Errorf("NormalizeMode(%q) = %q, want %q", ModeManual, got, ModeManual)
	}
}
