package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeOriginStamp drops the stamp migrate_out leaves in a storage path's
// staging dir.
func writeOriginStamp(t *testing.T, storagePath, serverUUID, owner string) {
	t.Helper()
	dir := filepath.Join(storagePath, migrationStagingDir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", dir, err)
	}
	if err := os.WriteFile(migrationOriginPath(storagePath, serverUUID), []byte(owner), 0644); err != nil {
		t.Fatalf("write origin stamp: %v", err)
	}
}

// TestSharedStorageOwner covers the guard that stands between a shared network
// mount and total data loss: on shared storage the source and target resolve to
// the SAME directory, so the transfer overwrites the live server with a copy of
// itself and the source's cleanup then deletes it.
func TestSharedStorageOwner(t *testing.T) {
	const uuid = "11111111-2222-3333-4444-555555555555"

	prevNodeID := nodeID
	t.Cleanup(func() { nodeID = prevNodeID })
	nodeID = "node-self"

	tests := []struct {
		name string
		// stamps maps a storage-path index to the node named in its stamp.
		stamps map[int]string
		paths  int
		want   string
	}{
		{
			name:  "no stamp at all is our own storage",
			paths: 1,
			want:  "",
		},
		{
			// The normal case after this node migrated a server away: the stamp
			// names us, so it must not block a later move back in.
			name:   "our own stale stamp is ignored",
			paths:  1,
			stamps: map[int]string{0: "node-self"},
			want:   "",
		},
		{
			name:   "a foreign stamp on our disk means shared storage",
			paths:  1,
			stamps: map[int]string{0: "node-other"},
			want:   "node-other",
		},
		{
			// Only one of several paths may be the shared mount.
			name:   "foreign stamp on the second path is found",
			paths:  3,
			stamps: map[int]string{2: "node-other"},
			want:   "node-other",
		},
		{
			name:   "empty stamp is not an owner",
			paths:  1,
			stamps: map[int]string{0: "   "},
			want:   "",
		},
		{
			name:   "our stamp does not mask a foreign one elsewhere",
			paths:  2,
			stamps: map[int]string{0: "node-self", 1: "node-other"},
			want:   "node-other",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			roots := make([]string, tt.paths)
			for i := range roots {
				roots[i] = t.TempDir()
			}
			for idx, owner := range tt.stamps {
				writeOriginStamp(t, roots[idx], uuid, owner)
			}

			sm := NewStorageManager(strings.Join(roots, ","), nil)
			if got := len(sm.Paths()); got != tt.paths {
				t.Fatalf("storage manager took %d paths, want %d", got, tt.paths)
			}

			if got := sharedStorageOwner(sm, uuid); got != tt.want {
				t.Errorf("sharedStorageOwner() = %q, want %q", got, tt.want)
			}
		})
	}
}

// A stamp for a DIFFERENT server must not block this server's move.
func TestSharedStorageOwnerIsPerServer(t *testing.T) {
	prevNodeID := nodeID
	t.Cleanup(func() { nodeID = prevNodeID })
	nodeID = "node-self"

	root := t.TempDir()
	writeOriginStamp(t, root, "other-server-uuid", "node-other")

	sm := NewStorageManager(root, nil)
	if got := sharedStorageOwner(sm, "the-server-being-moved"); got != "" {
		t.Errorf("sharedStorageOwner() = %q, want empty", got)
	}
}

// The stamp migrate_out writes must be exactly what the guard reads back, or
// the two halves drift apart and the guard silently stops working.
func TestOriginStampRoundTrip(t *testing.T) {
	const uuid = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"

	prevNodeID := nodeID
	t.Cleanup(func() { nodeID = prevNodeID })

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, migrationStagingDir), 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	// Written by the source exactly as handleMigrateOut does it.
	nodeID = "source-node"
	if err := os.WriteFile(migrationOriginPath(root, uuid), []byte(nodeID), 0644); err != nil {
		t.Fatalf("write origin stamp: %v", err)
	}

	// Read back by a DIFFERENT node that sees the same bytes.
	nodeID = "target-node"
	sm := NewStorageManager(root, nil)
	if got := sharedStorageOwner(sm, uuid); got != "source-node" {
		t.Errorf("sharedStorageOwner() = %q, want %q", got, "source-node")
	}
}
