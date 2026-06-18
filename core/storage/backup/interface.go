package backup

import (
	"context"
	"io"
	"time"
)

// Object is a single artifact stored in a BackupStorage.
type Object struct {
	Key          string
	Size         int64
	LastModified time.Time
}

// Storage is the interface that any backup backend must satisfy.
// Implementations are kept narrow — the worker streams a single tar.gz per
// run, the panel/handler streams it back out, and the cron job lists keys
// for retention. Anything more elaborate (multipart uploads, checksums,
// encryption) belongs in v2.
type Storage interface {
	// Provider returns the textual provider name, e.g. "local" or "s3".
	Provider() string

	// Put stores the byte stream under `key`. Caller is responsible for
	// closing `r` after Put returns.
	Put(ctx context.Context, key string, r io.Reader, size int64) error

	// Get returns a reader for `key`. Caller MUST close.
	Get(ctx context.Context, key string) (io.ReadCloser, error)

	// Delete removes the object under `key`. Returns nil even when the key
	// did not exist (so callers can be idempotent).
	Delete(ctx context.Context, key string) error

	// List returns all objects under `prefix`. Empty slice when nothing
	// matches (not an error).
	List(ctx context.Context, prefix string) ([]Object, error)

	// Stat returns metadata for `key`. Returns os.ErrNotExist-style error
	// when missing; callers should check for not-found semantics.
	Stat(ctx context.Context, key string) (Object, error)

	// DownloadURL returns a pre-signed GET URL valid for the given duration if
	// the provider supports it. LocalStorage returns ("", nil) — the panel
	// falls back to streaming via Core in that case.
	DownloadURL(ctx context.Context, key string, ttl time.Duration) (string, error)

	// UploadURL returns a pre-signed PUT URL valid for the given duration if the
	// provider supports it (S3/R2). Used to let a BYON tenant node upload a
	// backup WITHOUT ever receiving the bucket credentials. Non-S3 providers
	// return ("", nil).
	UploadURL(ctx context.Context, key string, ttl time.Duration) (string, error)
}
