package handlers

import (
	"net/http"
	"sync"
	"time"
)

// IPRateLimiter is a per-IP sliding-window counter for public, unauthenticated
// endpoints (login, registration, password reset, first-admin setup) and the
// pre-validation gate on external API keys. In-memory and per-Core: enough to
// blunt brute-force and credential-stuffing. A distributed Redis-backed limiter
// can replace it if Multi-Core fairness ever matters.
type IPRateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*rateBucket
}

func NewIPRateLimiter() *IPRateLimiter {
	return &IPRateLimiter{buckets: make(map[string]*rateBucket)}
}

// allow reports whether the IP is still under its per-minute budget, rolling
// the window when the bucket's reset has passed. It opportunistically evicts
// expired buckets so the map cannot grow without bound under many distinct IPs.
func (l *IPRateLimiter) allow(ip string, perMin int) bool {
	if perMin <= 0 {
		perMin = 60
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if len(l.buckets) > 10000 {
		for k, b := range l.buckets {
			if now.After(b.reset) {
				delete(l.buckets, k)
			}
		}
	}
	b, ok := l.buckets[ip]
	if !ok || now.After(b.reset) {
		l.buckets[ip] = &rateBucket{count: 1, reset: now.Add(time.Minute)}
		return true
	}
	if b.count >= perMin {
		return false
	}
	b.count++
	return true
}

// Limit wraps a public handler with a fixed per-minute budget keyed on the
// client IP, answering 429 + Retry-After when the budget is exhausted.
func (l *IPRateLimiter) Limit(perMin int, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		if ip == "" {
			ip = "unknown"
		}
		if !l.allow(ip, perMin) {
			w.Header().Set("Retry-After", "60")
			sendJSONError(w, "Too many requests, slow down", http.StatusTooManyRequests)
			return
		}
		next(w, r)
	}
}
