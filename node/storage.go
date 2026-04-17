package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/redis/go-redis/v9"
)

// StorageManager manages multiple storage paths for MC server data.
// It handles path assignment for new servers (auto-balancing by free space),
// tracking which server lives on which path (via Redis), and resolving
// server UUIDs to their actual filesystem location.
type StorageManager struct {
	paths []string        // Validated, available storage paths
	rdb   *redis.Client
	mu    sync.RWMutex
}

// StorageInfo holds disk usage information for a single storage path.
type StorageInfo struct {
	Path        string   `json:"path"`
	TotalBytes  uint64   `json:"total_bytes"`
	FreeBytes   uint64   `json:"free_bytes"`
	UsedBytes   uint64   `json:"used_bytes"`
	ServerCount int      `json:"server_count"`
	ServerUUIDs []string `json:"server_uuids"` // Top-level UUID directories on disk
}

// NewStorageManager creates a StorageManager with the given paths.
// Invalid or unmounted paths are skipped with a warning.
// If no paths are valid, falls back to the default path.
func NewStorageManager(pathsCSV string, rdb *redis.Client) *StorageManager {
	sm := &StorageManager{rdb: rdb}

	if pathsCSV == "" {
		// Default: backward-compatible single path
		baseDir, _ := os.Getwd()
		defaultPath := filepath.Join(baseDir, "dylaris_data", "servers")
		os.MkdirAll(defaultPath, 0755)
		sm.paths = []string{defaultPath}
		log.Printf("storage: using default path: %s", defaultPath)
		return sm
	}

	// Parse and validate each path
	raw := strings.Split(pathsCSV, ",")
	for _, p := range raw {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}

		abs, err := filepath.Abs(p)
		if err != nil {
			log.Printf("storage: [warn] cannot resolve path %q: %v — skipping", p, err)
			continue
		}

		// Check if path exists and is writable
		if err := validateStoragePath(abs); err != nil {
			log.Printf("storage: [warn] %s — skipping", err)
			continue
		}

		sm.paths = append(sm.paths, abs)
		log.Printf("storage: [ok] %s", abs)
	}

	// Fallback if no paths are valid
	if len(sm.paths) == 0 {
		baseDir, _ := os.Getwd()
		defaultPath := filepath.Join(baseDir, "dylaris_data", "servers")
		os.MkdirAll(defaultPath, 0755)
		sm.paths = []string{defaultPath}
		log.Printf("storage: [warn] no configured paths valid, falling back to %s", defaultPath)
	}

	return sm
}

// validateStoragePath checks that a path exists, is a directory, and is writable.
func validateStoragePath(path string) error {
	stat, err := os.Stat(path)
	if os.IsNotExist(err) {
		return fmt.Errorf("%s does not exist", path)
	}
	if err != nil {
		return fmt.Errorf("%s: %v", path, err)
	}
	if !stat.IsDir() {
		return fmt.Errorf("%s is not a directory", path)
	}

	// Write test
	testFile := filepath.Join(path, ".dylaris_write_test")
	if err := os.WriteFile(testFile, []byte("ok"), 0644); err != nil {
		return fmt.Errorf("%s is not writable: %v", path, err)
	}
	os.Remove(testFile)

	return nil
}

// Paths returns the list of available storage paths.
func (sm *StorageManager) Paths() []string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.paths
}

