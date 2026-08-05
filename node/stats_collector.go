package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// StatsPayload is the JSON published to Redis for each stats tick.
type StatsPayload struct {
	Timestamp  int64   `json:"ts"`
	CPU        float64 `json:"cpu"`
	CPULimit   float64 `json:"cpuLimit"`
	MemUsedMB  int64   `json:"memUsed"`
	MemLimitMB int64   `json:"memLimit"`
	// JavaHeapUsedMB is the post-GC live-heap size in MB, populated by
	// the log-shipper from `-Xlog:gc` summary lines. Omitted when no
	// GC has been observed yet (early startup, Java 8 image, etc.) so
	// the panel can fall back to the container metric without inferring
	// a misleading zero.
	JavaHeapUsedMB int64  `json:"javaHeapUsed,omitempty"`
	Players        int    `json:"players"`
	MaxPlayers     int    `json:"maxPlayers"`
	MOTD           string `json:"motd"`
}

// DiskUsagePayload is stored per-server in Redis.
type DiskUsagePayload struct {
	Total      int64            `json:"total"`
	Limit      int64            `json:"limit"`
	SubServers map[string]int64 `json:"subServers"`
	Warning    string           `json:"warning,omitempty"` // "", "80", "90", "full"
}

const (
	containerScanInterval = 10 * time.Second
	liveInterval          = 2 * time.Second
	pingInterval          = 15 * time.Second
	diskInterval          = 10 * time.Second
	diskFallbackInterval  = 5 * time.Minute
	historyBatchInterval  = 2 * time.Minute
	pingTimeout           = 3 * time.Second
	watchCacheDuration    = 2 * time.Second
)

// statsWriteProtectedStatuses are statuses the stats collector must NOT
// overwrite when it writes "stopped"/"restarting" for a vanished container.
// This is intentionally NARROWER than reconciler.go's package-level
// protectedStatuses (no "starting"/"suspended"): the collector only guards
// against clobbering an in-flight install/setup/shutdown or a disk-full
// hold, whereas the reconciler additionally protects transient lifecycle
// states it manages directly. Named distinctly so the difference is explicit
// rather than an accidental shadow of the same identifier.
var statsWriteProtectedStatuses = map[string]bool{
	"installing":    true,
	"pending_setup": true,
	"stopping":      true,
	"disk_full":     true,
}

// containerSnapshot holds the latest stats for a container (used for batching).
type containerSnapshot struct {
	mu       sync.Mutex
	uuid     string
	payload  *StatsPayload
	cpuLimit float64
}

