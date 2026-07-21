package handlers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"

	"dylaris-core/models"
	backupstorage "dylaris-core/storage/backup"
)

// backupStorageProbePayload is the fixed content the round-trip probe writes and
// reads back. Reading it back and comparing is what turns "writable" into
// "reachable and consistent": a backend that accepts a write but hands back
// different or no bytes is broken, and the old put-then-delete probe reported it
// as green.
const backupStorageProbePayload = "dylaris-backup-probe"

// probeBackupStorage writes, reads back and deletes a uniquely-named probe
// object to verify a backup backend is reachable AND read/write-consistent, not
// merely writable. It deletes the probe on every return path - write error, read
// error, mismatch - so a broken candidate never leaves a stray object behind.
// Mirrors probeStorageProvider for core storage.
func probeBackupStorage(ctx context.Context, provider backupstorage.Storage) (ok bool, message string) {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	key := "__dylaris_probe_" + hex.EncodeToString(b) + ".txt"

	if err := provider.Put(ctx, key, strReader(backupStorageProbePayload), int64(len(backupStorageProbePayload))); err != nil {
		_ = provider.Delete(ctx, key)
		return false, "put failed: " + err.Error()
	}
	rc, err := provider.Get(ctx, key)
	if err != nil {
		_ = provider.Delete(ctx, key)
		return false, "read-back failed: " + err.Error()
	}
	got, readErr := io.ReadAll(rc)
	rc.Close()
	if readErr != nil {
		_ = provider.Delete(ctx, key)
		return false, "read-back failed: " + readErr.Error()
	}
	if string(got) != backupStorageProbePayload {
		_ = provider.Delete(ctx, key)
		return false, "read-back mismatch: storage backend is not consistent"
	}
	if err := provider.Delete(ctx, key); err != nil {
		return false, "cleanup failed: " + err.Error()
	}
	return true, "Storage reachable: write, read and delete all succeeded."
}

// backupStorageEphemeralWarning returns a non-empty warning when a local/shared
// backup path sits on the container's own filesystem instead of a mounted
// volume, meaning every archive written there is LOST on the next container
// recreation. It mirrors the core-storage ephemeral-path warning and goes
// through the same pathOnContainerRootFS seam. Any other provider, an
// unparseable config, or a path the check cannot judge (a non-Linux host)
// returns "". A silently-ephemeral backup target is worse than an ephemeral core
// path: the operator believes backups exist when they do not.
func backupStorageEphemeralWarning(s *models.BackupStorage) string {
	if s.Provider != "local" && s.Provider != "shared" {
		return ""
	}
	var cfg backupstorage.LocalConfig
	if err := json.Unmarshal(s.Config, &cfg); err != nil || cfg.BasePath == "" {
		return ""
	}
	if onRoot, determinable := pathOnContainerRootFS(cfg.BasePath); determinable && onRoot {
		return "This backup path is on the container's own filesystem, not a mounted volume, so every archive written here is LOST when the container is recreated. The read/write test above still passes because that directory is genuinely writable. Add a bind mount or volume for this path in your compose/stack file, or point it at a directory inside a volume that is already mounted."
	}
	return ""
}
