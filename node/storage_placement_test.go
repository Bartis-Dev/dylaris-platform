package main

import (
	"strings"
	"testing"
)

// newPlacementManager builds a StorageManager over n real temp dirs so free-space
// probing works, and returns it with the paths in creation order.
func newPlacementManager(t *testing.T, n int) (*StorageManager, []string) {
	t.Helper()
	roots := make([]string, n)
	for i := range roots {
		roots[i] = t.TempDir()
	}
	sm := NewStorageManager(strings.Join(roots, ","), nil)
	if got := len(sm.Paths()); got != n {
		t.Fatalf("storage manager took %d paths, want %d", got, n)
	}
	return sm, roots
}

func TestOrderedPaths(t *testing.T) {
	sm, roots := newPlacementManager(t, 3)

	tests := []struct {
		name  string
		order []int // indexes into roots
		want  []int
	}{
		{
			name:  "no order keeps the configured order",
			order: nil,
			want:  []int{0, 1, 2},
		},
		{
			name:  "a full order is honoured",
			order: []int{2, 0, 1},
			want:  []int{2, 0, 1},
		},
		{
			// The case that matters when a disk is added later: it must land at
			// the bottom without the admin editing anything.
			name:  "unlisted paths come last in configured order",
			order: []int{2},
			want:  []int{2, 0, 1},
		},
		{
			name:  "duplicates in the order are collapsed",
			order: []int{1, 1, 0},
			want:  []int{1, 0, 2},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			order := make([]string, len(tt.order))
			for i, idx := range tt.order {
				order[i] = roots[idx]
			}
			sm.SetPlacement(placementManual, order)

			got := sm.orderedPaths()
			if len(got) != len(tt.want) {
				t.Fatalf("orderedPaths() returned %d paths, want %d", len(got), len(tt.want))
			}
			for i, idx := range tt.want {
				if got[i] != roots[idx] {
					t.Errorf("orderedPaths()[%d] = %q, want roots[%d] = %q", i, got[i], idx, roots[idx])
				}
			}
		})
	}
}

// A path the node no longer has must simply drop out of the order rather than
// being handed to a server.
func TestOrderedPathsDropsUnknownEntries(t *testing.T) {
	sm, roots := newPlacementManager(t, 2)
	sm.SetPlacement(placementManual, []string{"/gone", roots[1], "/also-gone"})

	got := sm.orderedPaths()
	want := []string{roots[1], roots[0]}
	if len(got) != len(want) {
		t.Fatalf("orderedPaths() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("orderedPaths()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestSetPlacementNormalisesMode(t *testing.T) {
	sm, roots := newPlacementManager(t, 2)

	for _, mode := range []string{"", "auto", "nonsense", "MANUAL"} {
		t.Run("mode="+mode, func(t *testing.T) {
			sm.SetPlacement(mode, []string{roots[1]})
			// Anything that is not exactly "manual" must behave as auto, or a
			// typo in the stored config would silently change placement.
			if sm.placementMode != placementAuto {
				t.Errorf("placementMode = %q, want %q", sm.placementMode, placementAuto)
			}
		})
	}

	sm.SetPlacement(placementManual, []string{roots[1]})
	if sm.placementMode != placementManual {
		t.Errorf("placementMode = %q, want %q", sm.placementMode, placementManual)
	}
}

// SetPlacement must not alias the caller's slice, or a later mutation of the
// decoded config would silently reorder placement.
func TestSetPlacementCopiesOrder(t *testing.T) {
	sm, roots := newPlacementManager(t, 2)

	order := []string{roots[1], roots[0]}
	sm.SetPlacement(placementManual, order)
	order[0] = "/mutated"

	if got := sm.orderedPaths(); got[0] != roots[1] {
		t.Errorf("orderedPaths()[0] = %q, want %q - SetPlacement kept a reference to the caller's slice", got[0], roots[1])
	}
}

// Manual placement selects the first path in the order; auto ignores the order
// entirely.
func TestSelectStoragePathHonoursMode(t *testing.T) {
	sm, roots := newPlacementManager(t, 2)
	sm.SetPlacement(placementManual, []string{roots[1], roots[0]})

	got, err := sm.SelectStoragePath("server-uuid", "")
	if err != nil {
		t.Fatalf("SelectStoragePath returned error: %v", err)
	}
	if got != roots[1] {
		t.Errorf("manual placement chose %q, want the first path in the order %q", got, roots[1])
	}

	// An explicit request still overrides the order - that is how the create
	// wizard pins a server to a disk.
	got, err = sm.SelectStoragePath("other-uuid", roots[0])
	if err != nil {
		t.Fatalf("SelectStoragePath returned error: %v", err)
	}
	if got != roots[0] {
		t.Errorf("explicit path request chose %q, want %q", got, roots[0])
	}
}
