// Package leader implements Redis-lease leader election for Core instances.
//
// Pattern adapted from gateway/hub/pkg/hub/leader.go: a single Redis key holds
// the current leader's instance ID with a TTL. The leader refreshes the TTL
// periodically; if it dies or loses connection, the TTL expires and any
// follower can pick the key up via SETNX on its next acquire attempt.
//
// Usage:
//
//	l := leader.New(redisClient, "dylaris:core:leader", cfg.CoreID)
//	l.Start(ctx)
//	defer l.Stop()
//
//	// Inside any periodic loop:
//	if !l.IsLeader() {
//	    continue
//	}
//	doSingletonWork()
//
// Timing constants mirror Hub's so operations staff have one mental model for
// all dylaris leases (30s TTL, 10s refresh, 5s acquire-retry). All methods
// are safe for concurrent callers — IsLeader is backed by atomic.Bool.
package leader

import (
	"context"
	"log"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	// LeaseTTL is how long a SETNX lease lives without a refresh. Long enough
	// to survive one missed refresh from a temporarily slow Core, short enough
	// that a crashed leader is replaced inside a minute.
	LeaseTTL = 30 * time.Second
	// LeaseRefresh is how often the leader extends its lease. Three refreshes
	// fit inside one TTL window, so one network blip during a refresh doesn't
	// cost the leadership.
	LeaseRefresh = 10 * time.Second
	// AcquireRetry is how often a follower retries SETNX. Same order as TTL
	// to keep failover within ~one minute without hammering Redis.
	AcquireRetry = 5 * time.Second
)

// Election is the small surface the rest of Core consumes — keeps tests
// trivial (a fake Election with IsLeader returning a settable bool is enough).
type Election interface {
	// IsLeader reports whether this instance currently holds the lease.
	// Cheap, safe to call from hot paths.
	IsLeader() bool
}

// RedisLeader runs a single election goroutine that flips between acquire and
// refresh modes.
type RedisLeader struct {
	rdb        *redis.Client
	key        string
	instanceID string

	isLeader atomic.Bool
	stopCh   chan struct{}
	doneCh   chan struct{}
}

// New constructs a leader without starting it. The key should be globally
// unique for the role being elected (e.g. "dylaris:core:leader"); instanceID
// should uniquely identify this Core process across the cluster — DYLARIS_CORE_ID
// from config is the right value.
func New(rdb *redis.Client, key, instanceID string) *RedisLeader {
	return &RedisLeader{
		rdb:        rdb,
		key:        key,
		instanceID: instanceID,
		stopCh:     make(chan struct{}),
		doneCh:     make(chan struct{}),
	}
}

// Start launches the election loop in its own goroutine. Returns immediately.
// Call Stop or cancel ctx to terminate; either triggers a best-effort lease
// release so a freshly started successor doesn't have to wait for the TTL.
func (l *RedisLeader) Start(ctx context.Context) {
	go l.run(ctx)
}

// Stop signals the election goroutine to exit and blocks until it does.
// Idempotent — calling twice is safe.
func (l *RedisLeader) Stop() {
	select {
	case <-l.stopCh:
		// already stopped
	default:
		close(l.stopCh)
	}
	<-l.doneCh
}

// IsLeader is the public read of the atomic flag. Falls through to a single
// atomic load, so calling it inside a 5s ticker loop is essentially free.
func (l *RedisLeader) IsLeader() bool {
	return l.isLeader.Load()
}

func (l *RedisLeader) run(ctx context.Context) {
	defer close(l.doneCh)
	defer l.release()

	for {
		// Check shutdown signals first so a fast Stop() doesn't get stuck on
		// a retry sleep.
		select {
		case <-l.stopCh:
			return
		case <-ctx.Done():
			return
		default:
		}

		if l.isLeader.Load() {
			l.runAsLeader(ctx)
			// Drop to acquire mode after a refresh failure.
			continue
		}

		acquireCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		acquired, err := l.rdb.SetNX(acquireCtx, l.key, l.instanceID, LeaseTTL).Result()
		cancel()

		if err != nil {
			log.Printf("leader(%s): SETNX error: %v", l.key, err)
			l.sleepOrStop(ctx, AcquireRetry)
			continue
		}
		if acquired {
			l.isLeader.Store(true)
			log.Printf("leader(%s): acquired lease (instance=%s)", l.key, l.instanceID)
			continue
		}
		l.sleepOrStop(ctx, AcquireRetry)
	}
}

func (l *RedisLeader) runAsLeader(ctx context.Context) {
	ticker := time.NewTicker(LeaseRefresh)
	defer ticker.Stop()

	for {
		select {
		case <-l.stopCh:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		refreshCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		ok, err := l.refresh(refreshCtx)
		cancel()

		if err != nil {
			log.Printf("leader(%s): refresh error: %v", l.key, err)
			l.isLeader.Store(false)
			return
		}
		if !ok {
			// Key value no longer matches our instance — TTL elapsed and a
			// successor took over. Step down so the outer loop tries to
			// re-acquire (or accept follower status).
			log.Printf("leader(%s): lease lost (value changed) — re-electing", l.key)
			l.isLeader.Store(false)
			return
		}
	}
}

// refresh extends the TTL only if the key value still equals instanceID.
// Uses a Lua script so the GET + PEXPIRE pair is atomic — without this, a
// race where the TTL expires between GET and EXPIRE could steal a successor's
// lease.
func (l *RedisLeader) refresh(ctx context.Context) (bool, error) {
	const script = `
		if redis.call('get', KEYS[1]) == ARGV[1] then
			return redis.call('pexpire', KEYS[1], ARGV[2])
		else
			return 0
		end
	`
	res, err := l.rdb.Eval(ctx, script, []string{l.key}, l.instanceID, LeaseTTL.Milliseconds()).Result()
	if err != nil {
		return false, err
	}
	n, _ := res.(int64)
	return n == 1, nil
}

// release best-effort frees the lease on shutdown. Same atomic-compare-and-
// delete pattern as refresh so we don't steal a successor's key when our
// own refresh has already failed.
func (l *RedisLeader) release() {
	if !l.isLeader.Load() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	const script = `
		if redis.call('get', KEYS[1]) == ARGV[1] then
			return redis.call('del', KEYS[1])
		else
			return 0
		end
	`
	l.rdb.Eval(ctx, script, []string{l.key}, l.instanceID)
	l.isLeader.Store(false)
}

func (l *RedisLeader) sleepOrStop(ctx context.Context, d time.Duration) {
	select {
	case <-l.stopCh:
	case <-ctx.Done():
	case <-time.After(d):
	}
}

// Always returns true. Useful in single-process tests + non-clustered local
// dev where you want to skip Redis entirely.
type alwaysLeader struct{}

// AlwaysLeader is an Election implementation that always reports true.
// Drop-in for non-clustered local dev where Redis isn't desired.
func AlwaysLeader() Election { return alwaysLeader{} }

func (alwaysLeader) IsLeader() bool { return true }