// StartStatsCollector discovers running MC containers and collects stats.
// bufferMaxLen controls the Redis Stream MAXLEN for the per-server buffer.
// quota is the filesystem quota provider (may be nil or unavailable).
func StartStatsCollector(ctx context.Context, rdb *redis.Client, dm *DockerManager, nid string, bufferMaxLen int64, quota *QuotaSet) {
	log.Println("Stats collector started")

	tracked := make(map[string]context.CancelFunc) // uuid -> cancel
	snapshots := make(map[string]*containerSnapshot)
	var mu sync.Mutex

	ticker := time.NewTicker(containerScanInterval)
	defer ticker.Stop()

	// History batch ticker: every 2 min, collect snapshots from ALL containers
	// and push a single batch to the node's Redis stream for Core to consume.
	batchKey := fmt.Sprintf("dylaris:node:%s:stats:batch", nid)
	go func() {
		batchTicker := time.NewTicker(historyBatchInterval)
		defer batchTicker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-batchTicker.C:
				mu.Lock()
				var items []map[string]interface{}
				for _, snap := range snapshots {
					snap.mu.Lock()
					if snap.payload != nil {
						items = append(items, map[string]interface{}{
							"uuid":         snap.uuid,
							"cpu":          snap.payload.CPU,
							"cpuLimit":     snap.cpuLimit,
							"memUsed":      snap.payload.MemUsedMB,
							"memLimit":     snap.payload.MemLimitMB,
							"javaHeapUsed": snap.payload.JavaHeapUsedMB,
							"players":      snap.payload.Players,
							"maxPlayers":   snap.payload.MaxPlayers,
						})
					}
					snap.mu.Unlock()
				}
				mu.Unlock()

				if len(items) > 0 {
					batch := map[string]interface{}{
						"ts":    time.Now().Unix(),
						"stats": items,
					}
					data, _ := json.Marshal(batch)
					rdb.XAdd(ctx, &redis.XAddArgs{
						Stream: batchKey,
						MaxLen: 100,
						Approx: true,
						Values: map[string]interface{}{"data": string(data)},
					})
				}
			}
		}
	}()

	scan := func() {
		containers, err := dm.ListRunningMCContainers()
		if err != nil {
			log.Printf("stats: list containers error: %v", err)
			return
		}

		running := make(map[string]bool)
		for _, c := range containers {
			running[c.UUID] = true
		}

		mu.Lock()
		defer mu.Unlock()

		// Stop collectors for containers that are no longer running
		for uuid, cancel := range tracked {
			if !running[uuid] {
				cancel()
				delete(tracked, uuid)
				delete(snapshots, uuid)
				// Check desired state before reporting status — but don't overwrite protected statuses
				statusKey := fmt.Sprintf("dylaris:server:%s:status", uuid)
				currentStatus, _ := rdb.Get(ctx, statusKey).Result()
				// statsWriteProtectedStatuses alone cannot answer this: it reads the
				// status key, which Core's status watcher drains every 5 seconds, so
				// during a long operation it usually reads empty and the guard passes.
				// Observed live during a reinstall - the collector wrote "restarting"
				// over the node's own "installing". That is not just a wrong label:
				// the message it stands for ("Reconciler will handle restart") is
				// false while the node is deliberately holding the reconciler off.
				if !statsWriteProtectedStatuses[currentStatus] && !isNodeBusy(ctx, rdb, uuid) {
					desiredKey := fmt.Sprintf("dylaris:server:%s:desired_state", uuid)
					desired, _ := rdb.Get(ctx, desiredKey).Result()
					if desired == "online" {
						// Reconciler will handle restart — signal "restarting" instead of "stopped"
						rdb.Set(ctx, statusKey, "restarting", 30*time.Second)
					} else {
						rdb.Set(ctx, statusKey, "stopped", 30*time.Second)
					}
				}
			}
		}

		// Start collectors for new containers
		for _, c := range containers {
			if _, ok := tracked[c.UUID]; !ok {
				cctx, cancel := context.WithCancel(ctx)
				tracked[c.UUID] = cancel
				snap := &containerSnapshot{uuid: c.UUID}
				snapshots[c.UUID] = snap
				go collectForContainer(cctx, rdb, dm, c.UUID, c.ContainerName, bufferMaxLen, quota, snap)
			}
		}
	}

	scan()
	for {
		select {
		case <-ctx.Done():
			mu.Lock()
			for _, cancel := range tracked {
				cancel()
			}
			mu.Unlock()
			return
		case <-ticker.C:
			scan()
		}
	}
}

