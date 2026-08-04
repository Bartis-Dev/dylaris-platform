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

// CredentialBodyLimit bounds a request body on the UNAUTHENTICATED surface.
//
// 64 KiB is roughly a thousand times what these payloads need - a username, a
// password, a token, a TOTP code, at most a handful of security questions - and
// still far below anything that can hurt.
const CredentialBodyLimit = 64 << 10

// LimitBody caps how many bytes a handler can read from the request body.
//
// The IP rate limiter above bounds how MANY requests an anonymous caller may
// send; it says nothing about how BIG one may be. Every public handler decodes
// with json.NewDecoder(r.Body).Decode(&req), which allocates whatever a string
// field contains, so one request carrying a multi-gigabyte value was enough to
// exhaust Core - no credential, no second request needed.
//
// A body over the cap makes the handler's existing Decode fail, which its
// existing "Invalid JSON" 400 already covers, so no handler changes.
//
// Deliberately NOT applied as blanket middleware on /api: the upload handlers
// set their own, much larger MaxBytesReader, and an outer wrapper would win
// (the inner one only wraps the already-capped reader) and silently break every
// upload at the smaller limit. Wrapping the routes that take credentials keeps
// the two from interacting.
func LimitBody(max int64, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, max)
		}
		next(w, r)
	}
}

// Map-size bounds. maxBuckets is the ceiling that triggers eviction;
// bucketEvictTarget is how far down eviction brings it, leaving headroom so the
// sweep does not run on every call once busy.
const (
	maxBuckets        = 10000
	bucketEvictTarget = 9000
)

// allow reports whether the IP is still under its per-minute budget, rolling
// the window when the bucket's reset has passed.
//
// The map is bounded at maxBuckets. Expired buckets are dropped first; if a
// flood of DISTINCT live IPs keeps it over the ceiling with nothing expired,
// arbitrary buckets are evicted down to the target. An earlier version swept
// only expired entries and claimed that bounded the map - it did not, because
// under exactly that flood nothing is expired. Map iteration is randomized, so
// the eviction drops an arbitrary subset; an evicted IP simply gets a fresh
// window, which is the safe direction to fail under memory pressure. The XFF
// handling in clientIP is what stops a single client minting buckets by forging
// the header, so reaching this ceiling now takes genuinely many source IPs.
func (l *IPRateLimiter) allow(ip string, perMin int) bool {
	if perMin <= 0 {
		perMin = 60
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if len(l.buckets) >= maxBuckets {
		for k, b := range l.buckets {
			if now.After(b.reset) {
				delete(l.buckets, k)
			}
		}
		for k := range l.buckets {
			if len(l.buckets) <= bucketEvictTarget {
				break
			}
			delete(l.buckets, k)
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
