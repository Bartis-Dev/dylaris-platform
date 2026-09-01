package services

import (
	"testing"
	"time"
)

// What a manual grant is worth, and where that number lives.
//
// The grant used to write its count into max_nodes / max_links - the SAME
// columns the store pushes a purchase into, and the same ones the resolver
// reads as evidence a subscription exists. Two things went wrong with that: a
// granted tenant was indistinguishable from a paying one, and the override
// outlived the grant, so when the deadline passed they kept the product for
// good. Deriving the cap fixes both, because nothing is left behind to clean up.
func TestGrantedCap(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	future := now.Add(24 * time.Hour)
	past := now.Add(-time.Minute)
	n := func(v int64) *int64 { return &v }

	tests := []struct {
		name      string
		purchased *int64
		expires   *time.Time
		want      *int64
	}{
		{"nothing bought and nothing granted has no cap", nil, nil, nil},
		{"a live grant is worth one", nil, &future, n(1)},
		// The one the old shape got wrong: an expired grant must stop raising
		// the ceiling on its own.
		{"an expired grant is worth nothing", nil, &past, nil},
		// "A purchase takes over" in its entirety: the bought number is the
		// answer whether or not a grant is still running underneath it.
		{"a purchase wins over a live grant", n(3), &future, n(3)},
		// Zero is a real answer, not an absent one. The store pushes it on a
		// cancellation, and it must not fall back to the grant's one.
		{"a purchase of none stays none", n(0), &future, n(0)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := grantedCap(tt.purchased, tt.expires, now)
			switch {
			case tt.want == nil && got != nil:
				t.Errorf("got a cap of %d, want none at all", *got)
			case tt.want != nil && got == nil:
				t.Errorf("got no cap, want %d", *tt.want)
			case tt.want != nil && *got != *tt.want:
				t.Errorf("got %d, want %d", *got, *tt.want)
			}
		})
	}
}

// A grant gives exactly one, so a tenant holding one machine may not mint a
// second - and must not be swept for holding the one they were given. The two
// comparisons differ on purpose; this pins that they still line up.
func TestOneGrantedSlotAllowsExactlyOneMachine(t *testing.T) {
	cap := grantedCap(nil, ptrTime(time.Now().Add(time.Hour)), time.Now())
	if AtOrOver(cap, 0) {
		t.Error("a granted tenant could not add their first machine")
	}
	if !AtOrOver(cap, 1) {
		t.Error("a granted tenant could add a second machine")
	}
	if Exceeds(cap, 1) {
		t.Error("a granted tenant with one machine reads as over their limit")
	}
}