func collectForContainer(ctx context.Context, rdb *redis.Client, dm *DockerManager, uuid, containerName string, bufferMaxLen int64, quota *QuotaSet, snap *containerSnapshot) {
	log.Printf("stats: tracking %s", containerName)

	liveTicker := time.NewTicker(liveInterval)
	pingTicker := time.NewTicker(pingInterval)

	// Disk interval depends on quota availability
	dInterval := diskFallbackInterval
	if quota.AnyAvailable() {
		dInterval = diskInterval
	}
	diskTicker := time.NewTicker(dInterval)

	defer liveTicker.Stop()
	defer pingTicker.Stop()
	defer diskTicker.Stop()

	liveKey := fmt.Sprintf("dylaris:server:%s:stats:live", uuid)
	bufferKey := fmt.Sprintf("dylaris:server:%s:stats:buffer", uuid)
	watchKey := fmt.Sprintf("dylaris:server:%s:stats:watching", uuid)
	diskKey := fmt.Sprintf("dylaris:server:%s:stats:disk", uuid)
	statusKey := fmt.Sprintf("dylaris:server:%s:status", uuid)
	// Live heap size from the log-shipper's GC log parser. May be absent
	// (no GC yet, Java 8 image, etc.) -- we treat that as "no value" and
	// the panel falls back to the container metric.
	heapKey := fmt.Sprintf("dylaris:server:%s:java-heap", uuid)

	var lastPing *SLPResponse
	var pingMu sync.Mutex

	// Cache watching key check (avoid Redis roundtrip every 2s)
	var watchCache bool
	var watchCacheTime time.Time

	// Cache status to avoid Redis roundtrips every 2s
	var lastWrittenStatus string
	var statusCacheTime time.Time

	isWatching := func() bool {
		if time.Since(watchCacheTime) < watchCacheDuration {
			return watchCache
		}
		n, err := rdb.Exists(ctx, watchKey).Result()
		watchCache = err == nil && n > 0
		watchCacheTime = time.Now()
		return watchCache
	}

	// Initial ping. Use the admin-configurable global container port (default
	// 25565) so the ping target stays in sync with the port MC actually
	// listens on inside the container.
	go func() {
		resp, err := PingMinecraftServer(fmt.Sprintf("%s:%d", containerName, getContainerPort()), pingTimeout)
		if err == nil {
			pingMu.Lock()
			lastPing = resp
			pingMu.Unlock()
		}
	}()

	// Initial disk scan
	go func() {
		usage := getDiskUsage(ctx, rdb, uuid, quota)
		if usage != nil {
			data, _ := json.Marshal(usage)
			rdb.Set(ctx, diskKey, string(data), 10*time.Minute)
		}
	}()

	getCPULimit := func() float64 {
		info, err := dm.cli.ContainerInspect(dm.ctx, containerName)
		if err != nil {
			return 0
		}
		if info.HostConfig.NanoCPUs > 0 {
			return float64(info.HostConfig.NanoCPUs) / 1e9
		}
		return 0
	}

	cpuLimit := getCPULimit()
	snap.mu.Lock()
	snap.cpuLimit = cpuLimit
	snap.mu.Unlock()

	var prevCPU *PrevCPUStats
	collectAndPublish := func() {
		stats, newPrev, err := dm.GetContainerStats(containerName, prevCPU)
		prevCPU = newPrev
		if err != nil || stats == nil {
			return
		}

		pingMu.Lock()
		ping := lastPing
		pingMu.Unlock()

		// Scale CPU% relative to allocated cores (100% = fully using allocated CPU)
		cpuPct := stats.CPUPercent
		if cpuLimit > 0 {
			cpuPct = cpuPct / cpuLimit
		}

		payload := StatsPayload{
			Timestamp:  time.Now().Unix(),
			CPU:        cpuPct,
			CPULimit:   cpuLimit,
			MemUsedMB:  stats.MemUsedMB,
			MemLimitMB: stats.MemLimitMB,
		}
		// Pull the latest post-GC heap size if the log-shipper has seen
		// one. Best-effort: a missing/unparseable key just means we omit
		// the field for this tick and the panel keeps using the previous
		// value (chart smoothing) or falls back to container memory.
		if heapStr, err := rdb.Get(ctx, heapKey).Result(); err == nil {
			if mb, perr := strconv.ParseInt(heapStr, 10, 64); perr == nil && mb > 0 {
				payload.JavaHeapUsedMB = mb
			}
		}
		if ping != nil {
			payload.Players = ping.Players
			payload.MaxPlayers = ping.MaxPlayers
			payload.MOTD = ping.MOTD
		}

		data, _ := json.Marshal(payload)

		// Update snapshot for batch collection
		snap.mu.Lock()
		snap.payload = &payload
		snap.mu.Unlock()

		// Always write to buffer stream (replaces sorted set)
		rdb.XAdd(ctx, &redis.XAddArgs{
			Stream: bufferKey,
			MaxLen: bufferMaxLen,
			Approx: true,
			Values: map[string]interface{}{"data": string(data)},
		})

		// Only publish to pub/sub if someone is watching (on-demand)
		if isWatching() {
			rdb.Publish(ctx, liveKey, string(data))
		}

		// Container is running and collecting stats → online
		newStatus := "online"
		if newStatus != lastWrittenStatus || time.Since(statusCacheTime) > 15*time.Second {
			// Check protected statuses before overwriting
			currentStatus, _ := rdb.Get(ctx, statusKey).Result()
			if !statsWriteProtectedStatuses[currentStatus] {
				rdb.Set(ctx, statusKey, newStatus, 30*time.Second)
				lastWrittenStatus = newStatus
			}
			statusCacheTime = time.Now()
		}
	}

	for {
		select {
		case <-ctx.Done():
			log.Printf("stats: stopped tracking %s", containerName)
			return
		case <-liveTicker.C:
			collectAndPublish()
		case <-pingTicker.C:
			go func() {
				resp, err := PingMinecraftServer(fmt.Sprintf("%s:%d", containerName, getContainerPort()), pingTimeout)
				if err == nil {
					pingMu.Lock()
					lastPing = resp
					pingMu.Unlock()
				}
			}()
		case <-diskTicker.C:
			go func() {
				usage := getDiskUsage(ctx, rdb, uuid, quota)
				if usage != nil {
					data, _ := json.Marshal(usage)
					rdb.Set(ctx, diskKey, string(data), 10*time.Minute)

					if usage.Warning == "full" {
						// Check if container is actually running before stopping
						currentStatus, _ := rdb.Get(ctx, statusKey).Result()
						if currentStatus == "online" || currentStatus == "" {
							log.Printf("Disk quota full for %s — stopping server", uuid)
							gracefulStop(rdb, uuid, dm)
							rdb.Set(ctx, fmt.Sprintf("dylaris:server:%s:status", uuid), "disk_full", 30*time.Second)
							// Publish disk_full event so panel reacts immediately
							evt, _ := json.Marshal(map[string]interface{}{
								"type":    "disk_warning",
								"warning": "disk_full",
								"total":   usage.Total,
								"limit":   usage.Limit,
							})
							rdb.Publish(ctx, liveKey, string(evt))
						}
					} else {
						// Push disk warning via live channel so panel reacts immediately
						if usage.Warning != "" {
							evt, _ := json.Marshal(map[string]interface{}{
								"type":    "disk_warning",
								"warning": usage.Warning,
								"total":   usage.Total,
								"limit":   usage.Limit,
							})
							rdb.Publish(ctx, liveKey, string(evt))
						}

						// Auto-resolve: if status is disk_full but usage is below 100%, reset to stopped
						currentStatus, _ := rdb.Get(ctx, statusKey).Result()
						if currentStatus == "disk_full" {
							log.Printf("Disk quota resolved for %s — setting status to stopped", uuid)
							rdb.Set(ctx, fmt.Sprintf("dylaris:server:%s:status", uuid), "stopped", 30*time.Second)
							evt, _ := json.Marshal(map[string]interface{}{
								"type":    "disk_warning",
								"warning": "resolved",
								"total":   usage.Total,
								"limit":   usage.Limit,
							})
							rdb.Publish(ctx, liveKey, string(evt))
						}
					}
				}
			}()
		}
	}
}