// GetServerPath resolves the storage path for a server UUID.
// 1. Redis lookup: node:{nodeID}:server:{uuid}:storage
// 2. Fallback: scan all paths for the UUID directory
// 3. If not found, returns the first path (for new servers, use SelectStoragePath instead)
func (sm *StorageManager) GetServerPath(serverUUID string) string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	// 1. Redis lookup
	if sm.rdb != nil {
		key := fmt.Sprintf("node:%s:server:%s:storage", nodeID, serverUUID)
		if val, err := sm.rdb.Get(context.Background(), key).Result(); err == nil && val != "" {
			// Verify the path is still in our list
			for _, p := range sm.paths {
				if p == val {
					return val
				}
			}
			log.Printf("storage: Redis path %q for %s not in active paths, scanning...", val, serverUUID)
		}
	}

	// 2. Scan all paths for existing UUID directory
	for _, p := range sm.paths {
		serverDir := filepath.Join(p, serverUUID)
		if stat, err := os.Stat(serverDir); err == nil && stat.IsDir() {
			// Found it — update Redis for future lookups
			sm.setRedisPath(serverUUID, p)
			return p
		}
	}

	// 3. Not found — return first path as default
	if len(sm.paths) > 0 {
		return sm.paths[0]
	}
	return ""
}

// GetServerDir returns the full directory path for a server UUID.
// This is the convenience method: storagePath + "/" + uuid
func (sm *StorageManager) GetServerDir(serverUUID string) string {
	return filepath.Join(sm.GetServerPath(serverUUID), serverUUID)
}

// SelectStoragePath picks the best path for a new server based on free space.
// After selection, it creates the server directory and stores the assignment in Redis.
func (sm *StorageManager) SelectStoragePath(serverUUID string, preferredPath string) (string, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// If admin explicitly chose a path, use it (if valid)
	if preferredPath != "" {
		for _, p := range sm.paths {
			if p == preferredPath {
				return sm.assignPath(serverUUID, p)
			}
		}
		return "", fmt.Errorf("preferred path %q not available", preferredPath)
	}

	// Auto: pick path with most free space
	var bestPath string
	var bestFree uint64

	for _, p := range sm.paths {
		free := getFreeSpace(p)
		if free > bestFree {
			bestFree = free
			bestPath = p
		}
	}

	if bestPath == "" {
		return "", fmt.Errorf("no storage paths available")
	}

	return sm.assignPath(serverUUID, bestPath)
}

// assignPath creates the server directory and stores the path in Redis.
// Caller must hold sm.mu.
func (sm *StorageManager) assignPath(serverUUID, storagePath string) (string, error) {
	serverDir := filepath.Join(storagePath, serverUUID)
	if err := os.MkdirAll(serverDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create server directory: %v", err)
	}

	sm.setRedisPath(serverUUID, storagePath)
	log.Printf("storage: assigned %s → %s", serverUUID, storagePath)
	return storagePath, nil
}

// RemoveServerPath cleans up the Redis key when a server is deleted.
func (sm *StorageManager) RemoveServerPath(serverUUID string) {
	if sm.rdb != nil {
		key := fmt.Sprintf("node:%s:server:%s:storage", nodeID, serverUUID)
		sm.rdb.Del(context.Background(), key)
	}
}

// GetStorageInfo returns disk usage info for all storage paths.
func (sm *StorageManager) GetStorageInfo() []StorageInfo {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	var infos []StorageInfo
	for _, p := range sm.paths {
		info := StorageInfo{Path: p}

		total, free := getDiskSpace(p)
		info.TotalBytes = total
		info.FreeBytes = free
		if total > free {
			info.UsedBytes = total - free
		}

		// Count and list server directories (UUID folders)
		entries, err := os.ReadDir(p)
		info.ServerUUIDs = []string{}
		if err == nil {
			for _, e := range entries {
				if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
					info.ServerCount++
					info.ServerUUIDs = append(info.ServerUUIDs, e.Name())
				}
			}
		}

		infos = append(infos, info)
	}
	return infos
}

// setRedisPath stores the storage path for a server in Redis (no expiry).
func (sm *StorageManager) setRedisPath(serverUUID, storagePath string) {
	if sm.rdb == nil {
		return
	}
	key := fmt.Sprintf("node:%s:server:%s:storage", nodeID, serverUUID)
	if err := sm.rdb.Set(context.Background(), key, storagePath, 0).Err(); err != nil {
		log.Printf("storage: failed to set Redis key %s: %v", key, err)
	}
}

