package retry

import (
	"testing"
	"time"
)

func TestBackoffSchedule(t *testing.T) {
	var b Backoff
	// 12 fast attempts, then the slow phase forever. Checked past the boundary
	// so an off-by-one in either direction shows up.
	for i := 1; i <= 15; i++ {
		got := b.Next()
		want := FastDelay
		if i > FastAttempts {
			want = SlowDelay
		}
		if got != want {
			t.Errorf("attempt %d: got %s, want %s", i, got, want)
		}
	}
}

func TestBackoffResetRestoresFastPhase(t *testing.T) {
	var b Backoff
	for range 20 {
		b.Next()
	}
	if got := b.Next(); got != SlowDelay {
		t.Fatalf("before reset: got %s, want %s", got, SlowDelay)
	}
	b.Reset()
	// The whole point of Reset: a component that reconnects and fails again
	// gets the dense schedule, not the ceiling it was left at.
	for i := 1; i <= FastAttempts; i++ {
		if got := b.Next(); got != FastDelay {
			t.Fatalf("attempt %d after reset: got %s, want %s", i, got, FastDelay)
		}
	}
	if got := b.Next(); got != SlowDelay {
		t.Errorf("after reset + %d attempts: got %s, want %s", FastAttempts, got, SlowDelay)
	}
}

func TestBackoffDoesNotOverflowOnLongOutage(t *testing.T) {
	// A node can sit unreachable for weeks; the counter must saturate rather
	// than run away.
	var b Backoff
	for range 1_000_000 {
		b.Next()
	}
	if b.attempt != FastAttempts {
		t.Errorf("attempt counter = %d, want it saturated at %d", b.attempt, FastAttempts)
	}
	if got := b.Next(); got != SlowDelay {
		t.Errorf("got %s, want %s", got, SlowDelay)
	}
}

func TestZeroValueIsUsable(t *testing.T) {
	b := &Backoff{}
	if got := b.Next(); got != 5*time.Second {
		t.Errorf("zero value first delay = %s, want 5s", got)
	}
}
