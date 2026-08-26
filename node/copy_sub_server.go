package main

// Duplicate one sub-server's directory tree into a second sub-server of the
// same server.
//
// This is what makes a version bump reversible: copy the server, move the copy
// to the new Minecraft version, and switch between them. The source must be
// stopped before this runs (Core enforces it) - copying a running server's
// world files would capture them mid-write.
//
// copy_sub_server payload (from core):
//
//	uuid, sourceSubServer, targetSubServer
//
// Both names are re-checked here for the same reason install_mod re-checks
// its own: this arrives over a queue.

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"dylaris-pkg/validate"

	"github.com/redis/go-redis/v9"
)

type copySubServerPayload struct {
	UUID            string `json:"uuid"`
	SourceSubServer string `json:"sourceSubServer"`
	TargetSubServer string `json:"targetSubServer"`
}

func runCopySubServer(ctx context.Context, rdb *redis.Client, storage *StorageManager, payload string) {
	var pl copySubServerPayload
	if err := json.Unmarshal([]byte(payload), &struct {
		Config *copySubServerPayload `json:"config"`
	}{Config: &pl}); err != nil {
		log.Printf("copy_sub_server: decode failed: %v", err)
		return
	}
	if !validate.IsSubServerName(pl.SourceSubServer) || !validate.IsSubServerName(pl.TargetSubServer) {
		log.Printf("copy_sub_server: invalid sub-server name(s) %q -> %q", pl.SourceSubServer, pl.TargetSubServer)
		return
	}
	if pl.SourceSubServer == pl.TargetSubServer {
		log.Printf("copy_sub_server: source and target are the same (%q)", pl.SourceSubServer)
		return
	}
	serverPath := storage.GetServerDir(pl.UUID)
	if serverPath == "" {
		log.Printf("copy_sub_server: storage lookup for %s returned empty path", pl.UUID)
		return
	}
	if !commandsInFlight.enter("copy_sub:" + pl.UUID) {
		log.Printf("copy_sub_server: a copy of %s is already running on this node, ignoring the duplicate", pl.UUID)
		return
	}
	defer commandsInFlight.leave("copy_sub:" + pl.UUID)

	src, err := resolveWithinDir(serverPath, pl.SourceSubServer)
	if err != nil {
		log.Printf("copy_sub_server: %v", err)
		return
	}
	if st, err := os.Stat(src); err != nil || !st.IsDir() {
		log.Printf("copy_sub_server: source %q is not a directory", pl.SourceSubServer)
		return
	}
	dst := filepath.Join(serverPath, pl.TargetSubServer)
	if _, err := os.Lstat(dst); err == nil {
		log.Printf("copy_sub_server: target %q already exists", pl.TargetSubServer)
		return
	}

	statusKey := fmt.Sprintf("dylaris:server:%s:status", pl.UUID)
	releaseBusy := holdBusyStatus(rdb, pl.UUID, "installing", busyStatusTTL)
	defer releaseBusy()

	// A partial copy left behind by a failure is worse than no copy: it would
	// present as a usable sub-server. Build under a temporary name and rename
	// into place only once the whole tree is there.
	staging := filepath.Join(serverPath, "."+pl.TargetSubServer+".copying")
	os.RemoveAll(staging)
	// copyDir, not copyTree: this is a DUPLICATION for a user, so the protected
	// entries (.active_server, .dylaris.json, .node_config.json,
	// .dylaris-backups) must not be carried into the copy. copyTree is the
	// verbatim variant and belongs to a whole-server MOVE. copyDir also already
	// pins the symlink boundary at the copy source, which a copy inside a
	// tenant-writable directory needs.
	if err := copyDir(src, staging); err != nil {
		os.RemoveAll(staging)
		log.Printf("copy_sub_server: copy %s -> %s failed: %v", pl.SourceSubServer, pl.TargetSubServer, err)
		rdb.Set(ctx, statusKey, "stopped", 30*time.Second)
		return
	}
	if err := os.Rename(staging, dst); err != nil {
		os.RemoveAll(staging)
		log.Printf("copy_sub_server: rename into place failed: %v", err)
		rdb.Set(ctx, statusKey, "stopped", 30*time.Second)
		return
	}
	rdb.Set(ctx, statusKey, "stopped", 30*time.Second)
	log.Printf("copy_sub_server: %s/%s copied to %s", pl.UUID, pl.SourceSubServer, pl.TargetSubServer)
}