// getFreeSpace returns the available bytes on the filesystem containing path.
func getFreeSpace(path string) uint64 {
	_, free := getDiskSpace(path)
	return free
}

// LogStorageInfo logs a summary of all storage paths at startup.
func (sm *StorageManager) LogStorageInfo() {
	infos := sm.GetStorageInfo()
	for _, info := range infos {
		totalGB := float64(info.TotalBytes) / (1024 * 1024 * 1024)
		freeGB := float64(info.FreeBytes) / (1024 * 1024 * 1024)
		log.Printf("storage: %s — %.1f GB free / %.1f GB total — %d servers",
			info.Path, freeGB, totalGB, info.ServerCount)
	}
}

// MigrateServerPath moves a server's data directory from its current storage path
// to targetPath. The server container must be stopped before calling this.
// Steps: validate → find current → check space → copy → verify → delete old → update Redis.
func (sm *StorageManager) MigrateServerPath(serverUUID, targetPath string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Validate target is in our configured paths
	validTarget := false
	for _, p := range sm.paths {
		if p == targetPath {
			validTarget = true
			break
		}
	}
	if !validTarget {
		return fmt.Errorf("target path %q is not a configured storage path", targetPath)
	}

	// Find current path: Redis lookup first, then filesystem scan
	currentPath := ""
	if sm.rdb != nil {
		key := fmt.Sprintf("node:%s:server:%s:storage", nodeID, serverUUID)
		if val, err := sm.rdb.Get(context.Background(), key).Result(); err == nil && val != "" {
			for _, p := range sm.paths {
				if p == val {
					currentPath = val
					break
				}
			}
		}
	}
	if currentPath == "" {
		for _, p := range sm.paths {
			if stat, err := os.Stat(filepath.Join(p, serverUUID)); err == nil && stat.IsDir() {
				currentPath = p
				break
			}
		}
	}
	if currentPath == "" {
		return fmt.Errorf("server %s not found on any storage path", serverUUID)
	}
	if currentPath == targetPath {
		return fmt.Errorf("server %s is already on path %q", serverUUID, targetPath)
	}

	srcDir := filepath.Join(currentPath, serverUUID)
	dstDir := filepath.Join(targetPath, serverUUID)

	// Check free space (dirSize returns int64, getFreeSpace returns uint64)
	srcSize := dirSize(srcDir) // int64, from stats_collector.go
	const spaceBuffer = uint64(512 * 1024 * 1024)
	free := getFreeSpace(targetPath)
	if srcSize > 0 && free < uint64(srcSize)+spaceBuffer {
		return fmt.Errorf("not enough free space on %s: need %s (+512MB buffer), have %s free",
			targetPath, formatBytes(uint64(srcSize)), formatBytes(free))
	}

	log.Printf("storage: migrating %s: %s → %s (%s)", serverUUID, currentPath, targetPath, formatBytes(uint64(srcSize)))

	if err := copyDir(srcDir, dstDir); err != nil {
		os.RemoveAll(dstDir)
		return fmt.Errorf("copy failed: %v", err)
	}

	// Verify: dest must be at least 90% of source size
	if dstSize := dirSize(dstDir); srcSize > 0 && dstSize < srcSize*9/10 {
		os.RemoveAll(dstDir)
		return fmt.Errorf("copy verification failed: source=%s, dest=%s", formatBytes(uint64(srcSize)), formatBytes(uint64(dstSize)))
	}

	// Remove old directory (data is safe at target)
	if err := os.RemoveAll(srcDir); err != nil {
		log.Printf("storage: [warn] could not remove old directory %s: %v", srcDir, err)
	}

	sm.setRedisPath(serverUUID, targetPath)
	log.Printf("storage: migration complete: %s → %s", serverUUID, targetPath)
	return nil
}

// formatBytes returns a human-readable byte size string.
func formatBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}
