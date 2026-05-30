// Package modpack provides at-rest storage for .mrpack files. Implementations
// share the ModpackStorageProvider interface. The active provider is built
// from settings via NewProviderFromSettings.
package modpack

import "errors"

// ModpackStorageProvider is the at-rest storage layer for .mrpack files.
// All keys are forward-slash-separated, provider-relative
// (e.g. "modpacks/<user-uuid>/<slug>/<version>/pack.mrpack").
type ModpackStorageProvider interface {
	Put(key string, data []byte) error
	Get(key string) ([]byte, error)
	Delete(key string) error
	Stat(key string) (size int64, exists bool, err error)
}

// ErrNotFound is returned when Get / Stat finds no matching key.
var ErrNotFound = errors.New("modpack storage: key not found")
