package modpack

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"

	"dylaris-core/storage"
)

// CoreStorageSubPrefix is the namespace modpack objects occupy inside the ONE
// configured Core file storage. It duplicates the canonical
// handlers.CoreStoragePrefixModpacks because this package must not import
// handlers (handlers already imports this package). The two are held together
// by handlers.TestCoreStorageSubPrefixesMatch.
const CoreStorageSubPrefix = "modpacks"

// CoreStorageProvider satisfies ModpackStorageProvider by delegating to the
// shared Core file storage provider, scoped to CoreStorageSubPrefix. A thin
// adapter, not a new backend: whatever the Core file storage is configured as
// (path or s3) is what .mrpack objects land on.
//
// It takes a storage.StorageProvider directly, unlike the backup side which
// takes an already-adapted backup.Storage. That asymmetry is not an
// oversight: package `storage` already imports `storage/backup`, so
// storage/backup cannot import `storage` back, whereas this package has no
// such constraint.
//
// On buffering: the []byte contract is fully satisfiable with no regression.
// The existing S3 modpack provider already does io.ReadAll on Get
// (s3.go:81) and bytes.NewReader on Put (s3.go:59), so this adapter has a
// byte-for-byte identical memory profile. Reshaping ModpackStorageProvider to
// stream would touch packs_mrpack.go's zip assembly and the SHA1/MD5
// computation in packs_import.go, and is explicitly out of scope here.
//
// On context: every method below passes context.Background() to the underlying
// StorageProvider, because ModpackStorageProvider has no ctx parameter. The
// consequence is real, not cosmetic: a modpack read or write against the shared
// Core storage cannot be cancelled or deadline-bound, so a hung S3 call or a
// wedged mount blocks the caller for as long as the backend takes. This is a
// known gap. Closing it means threading ctx through ModpackStorageProvider and
// its callers, which is a separate change.
type CoreStorageProvider struct {
	prov storage.StorageProvider
}

// NewCoreStorageProvider wraps a scoped Core file storage provider.
func NewCoreStorageProvider(p storage.StorageProvider) *CoreStorageProvider {
	return &CoreStorageProvider{prov: p}
}

func (p *CoreStorageProvider) Put(key string, data []byte) error {
	if err := p.prov.WriteFile(context.Background(), key, bytes.NewReader(data)); err != nil {
		return fmt.Errorf("modpack storage: core-storage put %s: %w", key, err)
	}
	return nil
}

// Get reads the whole object. A genuinely missing key is translated to
// ErrNotFound, because every caller in handlers/packs_*.go branches on
// ErrNotFound and would otherwise treat a 404 as a 500.
func (p *CoreStorageProvider) Get(key string) ([]byte, error) {
	rc, err := p.prov.GetFile(context.Background(), key)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("modpack storage: core-storage get %s: %w", key, err)
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		return nil, fmt.Errorf("modpack storage: core-storage read %s: %w", key, err)
	}
	return data, nil
}

// Delete is idempotent: LocalProvider.DeletePath is os.RemoveAll (nil on
// missing) and S3Provider.DeletePath returns nil for a missing key.
func (p *CoreStorageProvider) Delete(key string) error {
	if err := p.prov.DeletePath(context.Background(), key); err != nil {
		return fmt.Errorf("modpack storage: core-storage delete %s: %w", key, err)
	}
	return nil
}

// Stat reports (size, exists, err) by listing the key's PARENT directory,
// exactly like the backup adapter. StorageProvider has no Stat, and a GetFile
// probe would issue a full GetObject on S3 just to learn a size.
func (p *CoreStorageProvider) Stat(key string) (int64, bool, error) {
	dir := path.Dir(key)
	if dir == "." || dir == "/" {
		dir = ""
	}
	entries, err := p.prov.ListFiles(context.Background(), dir)
	if err != nil {
		return 0, false, fmt.Errorf("modpack storage: core-storage stat %s: %w", key, err)
	}
	base := path.Base(key)
	for _, e := range entries {
		if e.IsDir || e.Name != base {
			continue
		}
		return e.Size, true, nil
	}
	return 0, false, nil
}
