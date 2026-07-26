package storagereach

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	// claimKeyPrefix follows the fault records' layout (see faults.go): one key
	// per Core under this package's own namespace.
	claimKeyPrefix = keyPrefix + "claim:"

	// defaultClaimTTL is how long a Core's "I wrote my fleet beacon" claim
	// keeps that Core expected in its peers' storage.
	//
	// Two self-check intervals, and it is bounded from both sides. It must
	// outlive one missed 120s pass, or a Core that ticks late would silently
	// drop out of its peers' expectations and a genuinely fake-shared volume
	// would stop being detected. And it must stay SHORTER than
	// defaultBeaconMaxAge (300s), so a Core that stops writing loses its claim
	// BEFORE its last beacon ages out of storage. That ordering is what removes
	// the false not-shared window entirely: an observer only ever sees "claim
	// fresh, beacon there" or "no claim, not expected", never "claim fresh,
	// beacon gone".
	defaultClaimTTL = 240 * time.Second
)

func claimKey(coreID string) string { return claimKeyPrefix + coreID }

// PublishClaim records that coreID has just written its fleet beacon, so its
// peers know to expect that beacon and may read its absence as evidence.
//
// Redis is the channel precisely because it is NOT the storage under test: a
// claim survives the one failure the beacon itself cannot describe, which is a
// Core whose storage is fine reporting about a peer whose storage is not.
//
// The value is the write's unix second. Redis's own TTL is the primary bound;
// the timestamp lets a reader reject a claim that is too old regardless of what
// TTL it was written with.
func PublishClaim(ctx context.Context, rdb *redis.Client, coreID string, at time.Time, ttl time.Duration) error {
	if rdb == nil {
		return fmt.Errorf("storagereach: no redis client")
	}
	if ttl <= 0 {
		ttl = defaultClaimTTL
	}
	if err := rdb.Set(ctx, claimKey(coreID), strconv.FormatInt(at.Unix(), 10), ttl).Err(); err != nil {
		return fmt.Errorf("storagereach: publish beacon claim for %s: %w", coreID, err)
	}
	return nil
}

// ClearClaim withdraws a Core's claim. Idempotent: it runs on every pass whose
// beacon write failed, so the common case is a claim that is already gone.
func ClearClaim(ctx context.Context, rdb *redis.Client, coreID string) error {
	if rdb == nil {
		return fmt.Errorf("storagereach: no redis client")
	}
	if err := rdb.Del(ctx, claimKey(coreID)).Err(); err != nil {
		return fmt.Errorf("storagereach: clear beacon claim for %s: %w", coreID, err)
	}
	return nil
}

// FreshClaims returns which of coreIDs currently claim a recent beacon write.
//
// A real Redis failure is returned rather than reported as "nobody claimed":
// an empty set means "expect nothing", which would turn a Redis blip into a
// silent hole in the very detection this package exists to provide.
func FreshClaims(ctx context.Context, rdb *redis.Client, coreIDs []string, now time.Time, maxAge time.Duration) (map[string]bool, error) {
	out := make(map[string]bool, len(coreIDs))
	if len(coreIDs) == 0 {
		return out, nil
	}
	if rdb == nil {
		return nil, fmt.Errorf("storagereach: no redis client")
	}
	if maxAge <= 0 {
		maxAge = defaultClaimTTL
	}

	keys := make([]string, len(coreIDs))
	for i, id := range coreIDs {
		keys[i] = claimKey(id)
	}
	vals, err := rdb.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, fmt.Errorf("storagereach: read beacon claims: %w", err)
	}

	// MGET answers one value per key, but the reply comes off the wire and an
	// index panic on this path would take down a Core that is otherwise fine.
	n := len(vals)
	if len(coreIDs) < n {
		n = len(coreIDs)
	}
	cutoff := now.Add(-maxAge).Unix()
	for i := 0; i < n; i++ {
		s, ok := vals[i].(string)
		if !ok {
			// No key: this Core is not claiming a write, so nothing of its is
			// supposed to be in storage.
			continue
		}
		ts, err := strconv.ParseInt(s, 10, 64)
		if err != nil || ts < cutoff {
			// A corrupt or too-old claim is no claim at all. The explicit age
			// check is what stops a claim written with a longer TTL from
			// keeping a silent Core expected past its window.
			continue
		}
		out[coreIDs[i]] = true
	}
	return out, nil
}

// expectedParticipants narrows the participant set a SELF-CHECK aggregates over
// to the Cores this one is entitled to find in storage: itself, plus the peers
// whose absence would really be evidence about this Core's storage.
//
// This is the whole discriminator. Aggregate convicts a reporter of not-shared
// for every participant missing from its SeenPeers, so the participant list IS
// the assertion "these Cores' beacons must be here". Two kinds of peer do not
// belong in it:
//
//   - one that claims no recent beacon write - an old binary mid-rollout that
//     writes no beacon at all, a Core whose own write just failed, one that has
//     not got that far since booting. Nothing of its is supposed to be in
//     storage, so its absence is evidence about IT, not about this Core.
//   - one whose beacon was READ but carries a different fingerprint. The file is
//     right there, so the storage is demonstrably shared with it; it is just
//     labelling that storage differently. That is fingerprint-mismatch, which is
//     about the peer's config, and the round path is what convicts it of that.
//
// Pure, and applied ONLY on the self-check path. A config round is coordinated
// and time-boxed, so every participant in it is genuinely expected within the
// window; RunRound keeps passing Aggregate the full list, and a silent
// participant there is still no-response.
func expectedParticipants(participants []string, self string, claimed map[string]bool, mismatched []string) []string {
	skip := make(map[string]bool, len(mismatched))
	for _, id := range mismatched {
		skip[id] = true
	}
	out := make([]string, 0, len(participants))
	for _, id := range participants {
		if id == self {
			out = append(out, id)
			continue
		}
		if claimed[id] && !skip[id] {
			out = append(out, id)
		}
	}
	return out
}
