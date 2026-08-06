package main

import (
	"os"
	"path/filepath"
	"testing"
)

// A whole-server backup walks the server root, and the archive store lives
// inside it. Archiving it makes every run carry all its predecessors.
func TestIsBackupStoreEntry(t *testing.T) {
	inside := []string{
		backupDirName,
		backupDirName + "/20260806-113851.tar.gz",
		backupDirName + "/nested/deeper.tar.gz",
	}
	for _, rel := range inside {
		if !isBackupStoreEntry(rel) {
			t.Errorf("isBackupStoreEntry(%q) = false; the archive store would be archived into itself", rel)
		}
	}

	outside := []string{
		"survival",
		"survival/server.properties",
		".active_server",
		".node_config.json",
		// A sibling whose name merely starts the same must stay in the archive.
		backupDirName + "-old",
		"world/" + backupDirName + "-notes.txt",
	}
	for _, rel := range outside {
		if isBackupStoreEntry(rel) {
			t.Errorf("isBackupStoreEntry(%q) = true; ordinary content must still be backed up", rel)
		}
	}
}

// The restore swaps the whole server root and deletes the old one, so the live
// archives have to be carried across or restoring one backup destroys the rest.
func TestCarryArchivesAcrossSwap(t *testing.T) {
	t.Run("live archives survive the swap", func(t *testing.T) {
		root := t.TempDir()
		stashed := filepath.Join(root, "srv.pre-restore")
		restored := filepath.Join(root, "srv")

		mkArchive(t, stashed, "20260806-113851.tar.gz", "live")
		mkArchive(t, stashed, "20260805-090000.tar.gz", "older")
		if err := os.MkdirAll(restored, 0o755); err != nil {
			t.Fatal(err)
		}

		if err := carryArchivesAcrossSwap(stashed, restored); err != nil {
			t.Fatalf("carryArchivesAcrossSwap: %v", err)
		}
		for name, want := range map[string]string{
			"20260806-113851.tar.gz": "live",
			"20260805-090000.tar.gz": "older",
		} {
			got, err := os.ReadFile(filepath.Join(restored, backupDirName, name))
			if err != nil {
				t.Errorf("%s did not survive the restore: %v", name, err)
				continue
			}
			if string(got) != want {
				t.Errorf("%s = %q, want %q", name, got, want)
			}
		}
	})

	t.Run("a stale copy from the archive is replaced", func(t *testing.T) {
		root := t.TempDir()
		stashed := filepath.Join(root, "srv.pre-restore")
		restored := filepath.Join(root, "srv")

		mkArchive(t, stashed, "current.tar.gz", "live")
		// What an old, self-nesting archive brought with it.
		mkArchive(t, restored, "ancient.tar.gz", "stale")

		if err := carryArchivesAcrossSwap(stashed, restored); err != nil {
			t.Fatalf("carryArchivesAcrossSwap: %v", err)
		}
		if _, err := os.Stat(filepath.Join(restored, backupDirName, "ancient.tar.gz")); !os.IsNotExist(err) {
			t.Errorf("the stale nested copy was kept (stat err = %v)", err)
		}
		if _, err := os.Stat(filepath.Join(restored, backupDirName, "current.tar.gz")); err != nil {
			t.Errorf("the live archive was lost: %v", err)
		}
	})

	t.Run("nothing to carry is not an error", func(t *testing.T) {
		root := t.TempDir()
		stashed := filepath.Join(root, "srv.pre-restore")
		restored := filepath.Join(root, "srv")
		for _, d := range []string{stashed, restored} {
			if err := os.MkdirAll(d, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		if err := carryArchivesAcrossSwap(stashed, restored); err != nil {
			t.Errorf("a server with no backups yet must restore cleanly: %v", err)
		}
	})
}

func mkArchive(t *testing.T, serverRoot, name, body string) {
	t.Helper()
	dir := filepath.Join(serverRoot, backupDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
