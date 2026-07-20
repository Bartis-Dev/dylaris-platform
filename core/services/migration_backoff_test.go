package services

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

// TestNextMigrationBackoff pins the retry curve the migration queue consumer
// uses when it cannot reach Redis. The ceiling matters as much as the growth:
// it has to stay well under the 10m per-server migration lock TTL, or an outage
// could leave an interrupted migration's lock expiring before the queue is
// being consumed again.
func TestNextMigrationBackoff(t *testing.T) {
	tests := []struct {
		name    string
		current time.Duration
		want    time.Duration
	}{
		{name: "first failure starts at the initial delay", current: 0, want: migrationRetryInitial},
		{name: "a negative current is treated as no delay yet", current: -1 * time.Second, want: migrationRetryInitial},
		{name: "doubles while below the ceiling", current: 1 * time.Second, want: 2 * time.Second},
		{name: "doubles again", current: 4 * time.Second, want: 8 * time.Second},
		{name: "clamps instead of overshooting the ceiling", current: 20 * time.Second, want: migrationRetryMax},
		{name: "stays at the ceiling once reached", current: migrationRetryMax, want: migrationRetryMax},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := nextMigrationBackoff(tt.current); got != tt.want {
				t.Fatalf("nextMigrationBackoff(%s) = %s, want %s", tt.current, got, tt.want)
			}
		})
	}
}

// TestMigrationBackoffCeilingIsBelowTheMigrationLockTTL states the relationship
// the ceiling was chosen against as an assertion rather than as a comment, so
// raising one without the other fails here instead of in production.
func TestMigrationBackoffCeilingIsBelowTheMigrationLockTTL(t *testing.T) {
	// Reads the package constant deliberately. An earlier version of this test
	// declared its own `const migrationLockTTL = 10 * time.Minute`, which
	// shadowed the real one - so lowering the actual lock TTL, the exact drift
	// the test says it catches, left it green. It could not fail for its stated
	// reason.
	if migrationRetryMax >= migrationLockTTL/2 {
		t.Fatalf("migrationRetryMax = %s, want well under the %s migration lock TTL", migrationRetryMax, migrationLockTTL)
	}
}

// TestIsContextError separates the two reasons the queue consumer returns. It
// always returns an error, so treating every return as a failure would back off
// on a routine leadership handover, and treating none as a failure would
// restore the hot loop this backoff exists to stop.
func TestIsContextError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "shutdown or leadership handover", err: context.Canceled, want: true},
		{name: "deadline", err: context.DeadlineExceeded, want: true},
		{name: "wrapped cancellation still counts", err: fmt.Errorf("queue: %w", context.Canceled), want: true},
		{name: "redis is unreachable", err: errors.New("dial tcp: connect: connection refused"), want: false},
		{name: "consumer group could not be created", err: errors.New("NOGROUP no such key"), want: false},
		{name: "no error at all", err: nil, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isContextError(tt.err); got != tt.want {
				t.Fatalf("isContextError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
