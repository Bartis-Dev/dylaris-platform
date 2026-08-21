package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// The staging directory sits inside the server root for a sub-server restore,
// and that root is bind-mounted into the tenant's own MC container at /data.
// A predictable name plus os.MkdirAll meant a planted symlink was adopted
// silently, the extraction wrote through it, and the swap renamed the link
// itself into place.
func TestCreateStageDirNeverAdoptsWhatIsAlreadyThere(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "survival")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}

	// An escape hatch planted under a name from the pattern the code uses.
	outside := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	planted := target + stageDirInfix + "000000000"
	mustSymlink(t, outside, planted)

	seen := map[string]bool{}
	for range 5 {
		dir, err := createStageDir(target)
		if err != nil {
			t.Fatalf("createStageDir: %v", err)
		}
		if dir == planted {
			t.Fatal("createStageDir returned the planted path")
		}
		if seen[dir] {
			t.Fatalf("createStageDir returned %s twice", dir)
		}
		seen[dir] = true

		// Lstat, not Stat: Stat would report the symlink's target as a
		// directory and call the adopted link a success.
		fi, err := os.Lstat(dir)
		if err != nil {
			t.Fatalf("lstat: %v", err)
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			t.Fatal("the staging path is a symlink")
		}
		if !fi.IsDir() {
			t.Fatal("the staging path is not a directory")
		}
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) != 0 {
			t.Fatalf("the staging dir is not empty: %v %v", entries, err)
		}
	}

	// Nothing was written through the link.
	if entries, err := os.ReadDir(outside); err != nil || len(entries) != 0 {
		t.Fatalf("the symlink target was touched: %v %v", entries, err)
	}
}

func TestCreateStageDirIsReadableByTheContainer(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod bits are not meaningful on Windows")
	}
	root := t.TempDir()
	target := filepath.Join(root, "survival")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	dir, err := createStageDir(target)
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	// MkdirTemp's own 0700 would leave the restored world unreadable to an MC
	// image that does not run as this process's user.
	if perm := fi.Mode().Perm(); perm != 0o755 {
		t.Errorf("staging dir mode = %o, want 755", perm)
	}
}

// The name is produced in backup_restore.go and recognised in
// restore_cleanup.go. Pin that those two still agree, and that neither of the
// two leftover kinds is mistaken for the other.
func TestTheCleanupRecognisesTheNameARestoreActuallyProduces(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "survival")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	dir, err := createStageDir(target)
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Base(dir)

	if !restoreStage.MatchString(base) {
		t.Errorf("the cleanup does not recognise %q as a staging dir", base)
	}
	if preRestoreStash.MatchString(base) {
		t.Errorf("%q is matched as a pre-restore stash", base)
	}
	const stash = "survival.pre-restore-20260821-120000"
	if restoreStage.MatchString(stash) {
		t.Errorf("%q is matched as a staging dir", stash)
	}
}

// A restore killed mid-extraction leaves a full copy of the world behind. Only
// the stash was ever swept, so the staging tree stayed inside the server
// directory for good, counting against that server's own disk limit.
func TestRestoreCleanupRemovesAbandonedStagingDirs(t *testing.T) {
	sm, _ := newPlacementManager(t, 1)
	root := sm.Paths()[0]
	const uuid = "srv-uuid"

	sub := filepath.Join(root, uuid, "survival")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	age := func(p string, at time.Time) string {
		if err := os.Chtimes(p, at, at); err != nil {
			t.Fatal(err)
		}
		return p
	}
	old := time.Now().Add(-48 * time.Hour)

	abandoned, err := createStageDir(sub)
	if err != nil {
		t.Fatal(err)
	}
	age(abandoned, old)

	// A restore that started a moment ago must not be swept out from under
	// itself.
	inFlight, err := createStageDir(sub)
	if err != nil {
		t.Fatal(err)
	}

	// A user directory that merely looks similar. The server directory is the
	// file browser's root, so this is something they can create.
	userDir := filepath.Join(root, uuid, "world.restore-2")
	if err := os.MkdirAll(userDir, 0o755); err != nil {
		t.Fatal(err)
	}
	age(userDir, old)

	runRestoreCleanup(sm)

	if _, err := os.Stat(abandoned); !os.IsNotExist(err) {
		t.Errorf("abandoned staging dir kept: %v", err)
	}
	if _, err := os.Stat(inFlight); err != nil {
		t.Errorf("swept an in-flight staging dir: %v", err)
	}
	if _, err := os.Stat(userDir); err != nil {
		t.Errorf("deleted a user directory that only looks like ours: %v", err)
	}
}
