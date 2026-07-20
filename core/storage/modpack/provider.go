// Package modpack provides at-rest storage for .mrpack files. Implementations
// share the ModpackStorageProvider interface. The active provider is built
// from settings via NewProviderFromSettings, which resolves "local" (mirrored
// across N filesystem paths), "s3" (a dedicated bucket), or "core-storage"
// (the shared Core file storage, under the "modpacks" sub-prefix).
package modpack

import (
	"context"
	"errors"
	"io"
	"time"
)

// ModpackStorageProvider is the at-rest storage layer for .mrpack files.
// All keys are forward-slash-separated, provider-relative
// (e.g. "modpacks/<user-uuid>/<slug>/<version>/pack.mrpack").
//
// How far ctx actually reaches differs per backend, so do not read it as a
// blanket cancellation guarantee. S3Provider hands it to every SDK call, so a
// cancelled ctx aborts the in-flight HTTP request. LocalProvider checks it on
// entry and between mirror paths only: a filesystem syscall already blocked on
// a wedged mount cannot be interrupted from userspace. CoreStorageProvider
// passes it straight to the underlying storage.StorageProvider, which has the
// same per-backend split.
type ModpackStorageProvider interface {
	Put(ctx context.Context, key string, data []byte) error
	Get(ctx context.Context, key string) ([]byte, error)
	Delete(ctx context.Context, key string) error
	Stat(ctx context.Context, key string) (size int64, exists bool, err error)

	// Stream returns the object as a reader plus its size, for callers that
	// hand the bytes straight to a client without inspecting them. The caller
	// MUST close the reader. A missing key returns ErrNotFound.
	//
	// This exists alongside Get rather than replacing it. Get stays for the
	// callers that genuinely need the whole object in memory - zip assembly in
	// packs_mrpack.go, the hash computation in packs_import.go, manifest
	// parsing - and reshaping those was never the point.
	//
	// The point is the paths that do NOT need it. Serving a pack verbatim
	// through Get materialises the entire object in Core's heap, once per
	// concurrent request, and the public mirror route is exactly that shape:
	// a fleet installing one pack across N nodes made Core hold N copies of it
	// at the same time. Streaming makes that a constant.
	//
	// Size is returned because the mirror sets Content-Length before writing,
	// and a stream cannot be measured without consuming it. A backend that
	// cannot determine the size cheaply returns -1, and the caller omits the
	// header rather than buffering to count.
	Stream(ctx context.Context, key string) (rc io.ReadCloser, size int64, err error)

	// DownloadURL returns a direct, time-limited URL to the object, or ("",
	// nil) when the backend cannot presign one. Returning an empty string is
	// NOT an error: it is how the local and mirrored-path backends say "there
	// is no URL, stream it yourself", and every caller has to handle it.
	//
	// Where a URL is available it takes Core out of the data path entirely,
	// which is worth far more than any caching layer in front of it: the
	// bytes never enter the process at all.
	//
	// Deliberately mirrors backup.Storage.DownloadURL, including the ("", nil)
	// convention, so the two storage families behave the same way.
	DownloadURL(ctx context.Context, key string, ttl time.Duration) (string, error)
}

// SizeUnknown is returned by Stream when the backend cannot report a size
// without reading the object. Callers must not send a Content-Length in that
// case; buffering the stream to measure it would defeat streaming entirely.
const SizeUnknown int64 = -1

// ErrNotFound is returned when Get / Stat finds no matching key.
var ErrNotFound = errors.New("modpack storage: key not found")
