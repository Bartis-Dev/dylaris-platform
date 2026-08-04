package main

import (
	"path/filepath"
	"testing"
)

// TestBackupSubServerStaysInsideTheServerRoot pins the node-side half of the
// sub-server traversal fix.
//
// Core validates the name, but the Node is where it becomes a path, and
// filepath.Join CLEANS rather than confines: Join("/data/servers/x", "../..")
// is "/data", not an error. Both backup and restore therefore recheck the
// joined path against the server root. This test drives that exact
// computation rather than the workers themselves, which need Redis, a storage
// manager and a live provider to run.
func TestBackupSubServerStaysInsideTheServerRoot(t *testing.T) {
	const root = "/data/dylaris_data/servers/srv-uuid"

	escaping := []string{
		"..",
		"../..",
		"../other-tenant",
		"world/../../..",
		"../../.node_secret",
	}
	for _, sub := range escaping {
		joined := filepath.Join(root, sub)
		if withinRoot(root, joined) {
			t.Errorf("sub-server %q joined to %q, which withinRoot accepted; "+
				"a backup would archive it and a restore would write to it", sub, joined)
		}
	}

	contained := []string{
		"survival",
		"survival-2",
		"creative_world",
		"a+b",
	}
	for _, sub := range contained {
		joined := filepath.Join(root, sub)
		if !withinRoot(root, joined) {
			t.Errorf("sub-server %q joined to %q was rejected; ordinary multi-world jobs must keep working",
				sub, joined)
		}
	}

	// The root itself is inside the root: an empty sub-server means the whole
	// container, and both workers skip the join entirely in that case.
	if !withinRoot(root, root) {
		t.Error("the server root is not within itself; the no-sub-server job would break")
	}
}

// TestSubServerSiblingPrefixIsNotContained guards the classic prefix mistake:
// a plain strings.HasPrefix without the separator would accept a sibling
// directory whose name merely starts with the root's.
func TestSubServerSiblingPrefixIsNotContained(t *testing.T) {
	const root = "/data/servers/srv"
	if withinRoot(root, "/data/servers/srv-evil") {
		t.Error("a sibling directory sharing the root's name prefix was accepted as contained")
	}
}
