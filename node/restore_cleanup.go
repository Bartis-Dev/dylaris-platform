package main

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// preRestoreTTL is how long we keep the "moved aside" sub-server contents
// after a successful restore. Long enough that an admin can intervene if
// the restore caused damage, short enough that disk doesn't fill up.
const preRestoreTTL = 24 * time.Hour

// restoreCleanupInterval is how often we walk the storage paths looking
// for stale .pre-restore-* directories.
const restoreCleanupInterval = time.Hour

// StartRestoreCleanup runs a background goroutine that periodically deletes
// .pre-restore-<timestamp> directories left behind by RunRestore once they
// are older than preRestoreTTL. The async cleanup in RunRestore itself
// covers the happy path; this is the safety net for crash / SIGKILL.
func StartRestoreCleanup(ctx context.Context, sm *StorageManager) {
	go func() {
		// Run once shortly after boot so a clean restart immediately picks
		// up leftover stashes from the previous process.
		time.Sleep(30 * time.Second)
		runRestoreCleanup(sm)

		ticker := time.NewTicker(restoreCleanupInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				runRestoreCleanup(sm)
			}
		}
	}()
}

func runRestoreCleanup(sm *StorageManager) {
	roots := storageRoots(sm)
	now := time.Now()
	cutoff := now.Add(-preRestoreTTL)
	purged := 0
	for _, root := range roots {
		// root is e.g. ./dylaris_data/servers; one level down are
		// per-server UUID directories. The stash sits one more level
		// deeper, next to the sub-server we restored into.
		filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			if !info.IsDir() {
				return nil
			}
			base := filepath.Base(path)
			if !strings.Contains(base, ".pre-restore-") {
				// Avoid descending into world-data unnecessarily; we only
				// care about top two levels (server / sub-server).
				if depthBelow(root, path) > 2 {
					return filepath.SkipDir
				}
				return nil
			}
			// .pre-restore-* found — only purge when info.ModTime is older
			// than the TTL. ModTime survives Docker volume mounts, atime
			// often does not.
			if info.ModTime().Before(cutoff) {
				log.Printf("restore-cleanup: removing %s (age %v)", path, now.Sub(info.ModTime()).Round(time.Minute))
				if err := os.RemoveAll(path); err == nil {
					purged++
				}
			}
			return filepath.SkipDir // never recurse into the stash itself
		})
	}
	if purged > 0 {
		log.Printf("restore-cleanup: removed %d stale stash dir(s)", purged)
	}
}

// storageRoots returns every server-root path the StorageManager knows
// about. Falls back to the legacy default for the no-storage-manager case
// so tests / local dev still work.
func storageRoots(sm *StorageManager) []string {
	if sm == nil {
		return []string{"./dylaris_data/servers"}
	}
	roots := sm.Paths()
	if len(roots) == 0 {
		roots = []string{"./dylaris_data/servers"}
	}
	return roots
}

// depthBelow returns how many directory hops separate `path` from `root`.
// Used so the walker bails out once it descends past the sub-server level.
func depthBelow(root, path string) int {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." {
		return 0
	}
	return strings.Count(filepath.ToSlash(rel), "/") + 1
}
