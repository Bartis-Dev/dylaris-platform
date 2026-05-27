package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
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

// reconcileDeletedContainers detects server data directories whose Docker container
// has been manually deleted and recreates the container. Only acts when desired_state
// is "online" and the server is not in a protected transitional status.
func reconcileDeletedContainers(ctx context.Context, rdb *redis.Client, dm *DockerManager, storage *StorageManager) {
	// Build a set of UUIDs that currently have a Docker container (running or stopped).
	existing, err := dm.ListAllMCContainers()
	if err != nil {
		log.Printf("reconciler(deleted): failed to list containers: %v", err)
		return
	}
	containerUUIDs := make(map[string]bool, len(existing))
	for _, c := range existing {
		containerUUIDs[c.UUID] = true
	}

	// Scan all storage paths for server directories.
	for _, storagePath := range storage.Paths() {
		entries, err := os.ReadDir(storagePath)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() || e.Name() == "" || e.Name()[0] == '.' {
				continue
			}
			uuid := e.Name()

			// Skip if a container already exists for this UUID.
			if containerUUIDs[uuid] {
				continue
			}

			// Read the saved node config — if missing, we can't recreate.
			configPath := filepath.Join(storagePath, uuid, ".node_config.json")
			data, err := os.ReadFile(configPath)
			if err != nil {
				// No config file: either a non-server directory or pre-existing server.
				continue
			}

			// Skip if in a protected transitional status.
			statusKey := fmt.Sprintf("dylaris:server:%s:status", uuid)
			if status, err := rdb.Get(ctx, statusKey).Result(); err == nil && protectedStatuses[status] {
				continue
			}

			// Only recreate if desired_state is "online".
			desiredKey := fmt.Sprintf("dylaris:server:%s:desired_state", uuid)
			desired, err := rdb.Get(ctx, desiredKey).Result()
			if err != nil || desired != "online" {
				continue
			}

			var config ServerConfig
			if err := json.Unmarshal(data, &config); err != nil {
				log.Printf("reconciler(deleted): bad config for %s: %v", uuid, err)
				continue
			}

			// Guard against resurrecting a server whose active sub-server
			// was just deleted. The saved config still references the
			// dead sub-server name; if we Recreate*, Docker happily
			// auto-creates the empty bind-source dir and we end up with
			// a phantom MC server in a freshly-rebuilt empty folder. The
			// fix is to require both:
			//   a) the saved active sub-server name is non-empty, AND
			//   b) the corresponding dir actually exists on disk.
			// Anything else means there's no valid sub-server to start
			// and the server should stay down until the user picks one
			// in the Setup tab. Also force status to pending_setup so
			// the panel reflects that reality.
			if config.ActiveSubServer == "" {
				log.Printf("reconciler(deleted): mc_%s has empty active sub-server — leaving stopped, marking pending_setup", uuid)
				rdb.Set(ctx, statusKey, "pending_setup", 30*time.Second)
				continue
			}
			activeSubPath := filepath.Join(storagePath, uuid, config.ActiveSubServer)
			if st, err := os.Stat(activeSubPath); err != nil || !st.IsDir() {
				log.Printf("reconciler(deleted): mc_%s active sub-server %q missing on disk — leaving stopped, marking pending_setup", uuid, config.ActiveSubServer)
				rdb.Set(ctx, statusKey, "pending_setup", 30*time.Second)
				continue
			}

			log.Printf("reconciler(deleted): container mc_%s missing — recreating from saved config", uuid)

			// RecreateWithCommand handles stop+remove (no-op if missing), port binding, and start.
			// It errors only if network or container create fails.
			if err := dm.RecreateWithCommand(config); err != nil {
				log.Printf("reconciler(deleted): failed to recreate mc_%s: %v", uuid, err)
			} else {
				log.Printf("reconciler(deleted): mc_%s recreated and started", uuid)
				rdb.Set(ctx, statusKey, "starting", 30*time.Second)
			}
		}
	}
}

// StartReconciler runs a periodic loop that compares actual Docker container
// state against the desired state stored in Redis and auto-restarts crashed
// containers when desired_state is "online".
func StartReconciler(ctx context.Context, rdb *redis.Client, dm *DockerManager, storage *StorageManager) {
	log.Println("Reconciler started (interval: 15s)")
	tracker := make(map[string]*reconcileInfo)
	var mu sync.Mutex

	ticker := time.NewTicker(reconcileInterval)
	defer ticker.Stop()

	reconcile := func() {
		// Pass 1: restart crashed containers (container exists but not running).
		// Pass 2: recreate manually deleted containers (container missing entirely).
		reconcileDeletedContainers(ctx, rdb, dm, storage)

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
