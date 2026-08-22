package main

import (
	"os"
	"path/filepath"
	"testing"
)

// Core enforces the per-server sub-server limit from this map, and the du path
// - the one every install without xfs/ext4 project quotas takes - used to omit
// it entirely. len(nil) is never >= a positive limit, so the setting silently
// did nothing there. These are the distinctions the map has to get right.
func TestScanSubServerSizes(t *testing.T) {
	root := t.TempDir()

	mustDir := func(name string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(root, name), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
	}
	mustDir("survival")
	mustDir("creative")
	mustDir(".dylaris-backups")                 // not a sub-server
	mustDir(filepath.Join("survival", "world")) // a child, not a sibling

	if err := os.WriteFile(filepath.Join(root, ".active_server"), []byte("survival"), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "survival", "server.jar"), make([]byte, 2048), 0o644); err != nil {
		t.Fatalf("write jar: %v", err)
	}

	got := scanSubServerSizes(root)

	if len(got) != 2 {
		t.Fatalf("got %v, want exactly survival and creative", got)
	}
	for _, want := range []string{"survival", "creative"} {
		if _, ok := got[want]; !ok {
			t.Errorf("%q is missing from %v", want, got)
		}
	}
	if _, ok := got[".dylaris-backups"]; ok {
		t.Error("a dot-directory was counted as a sub-server: it would inflate the limit Core enforces")
	}
	if _, ok := got["world"]; ok {
		t.Error("a directory inside a sub-server was counted as one of its own")
	}
	if got["survival"] < 2048 {
		t.Errorf("survival measured %d bytes, want at least the 2048 written into it", got["survival"])
	}
}

// A server directory that is not there yet is a real state (a server created
// but never set up). It must read as an empty map rather than a nil one, so a
// caller can tell "the node looked and found nothing" from "the node never
// reported".
func TestScanSubServerSizesOnAMissingDirectory(t *testing.T) {
	got := scanSubServerSizes(filepath.Join(t.TempDir(), "does-not-exist"))
	if got == nil {
		t.Fatal("a missing directory returned nil, which JSON-encodes as null - exactly the value that made the limit unenforceable")
	}
	if len(got) != 0 {
		t.Errorf("got %v, want empty", got)
	}
}
