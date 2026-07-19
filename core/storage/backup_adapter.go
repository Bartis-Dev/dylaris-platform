package storage

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"path"
	"strings"
	"time"

	"dylaris-core/storage/backup"
)

// CoreStorageBackupAdapter satisfies backup.Storage by delegating to the
// shared Core file storage provider, which the caller has already scoped to
// the "server-backups" sub-prefix. It is a thin adapter, NOT a new backend:
// whatever the Core file storage is configured as (path or s3) is what
// backups land on.
//
// It lives in package `storage` rather than `storage/backup` on purpose:
// `storage` already imports `storage/backup` (see s3provider.go), so the
// reverse import would be a cycle. storage/backup therefore takes this in as
// an already-built backup.Storage via Deps.CoreStorage.
type CoreStorageBackupAdapter struct {
	prov StorageProvider
}

// NewCoreStorageBackupAdapter wraps a scoped Core file storage provider.
func NewCoreStorageBackupAdapter(p StorageProvider) *CoreStorageBackupAdapter {
	return &CoreStorageBackupAdapter{prov: p}
}

// Provider is the textual name persisted in backup_storages.provider.
func (a *CoreStorageBackupAdapter) Provider() string { return "core-storage" }

// Put stores the stream. size is still DROPPED: neither underlying WriteFile
// implementation needs a declared length. ctx is now threaded through.
func (a *CoreStorageBackupAdapter) Put(ctx context.Context, key string, r io.Reader, _ int64) error {
	return a.prov.WriteFile(ctx, key, r)
}

// Get returns a reader for key. S3Provider.GetFile already normalizes a
// genuinely-missing key to an fs.ErrNotExist-comparable error and leaves every
// other backend failure unwrapped; LocalProvider returns os.Open's error,
// which is fs.ErrNotExist-comparable too.
func (a *CoreStorageBackupAdapter) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	return a.prov.GetFile(ctx, key)
}

// Delete removes key. Idempotent per the backup.Storage contract:
// LocalProvider.DeletePath is os.RemoveAll (nil on missing) and
// S3Provider.DeletePath returns nil for a missing key. Verified, no wrapper.
func (a *CoreStorageBackupAdapter) Delete(ctx context.Context, key string) error {
	return a.prov.DeletePath(ctx, key)
}

// List enumerates every object under prefix, recursively, returning FULL keys
// (prefix included) so the result is drop-in comparable with LocalStorage.List
// and S3Storage.List. StorageProvider.ListFiles is one directory level only,
// so the recursion via WalkProvider is mandatory.
//
// LastModified is the ZERO time: neither backend exposes a per-key mtime
// through ListFiles. Verified safe - retention pruning
// (services/backup_scheduler.go) prunes by backup_runs rows and
// run.StorageKey, not by object mtime, and the panel shows
// backup_runs.started_at. Nothing reads Object.LastModified off a List/Stat
// result today. Do not start.
func (a *CoreStorageBackupAdapter) List(ctx context.Context, prefix string) ([]backup.Object, error) {
	files, err := WalkProvider(ctx, a.prov, prefix)
	if err != nil {
		return nil, fmt.Errorf("core-storage backup list %q: %w", prefix, err)
	}
	trimmed := strings.Trim(prefix, "/")
	out := make([]backup.Object, 0, len(files))
	for _, f := range files {
		key := f.Key
		if trimmed != "" {
			key = trimmed + "/" + f.Key
		}
		out = append(out, backup.Object{Key: key, Size: f.Size})
	}
	return out, nil
}

// Stat reports size for key by listing the key's PARENT directory and picking
// out the matching entry.
//
// Chosen over a GetFile probe because ListFiles returns Size on BOTH backends
// without transferring bytes (LocalProvider from os.ReadDir + Info();
// S3Provider from the ListObjectsV2 result), whereas a GetFile probe opens a
// body - on S3 that is a full GetObject, so measuring a 4 GB archive would
// stream or abort 4 GB. Its two accepted costs: LastModified is the zero
// time (see List), and on S3 it costs a prefix List of the parent. Acceptable
// because Stat is not on any hot path - backups are minutes apart at most.
func (a *CoreStorageBackupAdapter) Stat(ctx context.Context, key string) (backup.Object, error) {
	dir := path.Dir(key)
	if dir == "." || dir == "/" {
		dir = ""
	}
	entries, err := a.prov.ListFiles(ctx, dir)
	if err != nil {
		return backup.Object{}, fmt.Errorf("core-storage backup stat %q: %w", key, err)
	}
	base := path.Base(key)
	for _, e := range entries {
		if e.IsDir || e.Name != base {
			continue
		}
		return backup.Object{Key: key, Size: e.Size}, nil
	}
	return backup.Object{}, fmt.Errorf("core-storage backup stat %q: %w", key, fs.ErrNotExist)
}

// DownloadURL passes straight through: ("", nil) for the path backend, a
// presigned GET for s3.
func (a *CoreStorageBackupAdapter) DownloadURL(ctx context.Context, key string, ttl time.Duration) (string, error) {
	return a.prov.DownloadURL(ctx, key, ttl)
}

// UploadURL is not supported. StorageProvider has no presigned-PUT seam at
// all, so a BYON tenant node cannot upload directly to a core-storage backup
// target: it goes through Core, or through the operator-node path. Both
// existing callers already gate on Provider()=="s3" first, so returning an
// error here is unreachable today and fails loudly if a future caller forgets.
func (a *CoreStorageBackupAdapter) UploadURL(_ context.Context, _ string, _ time.Duration) (string, error) {
	return "", backup.ErrUploadURLUnsupported
}
