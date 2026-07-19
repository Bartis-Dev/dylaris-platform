// Package modpack provides at-rest storage for .mrpack files. Implementations
// share the ModpackStorageProvider interface. The active provider is built
// from settings via NewProviderFromSettings, which resolves "local" (mirrored
// across N filesystem paths), "s3" (a dedicated bucket), or "core-storage"
// (the shared Core file storage, under the "modpacks" sub-prefix).
package modpack

import (
	"context"
	"errors"
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
}

// ErrNotFound is returned when Get / Stat finds no matching key.
var ErrNotFound = errors.New("modpack storage: key not found")
