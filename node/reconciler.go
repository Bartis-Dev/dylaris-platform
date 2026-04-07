package main

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	reconcileInterval = 15 * time.Second
	maxCrashRetries   = 5
	aliveThreshold    = 60 * time.Second // container must run this long to reset crash count
)

// backoff durations indexed by crash count (0-based)
var backoffDurations = []time.Duration{
	0,
	30 * time.Second,
	60 * time.Second,
	120 * time.Second,
	120 * time.Second,
}

type reconcileInfo struct {
	crashCount    int
	lastRestart   time.Time
	lastSeenAlive time.Time
}

// protectedStatuses are statuses that indicate the server is in a transitional
// state managed by core/node commands — the reconciler must not interfere.
var protectedStatuses = map[string]bool{
	"installing":    true,
	"pending_setup": true,
	"stopping":      true,
	"starting":      true,
	"suspended":     true,
	"disk_full":     true,
}

// StartReconciler runs a periodic loop that compares actual Docker container
// state against the desired state stored in Redis and auto-restarts crashed
// containers when desired_state is "online".
func StartReconciler(ctx context.Context, rdb *redis.Client, dm *DockerManager) {
	log.Println("Reconciler started (interval: 15s)")
	tracker := make(map[string]*reconcileInfo)
	var mu sync.Mutex

	ticker := time.NewTicker(reconcileInterval)
	defer ticker.Stop()

	reconcile := func() {
		containers, err := dm.ListAllMCContainers()
		if err != nil {
			log.Printf("reconciler: failed to list containers: %v", err)
			return
		}

		mu.Lock()
		defer mu.Unlock()

		for _, c := range containers {
			desiredKey := fmt.Sprintf("dylaris:server:%s:desired_state", c.UUID)
			desired, err := rdb.Get(ctx, desiredKey).Result()
			if err != nil {
				// Key missing — no desired state published by core yet, skip
				continue
			}

			// Only act when desired state is "online" and container is not running
			if desired != "online" {
				// If desired is "stopped", clean up tracker
				delete(tracker, c.UUID)
				continue
			}

			if c.State == "running" {
				// Container is running as desired — update alive tracking
				info, exists := tracker[c.UUID]
				if exists {
					if time.Since(info.lastRestart) > aliveThreshold {
						// Container has been alive long enough — reset crash counter
						info.crashCount = 0
						info.lastSeenAlive = time.Now()
					}
				}
				continue
			}

			// Container is NOT running but desired_state is "online" — check for protected status
			statusKey := fmt.Sprintf("dylaris:server:%s:status", c.UUID)
			if status, err := rdb.Get(ctx, statusKey).Result(); err == nil && protectedStatuses[status] {
				continue
			}

			// Initialize tracker if needed
			info, exists := tracker[c.UUID]
			if !exists {
				info = &reconcileInfo{}
				tracker[c.UUID] = info
			}

			// Check if max retries exceeded
			if info.crashCount >= maxCrashRetries {
				// Set failed key so core can surface it
				failedKey := fmt.Sprintf("dylaris:server:%s:reconcile_failed", c.UUID)
				rdb.Set(ctx, failedKey, fmt.Sprintf("Container crashed %d times, auto-restart disabled", info.crashCount), 0)
				continue
			}

			// Check backoff
			backoffIdx := info.crashCount
			if backoffIdx >= len(backoffDurations) {
				backoffIdx = len(backoffDurations) - 1
			}
			if time.Since(info.lastRestart) < backoffDurations[backoffIdx] {
				continue
			}

			// Restart the container
			log.Printf("reconciler: restarting crashed container mc_%s (attempt %d/%d)", c.UUID, info.crashCount+1, maxCrashRetries)

			// Signal "restarting" status
			rdb.Set(ctx, statusKey, "restarting", 30*time.Second)

			if err := dm.PowerAction(c.UUID, "start"); err != nil {
				log.Printf("reconciler: failed to restart mc_%s: %v", c.UUID, err)
				info.crashCount++
				info.lastRestart = time.Now()
				continue
			}

			info.crashCount++
			info.lastRestart = time.Now()

			// Clear reconcile_failed key if it was set from a previous cycle
			failedKey := fmt.Sprintf("dylaris:server:%s:reconcile_failed", c.UUID)
			rdb.Del(ctx, failedKey)
		}
	}

	// Run immediately on startup
	reconcile()

	for {
		select {
		case <-ctx.Done():
			log.Println("Reconciler stopped")
			return
		case <-ticker.C:
			reconcile()
		}
	}
}
