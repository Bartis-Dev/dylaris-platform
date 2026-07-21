// Package quota is the single source of truth for beam upload limits, shared by
// the node (beam gRPC upload path) and core (browser HTTP upload path) so both
// enforce the SAME admin-configured caps against the SAME per-user/day Redis
// bucket. Keeping the key format, thresholds, and read/increment logic in one
// place prevents the two paths from silently drifting apart, which would make a
// per-user quota trivially evadable by switching upload paths.
//
// Config keys (published by core SaveBeamSettings): MaxUploadBytesKey and
// DailyUploadBytesKey, both byte counts with 0 = unlimited. The per-user/day
// counter lives under DailyKey. Every check fails OPEN: a nil client, a missing
// or unparseable value, a non-positive limit, or an empty username all mean "no
// limit" so a misconfigured or unpublished setting never blocks uploads.
package quota

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	// MaxUploadBytesKey / DailyUploadBytesKey are the Redis config keys core
	// publishes from BeamSettings. Both hold a byte count; 0 or missing = no limit.
	MaxUploadBytesKey   = "beam:max_upload_bytes"
	DailyUploadBytesKey = "beam:daily_upload_bytes"

	// dailyCounterTTL keeps a day's counter around long enough to self-clean once
	// the date rolls over (the key already carries the UTC date).
	dailyCounterTTL = 48 * time.Hour
)

// DailyKey is the per-user, per-day upload counter key. Kept a pure function of
// (username, day) so its format is testable without Redis. The day is taken in
// UTC so the window does not shift with node/core timezone.
func DailyKey(username string, day time.Time) string {
	return fmt.Sprintf("dylaris:beam:daily:%s:%s", username, day.UTC().Format("2006-01-02"))
}

// ExceedsSizeCap reports whether a single upload of `size` bytes is larger than
// `capBytes`. A non-positive cap means "no cap".
func ExceedsSizeCap(size, capBytes int64) bool {
	if capBytes <= 0 {
		return false
	}
	return size > capBytes
}

// ExceedsDaily reports whether adding `incoming` to today's `used` would exceed
// `limit`. A non-positive limit means "no limit".
func ExceedsDaily(used, limit, incoming int64) bool {
	if limit <= 0 {
		return false
	}
	return used+incoming > limit
}

// CheckSizeCap reports whether a single upload of `size` bytes is allowed under
// the configured absolute per-upload cap. Fail-open: nil rdb, a missing or
// unparseable key, or a non-positive cap all return allowed=true. The returned
// capBytes is the configured cap (0 when none) for the caller's error message.
func CheckSizeCap(ctx context.Context, rdb *redis.Client, size int64) (allowed bool, capBytes int64) {
	if rdb == nil {
		return true, 0
	}
	capBytes, err := rdb.Get(ctx, MaxUploadBytesKey).Int64()
	if err != nil {
		return true, 0
	}
	return !ExceedsSizeCap(size, capBytes), capBytes
}

// CheckDailyQuota reports whether adding `incoming` bytes to `username`'s daily
// upload total is allowed under the configured daily limit, reading the
// per-user/day counter. Fail-open: nil rdb, an empty username, a missing or
// unparseable or non-positive limit, or a counter read error all return
// allowed=true. Returns the current used total and the configured limit for the
// caller's error message. Best-effort: the counter is advisory (bumped on
// completion via RecordDailyUsage), so this reflects the value at call time and
// concurrent uploads by the same user are not serialized against the limit.
func CheckDailyQuota(ctx context.Context, rdb *redis.Client, username string, incoming int64) (allowed bool, used, limit int64) {
	if rdb == nil || username == "" {
		return true, 0, 0
	}
	limit, err := rdb.Get(ctx, DailyUploadBytesKey).Int64()
	if err != nil || limit <= 0 {
		return true, 0, limit
	}
	used, err = rdb.Get(ctx, DailyKey(username, time.Now())).Int64()
	if err != nil {
		used = 0 // no counter yet today
	}
	return !ExceedsDaily(used, limit, incoming), used, limit
}

// RecordDailyUsage adds `n` bytes to `username`'s daily upload counter and
// re-arms its TTL (IncrBy + Expire in one pipeline). Best-effort: a Redis error
// just means the usage is not accounted this round. A no-op when rdb/username is
// absent or n is non-positive.
func RecordDailyUsage(ctx context.Context, rdb *redis.Client, username string, n int64) {
	if rdb == nil || username == "" || n <= 0 {
		return
	}
	key := DailyKey(username, time.Now())
	pipe := rdb.Pipeline()
	pipe.IncrBy(ctx, key, n)
	pipe.Expire(ctx, key, dailyCounterTTL)
	_, _ = pipe.Exec(ctx)
}