// getDiskUsage returns disk usage, from the quota system where the filesystem
// supports it and from a du scan where it does not.
//
// The du path matters twice over: without it a server on NFS/CIFS reported NO
// disk usage at all (this function simply returned nil), and it is the only
// measurement the disk guard has to work with there.
func getDiskUsage(ctx context.Context, rdb *redis.Client, uuid string, quota *QuotaSet) *DiskUsagePayload {
	var usage *DiskUsagePayload
	if quota != nil && quota.IsAvailableFor(uuid) {
		usage = quota.GetDiskUsage(uuid)
	}
	if usage == nil {
		usage = duDiskUsage(ctx, rdb, uuid)
	}
	if usage == nil {
		return nil
	}

	// Set warning level based on limit utilization
	if usage.Limit > 0 && usage.Total > 0 {
		pct := float64(usage.Total) / float64(usage.Limit)
		switch {
		case pct >= 1.0:
			usage.Warning = "full"
		case pct >= 0.9:
			usage.Warning = "90"
		case pct >= 0.8:
			usage.Warning = "80"
		}
	}

	return usage
}

// duDiskUsage measures a server directory with du, for filesystems that cannot
// do project quotas. Deliberately not called on quota-capable paths: du walks
// every inode, which is exactly what quotas exist to avoid.
func duDiskUsage(ctx context.Context, rdb *redis.Client, uuid string) *DiskUsagePayload {
	if globalStorageMgr == nil {
		return nil
	}
	dir := globalStorageMgr.GetServerDir(uuid)
	if dir == "" {
		return nil
	}
	total := dirSize(dir)
	if total < 0 {
		return nil
	}
	return &DiskUsagePayload{Total: total, Limit: loadDiskLimit(ctx, rdb, uuid) * 1024 * 1024}
}

// dirSize returns the total size of a directory in bytes.
func dirSize(path string) int64 {
	out, err := exec.Command("du", "-sb", path).Output()
	if err == nil {
		fields := strings.Fields(string(out))
		if len(fields) > 0 {
			if size, err := strconv.ParseInt(fields[0], 10, 64); err == nil {
				return size
			}
		}
	}

	// Fallback: manual walk
	var size int64
	filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			size += info.Size()
		}
		return nil
	})
	return size
}
