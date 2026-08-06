package main

import (
	"os"
	"path/filepath"
	"testing"
)

// A storage migration MOVES the server directory: it copies to the target and
// then deletes the source. So everything the server needs must arrive, not just
// the files a user is allowed to duplicate.
//
// This is a regression test with a live cause. copyDir was taught to skip
// protected entries (a self-copy had zeroed a server's world AND both of its
// backups in one walk), and MigrateServerPath still called it. With backups
// present the migration then failed the 90% size check; without them it
// succeeded and deleted a source directory holding the only copy of
// .active_server and .node_config.json.
func TestMigrateServerPathKeepsProtectedEntries(t *testing.T) {
	sm, _ := newPlacementManager(t, 2)
	paths := sm.Paths()
	const uuid = "b083ff0c-fc16-47c5-9700-7b16486583e7_xegpnft249"

	srcDir := filepath.Join(paths[0], uuid)
	files := map[string]string{
		".active_server":    "survival",
		".node_config.json": `{"uuid":"x"}`,
		".dylaris.json":     "{}",
		filepath.Join(".dylaris-backups", "old.tar.gz"): "archive-bytes",
		filepath.Join("survival", "server.properties"):  "motd=hi",
	}
	for rel, body := range files {
		full := filepath.Join(srcDir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if err := sm.MigrateServerPath(uuid, paths[1]); err != nil {
		t.Fatalf("MigrateServerPath: %v", err)
	}

	dstDir := filepath.Join(paths[1], uuid)
	for rel, want := range files {
		body, err := os.ReadFile(filepath.Join(dstDir, rel))
		if err != nil {
			t.Errorf("%s did not survive the migration: %v", rel, err)
			continue
		}
		if string(body) != want {
			t.Errorf("%s = %q, want %q", rel, body, want)
		}
	}

	if _, err := os.Stat(srcDir); !os.IsNotExist(err) {
		t.Errorf("source directory still present after the move (stat err = %v)", err)
	}
}

// The user-facing copy keeps its behaviour: the two helpers must not converge
// back into one.
func TestCopyTreeAndCopyDirDisagreeOnProtectedEntries(t *testing.T) {
	src, verbatim, filtered := t.TempDir(), t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(src, ".active_server"), []byte("survival"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := copyTree(src, verbatim); err != nil {
		t.Fatalf("copyTree: %v", err)
	}
	if _, err := os.Stat(filepath.Join(verbatim, ".active_server")); err != nil {
		t.Errorf("copyTree dropped a protected entry: %v", err)
	}

	if err := copyDir(src, filtered); err != nil {
		t.Fatalf("copyDir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(filtered, ".active_server")); !os.IsNotExist(err) {
		t.Errorf("copyDir copied a protected entry (stat err = %v)", err)
	}
}
