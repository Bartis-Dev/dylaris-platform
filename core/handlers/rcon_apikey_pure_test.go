package handlers

import (
	"strings"
	"testing"
	"time"
)

// TestHashAPIKey pins HashAPIKey against independently-known SHA-256 hex
// vectors (computed via crypto/sha256 + encoding/hex directly, not by
// re-invoking HashAPIKey itself). This is the canonical key hash used by
// api-key create + external auth + warp mint - a silent change here (e.g.
// swapping algorithms or adding a pepper) would lock out every existing key.
func TestHashAPIKey(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"empty string", "", "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"},
		{"known vector abc", "abc", "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"},
		{"known vector hello", "hello", "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := HashAPIKey(c.input); got != c.want {
				t.Errorf("HashAPIKey(%q) = %q, want %q", c.input, got, c.want)
			}
		})
	}

	t.Run("deterministic - same input same output", func(t *testing.T) {
		a := HashAPIKey("dyl_sometoken")
		b := HashAPIKey("dyl_sometoken")
		if a != b {
			t.Errorf("HashAPIKey not deterministic: %q != %q", a, b)
		}
	})

	t.Run("different input different output", func(t *testing.T) {
		a := HashAPIKey("dyl_token_a")
		b := HashAPIKey("dyl_token_b")
		if a == b {
			t.Errorf("HashAPIKey collided for different inputs: both %q", a)
		}
	})
}

const hexCharset = "0123456789abcdef"

func isHexString(s string) bool {
	if len(s) == 0 {
		return true
	}
	for _, c := range s {
		if !strings.ContainsRune(hexCharset, c) {
			return false
		}
	}
	return true
}

// TestGenerateRconPassword pins the output shape (hex length == nBytes*2,
// hex-only charset, two calls differ) without asserting exact bytes (random).
func TestGenerateRconPassword(t *testing.T) {
	t.Run("length is nBytes*2 hex chars", func(t *testing.T) {
		for _, n := range []int{1, 4, 24} {
			got, err := generateRconPassword(n)
			if err != nil {
				t.Fatalf("generateRconPassword(%d) error: %v", n, err)
			}
			if len(got) != n*2 {
				t.Errorf("generateRconPassword(%d) len = %d, want %d", n, len(got), n*2)
			}
			if !isHexString(got) {
				t.Errorf("generateRconPassword(%d) = %q, contains non-hex characters", n, got)
			}
		}
	})

	t.Run("two calls produce different values", func(t *testing.T) {
		a, err := generateRconPassword(16)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		b, err := generateRconPassword(16)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if a == b {
			t.Errorf("two independent generateRconPassword(16) calls collided: %q", a)
		}
	})

	t.Run("nBytes==0 returns empty string, no error", func(t *testing.T) {
		got, err := generateRconPassword(0)
		if err != nil {
			t.Fatalf("generateRconPassword(0) error: %v", err)
		}
		if got != "" {
			t.Errorf("generateRconPassword(0) = %q, want empty string", got)
		}
	})

	// SMELL (not fixed - test-only task, this is documentation of real
	// behavior): generateRconPassword does not guard against a negative
	// nBytes. make([]byte, nBytes) with nBytes<0 panics at runtime
	// ("makeslice: len out of range") instead of returning an error. Every
	// current call site passes a hardcoded positive constant (rcon.go:245
	// passes 24), so this is unreachable today, but the function has no
	// defensive check if it's ever reused with a caller-influenced value.
	t.Run("nBytes<0 panics (documents current unguarded behavior)", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Fatalf("expected generateRconPassword(-1) to panic (make with negative len), it did not")
			}
		}()
		_, _ = generateRconPassword(-1)
	})
}

// TestAPIKeyRateLimiterAllow mirrors the IPRateLimiter.allow test style
// (ratelimit_test.go) for the api-key sliding-window limiter: burst up to
// perMin passes, the next request is denied, keys are isolated from each
// other, and a bucket manually pushed into the past resets on next allow().
func TestAPIKeyRateLimiterAllow(t *testing.T) {
	t.Run("burst up to perMin passes then next denied", func(t *testing.T) {
		l := newAPIKeyRateLimiter()
		const perMin = 3
		for i := 1; i <= perMin; i++ {
			if !l.allow(1, perMin) {
				t.Fatalf("request %d expected allowed, got denied", i)
			}
		}
		if l.allow(1, perMin) {
			t.Fatalf("request %d (perMin+1) expected denied, got allowed", perMin+1)
		}
	})

	t.Run("per-key isolation", func(t *testing.T) {
		l := newAPIKeyRateLimiter()
		const perMin = 2
		for i := 1; i <= perMin; i++ {
			if !l.allow(10, perMin) {
				t.Fatalf("key10 request %d expected allowed", i)
			}
		}
		if l.allow(10, perMin) {
			t.Fatalf("key10 request %d expected denied (budget exhausted)", perMin+1)
		}
		for i := 1; i <= perMin; i++ {
			if !l.allow(20, perMin) {
				t.Fatalf("key20 request %d expected allowed, key10's exhaustion leaked", i)
			}
		}
		if l.allow(20, perMin) {
			t.Fatalf("key20 request %d expected denied", perMin+1)
		}
	})

	t.Run("perMin<=0 falls back to default budget of 60", func(t *testing.T) {
		l := newAPIKeyRateLimiter()
		for i := 1; i <= 60; i++ {
			if !l.allow(30, 0) {
				t.Fatalf("request %d expected allowed under default 60/min budget", i)
			}
		}
		if l.allow(30, 0) {
			t.Fatalf("61st request expected denied under default 60/min budget")
		}
		if !l.allow(31, -5) {
			t.Fatalf("first request with negative perMin expected allowed (falls back to default)")
		}
	})

	t.Run("window reset after expiry allows again", func(t *testing.T) {
		// allow() has no injected clock, only time.Now(); rewrite the
		// unexported bucket's reset time into the past instead of sleeping
		// a full minute in a unit test (same technique as ratelimit_test.go).
		l := newAPIKeyRateLimiter()
		const perMin = 1
		if !l.allow(40, perMin) {
			t.Fatalf("first request expected allowed")
		}
		if l.allow(40, perMin) {
			t.Fatalf("second request expected denied within the same window")
		}
		l.mu.Lock()
		l.buckets[40].reset = time.Now().Add(-time.Second)
		l.mu.Unlock()
		if !l.allow(40, perMin) {
			t.Fatalf("request after simulated window elapse expected allowed (bucket should reset)")
		}
		if l.allow(40, perMin) {
			t.Fatalf("second request in the new window expected denied")
		}
	})
}

// TestAPIKeyRateLimiterShouldTouch pins the once-per-minute-per-key gate on
// the last-used DB stamp.
func TestAPIKeyRateLimiterShouldTouch(t *testing.T) {
	l := newAPIKeyRateLimiter()

	if !l.shouldTouch(1) {
		t.Fatalf("first shouldTouch(1) expected true")
	}
	if l.shouldTouch(1) {
		t.Fatalf("second shouldTouch(1) within the same minute expected false")
	}
	// A different key is not gated by key 1's touch.
	if !l.shouldTouch(2) {
		t.Fatalf("shouldTouch(2) expected true (independent from key 1)")
	}

	// Simulate more than a minute having passed for key 1.
	l.mu.Lock()
	l.lastTouch[1] = time.Now().Add(-2 * time.Minute)
	l.mu.Unlock()
	if !l.shouldTouch(1) {
		t.Fatalf("shouldTouch(1) after simulated minute elapse expected true")
	}
}
