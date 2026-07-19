package modpack

import (
	"errors"
	"strings"
	"testing"

	"dylaris-core/storage"
)

func newModpackAdapterOnTempDir(t *testing.T) *CoreStorageProvider {
	t.Helper()
	return NewCoreStorageProvider(&storage.LocalProvider{BasePath: t.TempDir()})
}

func TestModpackCoreStorageProvider_SatisfiesInterface(t *testing.T) {
	var _ ModpackStorageProvider = NewCoreStorageProvider(&storage.LocalProvider{BasePath: t.TempDir()})
}

func TestModpackCoreStorageProvider_PutGetStatDeleteRoundTrip(t *testing.T) {
	p := newModpackAdapterOnTempDir(t)
	key := "user-uuid/my-pack/1.0.0/pack.mrpack"

	if err := p.Put(key, []byte("mrpack-bytes")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := p.Get(key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(got) != "mrpack-bytes" {
		t.Errorf("Get = %q, want mrpack-bytes", got)
	}

	size, exists, err := p.Stat(key)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if !exists {
		t.Fatal("Stat exists = false, want true")
	}
	if size != 12 {
		t.Errorf("Stat size = %d, want 12", size)
	}

	if err := p.Delete(key); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, exists, _ := p.Stat(key); exists {
		t.Error("Stat exists = true after Delete")
	}
}

func TestModpackCoreStorageProvider_GetMissingReturnsErrNotFound(t *testing.T) {
	// The rest of the modpack code branches on modpack.ErrNotFound, NOT on a
	// raw fs.ErrNotExist, so the adapter must translate.
	p := newModpackAdapterOnTempDir(t)
	_, err := p.Get("nope/pack.mrpack")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get(missing) err = %v, want ErrNotFound", err)
	}
}

func TestModpackCoreStorageProvider_StatMissingIsNotAnError(t *testing.T) {
	// Mirrors LocalProvider/S3Provider modpack semantics: (0, false, nil).
	p := newModpackAdapterOnTempDir(t)
	size, exists, err := p.Stat("nope/pack.mrpack")
	if err != nil {
		t.Fatalf("Stat(missing) err = %v, want nil", err)
	}
	if exists || size != 0 {
		t.Errorf("Stat(missing) = (%d, %v), want (0, false)", size, exists)
	}
}

func TestModpackCoreStorageProvider_DeleteIsIdempotent(t *testing.T) {
	p := newModpackAdapterOnTempDir(t)
	if err := p.Delete("never/existed.mrpack"); err != nil {
		t.Fatalf("Delete(missing) = %v, want nil", err)
	}
}

func TestModpackCoreStorageProvider_StatOnTopLevelKey(t *testing.T) {
	p := newModpackAdapterOnTempDir(t)
	if err := p.Put("pack.mrpack", []byte("abc")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	size, exists, err := p.Stat("pack.mrpack")
	if err != nil || !exists || size != 3 {
		t.Fatalf("Stat(top-level) = (%d, %v, %v), want (3, true, nil)", size, exists, err)
	}
}

func TestNewProviderFromSettings_CoreStorageCase(t *testing.T) {
	var gotPrefix string
	build := func(subPrefix string) (storage.StorageProvider, error) {
		gotPrefix = subPrefix
		return &storage.LocalProvider{BasePath: t.TempDir()}, nil
	}
	get := func(k string) (string, error) {
		if k == "modpack_storage_provider" {
			return "core-storage", nil
		}
		return "", nil
	}

	prov, err := NewProviderFromSettings(get, build)
	if err != nil {
		t.Fatalf("NewProviderFromSettings err = %v, want nil", err)
	}
	if _, ok := prov.(*CoreStorageProvider); !ok {
		t.Fatalf("NewProviderFromSettings = %T, want *CoreStorageProvider", prov)
	}
	if gotPrefix != CoreStorageSubPrefix {
		t.Errorf("builder called with %q, want %q", gotPrefix, CoreStorageSubPrefix)
	}
	if CoreStorageSubPrefix != "modpacks" {
		t.Errorf("CoreStorageSubPrefix = %q, want \"modpacks\"", CoreStorageSubPrefix)
	}
}

func TestNewProviderFromSettings_CoreStorageWithoutBuilderFails(t *testing.T) {
	get := func(k string) (string, error) {
		if k == "modpack_storage_provider" {
			return "core-storage", nil
		}
		return "", nil
	}
	prov, err := NewProviderFromSettings(get, nil)
	if err == nil {
		t.Fatal("NewProviderFromSettings(core-storage, nil builder) err = nil, want an error")
	}
	if prov != nil {
		t.Errorf("returned a non-nil provider (%T) alongside the error", prov)
	}
}

func TestNewProviderFromSettings_CoreStorageBuilderErrorPropagates(t *testing.T) {
	boom := errors.New("core storage: s3 bucket is required")
	get := func(k string) (string, error) {
		if k == "modpack_storage_provider" {
			return "core-storage", nil
		}
		return "", nil
	}
	_, err := NewProviderFromSettings(get, func(string) (storage.StorageProvider, error) {
		return nil, boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want it to wrap %v", err, boom)
	}
}

func TestNewProviderFromSettings_ExistingCasesUnchanged(t *testing.T) {
	cases := []struct {
		name     string
		settings map[string]string
		wantNil  bool
		wantErr  bool
	}{
		{"empty provider with no paths is unconfigured", map[string]string{}, true, false},
		{"local with no paths is unconfigured", map[string]string{"modpack_storage_provider": "local"}, true, false},
		{"s3 with no bucket is unconfigured", map[string]string{"modpack_storage_provider": "s3"}, true, false},
		{"unknown provider errors", map[string]string{"modpack_storage_provider": "ftp"}, true, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// build closes over this subtest's own *testing.T (not the parent),
			// so a call here reports via t.Fatal on the goroutine that owns it.
			build := func(string) (storage.StorageProvider, error) {
				t.Fatal("buildCore must not be called for a non core-storage provider")
				return nil, nil
			}
			get := func(k string) (string, error) { return c.settings[k], nil }
			prov, err := NewProviderFromSettings(get, build)
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, c.wantErr)
			}
			if (prov == nil) != c.wantNil {
				t.Fatalf("provider = %T, wantNil %v", prov, c.wantNil)
			}
		})
	}
}

func TestNewProviderFromSettings_LocalStillResolves(t *testing.T) {
	dir := t.TempDir()
	get := func(k string) (string, error) {
		switch k {
		case "modpack_storage_provider":
			return "local", nil
		case "modpack_storage_paths":
			return `["` + strings.ReplaceAll(dir, `\`, `\\`) + `"]`, nil
		}
		return "", nil
	}
	prov, err := NewProviderFromSettings(get, nil)
	if err != nil {
		t.Fatalf("NewProviderFromSettings err = %v", err)
	}
	lp, ok := prov.(*LocalProvider)
	if !ok {
		t.Fatalf("provider = %T, want *LocalProvider", prov)
	}
	if len(lp.Paths) != 1 {
		t.Fatalf("LocalProvider.Paths = %v, want one path (mirroring is unchanged)", lp.Paths)
	}
}
