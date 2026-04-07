package main

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"golang.org/x/time/rate"
)

// BeamThrottle manages bandwidth limiting for all Beam file transfer streams on this node.
// A single shared rate.Limiter provides automatic max-min fair sharing:
// - 2 users downloading → each gets ~half the bandwidth
// - 1 fast + 1 slow user → fast user gets the remainder automatically
// - Only 1 user → gets full bandwidth
type BeamThrottle struct {
	limiter *rate.Limiter
	mu      sync.RWMutex
	limit   int64 // bytes/sec, 0 = unlimited
}

// NewBeamThrottle creates a new throttle. It starts a background goroutine
// that watches Redis for bandwidth limit changes from Core settings.
func NewBeamThrottle(ctx context.Context, rdb *redis.Client) *BeamThrottle {
	bt := &BeamThrottle{}

	// Load initial limit from Redis
	if rdb != nil {
		if val, err := rdb.Get(ctx, "beam:bw_limit").Int64(); err == nil && val > 0 {
			bt.setLimit(val)
		}
	}

	// Watch for limit changes
	if rdb != nil {
		go bt.watchLimit(ctx, rdb)
	}

	return bt
}

// WaitN blocks until n bytes are allowed or ctx is cancelled.
// If unlimited (limit=0), returns immediately.
func (bt *BeamThrottle) WaitN(ctx context.Context, n int) error {
	bt.mu.RLock()
	lim := bt.limiter
	bt.mu.RUnlock()

	if lim == nil {
		return nil // unlimited
	}

	return lim.WaitN(ctx, n)
}

// Limit returns the current bandwidth limit in bytes/sec (0 = unlimited).
func (bt *BeamThrottle) Limit() int64 {
	bt.mu.RLock()
	defer bt.mu.RUnlock()
	return bt.limit
}

// setLimit updates the rate limiter. 0 = unlimited (removes limiter).
func (bt *BeamThrottle) setLimit(bytesPerSec int64) {
	bt.mu.Lock()
	defer bt.mu.Unlock()

	bt.limit = bytesPerSec

	if bytesPerSec <= 0 {
		bt.limiter = nil
		log.Println("beam-throttle: bandwidth unlimited")
		return
	}

	// Burst = 256KB or 1 second worth, whichever is larger
	burst := int(bytesPerSec)
	if burst > 256*1024 {
		burst = 256 * 1024
	}
	if burst < 64*1024 {
		burst = 64 * 1024
	}

	bt.limiter = rate.NewLimiter(rate.Limit(bytesPerSec), burst)
	log.Printf("beam-throttle: bandwidth limit set to %d bytes/sec (%.1f MB/s)", bytesPerSec, float64(bytesPerSec)/(1024*1024))
}

// watchLimit polls Redis every 10 seconds for bandwidth limit changes.
func (bt *BeamThrottle) watchLimit(ctx context.Context, rdb *redis.Client) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			val, err := rdb.Get(ctx, "beam:bw_limit").Int64()
			if err != nil {
				// Key doesn't exist or Redis error — keep current limit
				continue
			}

			bt.mu.RLock()
			current := bt.limit
			bt.mu.RUnlock()

			if val != current {
				bt.setLimit(val)
			}
		}
	}
}
