package handlers

import (
	"fmt"
	"testing"
	"time"
)

func TestIPRateLimiterAllow(t *testing.T) {
	t.Run("within limit allowed then N+1th denied", func(t *testing.T) {
		l := NewIPRateLimiter()
		const perMin = 3
		for i := 1; i <= perMin; i++ {
			if !l.allow("1.2.3.4", perMin) {
				t.Fatalf("request %d expected allowed, got denied", i)
			}
		}
		if l.allow("1.2.3.4", perMin) {
			t.Fatalf("request %d (N+1th) expected denied, got allowed", perMin+1)
		}
		// Still denied on a subsequent attempt within the same window.
		if l.allow("1.2.3.4", perMin) {
			t.Fatalf("request %d expected denied, got allowed", perMin+2)
		}
	})

	t.Run("per-IP isolation", func(t *testing.T) {
		l := NewIPRateLimiter()
		const perMin = 2
		for i := 1; i <= perMin; i++ {
			if !l.allow("10.0.0.1", perMin) {
				t.Fatalf("ip1 request %d expected allowed", i)
			}
		}
		if l.allow("10.0.0.1", perMin) {
			t.Fatalf("ip1 request %d expected denied (budget exhausted)", perMin+1)
		}
		// A second IP must have its own independent budget.
		for i := 1; i <= perMin; i++ {
			if !l.allow("10.0.0.2", perMin) {
				t.Fatalf("ip2 request %d expected allowed, ip1's exhaustion leaked", i)
			}
		}
		if l.allow("10.0.0.2", perMin) {
			t.Fatalf("ip2 request %d expected denied", perMin+1)
		}
	})

	t.Run("perMin<=0 falls back to default budget of 60", func(t *testing.T) {
		l := NewIPRateLimiter()
		for i := 1; i <= 60; i++ {
			if !l.allow("5.5.5.5", 0) {
				t.Fatalf("request %d expected allowed under default 60/min budget", i)
			}
		}
		if l.allow("5.5.5.5", 0) {
			t.Fatalf("61st request expected denied under default 60/min budget")
		}
		// Negative values are treated the same as zero.
		if !l.allow("6.6.6.6", -5) {
			t.Fatalf("first request with negative perMin expected allowed (falls back to default)")
		}
	})

	t.Run("window reset after expiry allows again", func(t *testing.T) {
		// allow() has no injected clock, only time.Now(); sleeping a full
		// minute in a unit test is impractical, so this rewrites the
		// unexported bucket's reset time into the past (equivalent to what
		// real elapsed time would produce) instead of adding a clock seam to
		// production code.
		l := NewIPRateLimiter()
		const perMin = 1
		if !l.allow("7.7.7.7", perMin) {
			t.Fatalf("first request expected allowed")
		}
		if l.allow("7.7.7.7", perMin) {
			t.Fatalf("second request expected denied within the same window")
		}
		l.mu.Lock()
		l.buckets["7.7.7.7"].reset = time.Now().Add(-time.Second)
		l.mu.Unlock()
		if !l.allow("7.7.7.7", perMin) {
			t.Fatalf("request after simulated window elapse expected allowed (bucket should reset)")
		}
		// The freshly-reset bucket should itself enforce the limit again.
		if l.allow("7.7.7.7", perMin) {
			t.Fatalf("second request in the new window expected denied")
		}
	})

	t.Run("bucket map eviction sweeps expired entries once size exceeds 10000", func(t *testing.T) {
		l := NewIPRateLimiter()
		l.mu.Lock()
		for i := 0; i < 10001; i++ {
			l.buckets[fmt.Sprintf("ip-%d", i)] = &rateBucket{count: 1, reset: time.Now().Add(-time.Minute)}
		}
		l.mu.Unlock()

		if !l.allow("new-ip", 5) {
			t.Fatalf("allow on a fresh IP expected true")
		}

		l.mu.Lock()
		n := len(l.buckets)
		l.mu.Unlock()
		// Every pre-seeded bucket was already expired, so the opportunistic
		// sweep should have deleted all of them, leaving only the bucket
		// this allow() call just created.
		if n != 1 {
			t.Errorf("expected 1 bucket left after the eviction sweep, got %d", n)
		}
	})
}
