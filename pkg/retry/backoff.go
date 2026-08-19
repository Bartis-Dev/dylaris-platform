// Package retry holds the platform's standard reconnect schedule.
//
// Every long-lived component that depends on Core or Redis - the node agent,
// the log-shipper inside each Minecraft container - reconnects on the same
// schedule, so an operator reading one log knows what the others are doing.
package retry

import "time"

const (
	// FastDelay/FastAttempts cover the common outage: a service redeployed, an
	// overlay address moved, Valkey restarted. Those resolve in well under a
	// minute, so the first minute of retries is dense.
	FastDelay    = 5 * time.Second
	FastAttempts = 12

	// SlowDelay takes over once the outage has proven to be more than a blip.
	// Still short enough that recovery is prompt, long enough that a thousand
	// nodes hammering a Core that is down do not keep it that way.
	SlowDelay = 30 * time.Second
)

// Backoff yields FastDelay for the first FastAttempts calls to Next, then
// SlowDelay forever. The zero value is ready to use.
//
// Deliberately not exponential: the old 1s-doubling-to-30s schedule spent its
// early attempts on delays too short to matter and reached the ceiling after
// roughly a minute anyway. A flat fast phase is easier to reason about from a
// log timestamp.
//
// Not safe for concurrent use - each connection owns its own Backoff.
type Backoff struct{ attempt int }

// Next reports how long to wait before the next attempt and advances the
// schedule.
func (b *Backoff) Next() time.Duration {
	if b.attempt < FastAttempts {
		b.attempt++
		return FastDelay
	}
	// Saturate rather than keep counting: a component can sit here for weeks.
	b.attempt = FastAttempts
	return SlowDelay
}

// Reset returns the schedule to its fast phase. Call it on every SUCCESSFUL
// connection, not only at startup: a component that flaps then gets the dense
// retries again on each new failure instead of being stuck at SlowDelay
// because of an outage that has long since ended.
func (b *Backoff) Reset() { b.attempt = 0 }
