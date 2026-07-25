package storagereach

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	faultKeyPrefix = keyPrefix + "fault:"
	// faultTTL is 2.5 periodic ticks. A Core that stops ticking stops
	// asserting a fault it can no longer confirm, so a dead instance does not
	// leave a permanent red banner behind.
	faultTTL = 300 * time.Second
	// faultScanBudget bounds the SCAN the same way core_instances.go does:
	// this runs on an HTTP path shared with every other subsystem.
	faultScanBudget = 1000
)

// Fault is one Core's currently-failing self-check, as the panel shows it.
type Fault struct {
	CoreID       string   `json:"coreId"`
	Hostname     string   `json:"hostname,omitempty"`
	Status       Status   `json:"status"`
	Detail       string   `json:"detail,omitempty"`
	MissingPeers []string `json:"missingPeers,omitempty"`
	DeniedPeers  []string `json:"deniedPeers,omitempty"`
	// Since is when THIS failure started; At is the last confirmation. Both
	// unix seconds.
	Since int64 `json:"since"`
	At    int64 `json:"at"`
}

func faultKey(coreID string) string { return faultKeyPrefix + coreID }

// RecordFault writes or refreshes a Core's fault.
//
// Since is preserved while the status is unchanged, so the panel can say
// "failing since 09:14" instead of resetting to "just now" on every tick. A
// different status starts a new clock, because it is a different failure.
func RecordFault(ctx context.Context, rdb *redis.Client, f Fault) error {
	prev, err := rdb.Get(ctx, faultKey(f.CoreID)).Result()
	switch {
	case err == nil:
		var old Fault
		if json.Unmarshal([]byte(prev), &old) == nil && old.Status == f.Status && old.Since > 0 {
			f.Since = old.Since
		}
	case errors.Is(err, redis.Nil):
		// No prior record: the normal first-fault case. Keep f.Since as given.
	default:
		// A real Redis failure here must NOT be read as "no previous record" -
		// that would let one transient error silently overwrite a correct
		// Since with this tick's value, and every later successful call would
		// then faithfully preserve the corrupted value forever. The caller
		// retries on the next tick, and the record's TTL spans several ticks,
		// so skipping this write is strictly better than corrupting it.
		return fmt.Errorf("storagereach: read prior fault for %s: %w", f.CoreID, err)
	}
	data, err := json.Marshal(f)
	if err != nil {
		return err
	}
	if err := rdb.Set(ctx, faultKey(f.CoreID), data, faultTTL).Err(); err != nil {
		return fmt.Errorf("storagereach: record fault for %s: %w", f.CoreID, err)
	}
	return nil
}

// ClearFault removes a Core's fault. Idempotent: the periodic tick calls it on
// every healthy pass, so the common case is a fault that is not there.
func ClearFault(ctx context.Context, rdb *redis.Client, coreID string) error {
	if err := rdb.Del(ctx, faultKey(coreID)).Err(); err != nil {
		return fmt.Errorf("storagereach: clear fault for %s: %w", coreID, err)
	}
	return nil
}

// Faults returns every currently-recorded fault, sorted by Core id.
func Faults(ctx context.Context, rdb *redis.Client) ([]Fault, error) {
	if rdb == nil {
		return nil, fmt.Errorf("storagereach: no redis client")
	}
	var (
		out      []Fault
		cursor   uint64
		examined int
	)
	for {
		keys, next, err := rdb.Scan(ctx, cursor, faultKeyPrefix+"*", 100).Result()
		if err != nil {
			return nil, fmt.Errorf("storagereach: scan faults: %w", err)
		}
		for _, key := range keys {
			examined++
			val, err := rdb.Get(ctx, key).Result()
			if err != nil {
				if errors.Is(err, redis.Nil) {
					// Expired between SCAN and GET: the normal race, and a
					// fault that just aged out is not one worth reporting.
					continue
				}
				// A real Redis failure here must not be swallowed: doing so
				// would drop this Core's fault from the list and the panel
				// would render a healthier fleet than actually exists, which
				// is exactly what this feature exists to prevent. Fail loudly
				// (mirroring the Scan error above) instead of returning a
				// quietly-shortened, misleadingly-nil-error result.
				return nil, fmt.Errorf("storagereach: get fault %s: %w", key, err)
			}
			var f Fault
			if json.Unmarshal([]byte(val), &f) != nil || f.CoreID == "" {
				continue
			}
			out = append(out, f)
		}
		cursor = next
		if cursor == 0 || examined >= faultScanBudget {
			break
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CoreID < out[j].CoreID })
	return out, nil
}

// LocalStatus is this Core's own verdict, read by the route gate on every
// storage request and written by the self-check.
//
// It starts HEALTHY on purpose: a Core that has not yet run its boot check
// must not 503 its own storage routes in the seconds before that check
// completes. The boot check runs moments later and gates if it must.
type LocalStatus struct {
	mu     sync.RWMutex
	status Status
	detail string
}

func NewLocalStatus() *LocalStatus {
	return &LocalStatus{status: StatusOK}
}

func (l *LocalStatus) Set(status Status, detail string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.status, l.detail = status, detail
}

// Healthy reports whether this Core may serve storage traffic.
func (l *LocalStatus) Healthy() bool {
	if l == nil {
		// A Core built without the verifier (tests, tooling) must behave as
		// it did before the feature existed rather than deny every request.
		return true
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.status == StatusOK
}

func (l *LocalStatus) Snapshot() (Status, string) {
	if l == nil {
		return StatusOK, ""
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.status, l.detail
}
