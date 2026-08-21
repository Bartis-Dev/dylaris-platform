package main

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// preRestoreTTL is how long we keep the "moved aside" sub-server contents
// after a successful restore. Long enough that an admin can intervene if
// the restore caused damage, short enough that disk doesn't fill up.
const preRestoreTTL = 24 * time.Hour

// pendingDeleteTTL is the grace window for .pending-delete-* tombstones
// produced by delete_sub_server. The inline async retry already tries
// for ~78s; this catches the rare case where every retry hit busy-FS.
// 10 minutes is plenty -- the tombstone is hidden from the user the
// whole time, this is just disk-hygiene.
const pendingDeleteTTL = 10 * time.Minute

// restoreCleanupInterval is how often we walk the storage paths looking
// for stale .pre-restore-* directories.
const restoreCleanupInterval = time.Hour

// preRestoreStash matches the names RunRestore gives a stash:
// "<target>.pre-restore-<YYYYMMDD-HHMMSS>". The sub-server name comes first,
// so this cannot be a prefix check the way ".pending-delete-" is - but a bare
// strings.Contains was too loose in the other direction. The substring is
// reserved nowhere on the way in, so a directory called
// "notes.pre-restore-old" is something a user may legitimately create, and
// this walk would have deleted it 24 hours later without a word.
var preRestoreStash = regexp.MustCompile(`\.pre-restore-\d{8}-\d{6}$`)

// stageDirInfix is what RunRestore hands os.MkdirTemp as the fixed part of the
// staging directory's name. It lives here rather than in backup_restore.go so
// the thing that CREATES the name and the thing that has to recognise it again
// cannot drift apart.
const stageDirInfix = ".restore-"

// restoreStage matches a staging directory left behind by RunRestore:
// "<target>.restore-<random>". MkdirTemp's suffix is a long run of digits, so
// requiring six of them keeps a directory a user could plausibly create by hand
// ("world.restore-2") out of reach of the sweep.
//
// This is the second directory a restore creates, and until now only the first
// was swept. A restore killed mid-extraction (SIGKILL, node redeploy, OOM) left
// its whole staging tree behind for good - a full copy of the world, inside the
// server directory, counting against the server's own disk limit, with nothing
// that would ever remove it.
var restoreStage = regexp.MustCompile(`\` + stageDirInfix + `\d{6,}$`)

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
	preRestoreCutoff := now.Add(-preRestoreTTL)
	pendingCutoff := now.Add(-pendingDeleteTTL)
	purgedRestore, purgedPending := 0, 0
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
			// Both stashes are created at depth 1 (a whole-server restore, next
			// to the server dir) or depth 2 (a sub-server restore or a
			// delete_sub_server tombstone). Anything matching deeper than that
			// is not ours: the walk still VISITS depth 3, and a user can put a
			// directory there through the file browser, SFTP or a zip upload.
			// Neither name is reserved on the way in - only ".pending-delete-"
			// is, and only against the file API, not zip extraction.
			shallow := depthBelow(root, path) <= 2
			isRestore := shallow && (preRestoreStash.MatchString(base) || restoreStage.MatchString(base))
			isPending := shallow && strings.HasPrefix(base, ".pending-delete-")
			if !isRestore && !isPending {
				// Avoid descending into world-data unnecessarily; we only
				// care about top two levels (server / sub-server).
				if depthBelow(root, path) > 2 {
					return filepath.SkipDir
				}
				return nil
			}
			cutoff := preRestoreCutoff
			label := "pre-restore stash"
			switch {
			case isPending:
				cutoff = pendingCutoff
				label = "pending-delete tombstone"
			case restoreStage.MatchString(base):
				// Same 24h TTL as the stash: a restore still extracting after a
				// day is not running, it is dead.
				label = "restore staging dir"
			}
			if info.ModTime().Before(cutoff) {
				log.Printf("restore-cleanup: removing %s %s (age %v)", label, path, now.Sub(info.ModTime()).Round(time.Minute))
				if err := os.RemoveAll(path); err == nil {
					if isRestore {
						purgedRestore++
					} else {
						purgedPending++
					}
				}
			}
			return filepath.SkipDir // never recurse into the stash itself
		})
	}
	if purgedRestore > 0 || purgedPending > 0 {
		log.Printf("restore-cleanup: removed %d stash + %d pending-delete dir(s)", purgedRestore, purgedPending)
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
