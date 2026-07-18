package handlers

import (
	"os"
	"path/filepath"
	"testing"

	"dylaris-core/storage"
	"dylaris-core/store"
)

// coreStorageFakeStore returns a per-key canned setting value from an
// internal map. This is distinct from modeFakeStore (permissions_mode_test.go),
// which always returns the same value regardless of the requested key and so
// cannot drive the multi-key CoreStorageConfig shape.
type coreStorageFakeStore struct {
	store.Store
	values map[string]string
}

// GetSetting mirrors the real store's behavior for an unset setting: an empty
// value and a nil error.
func (f *coreStorageFakeStore) GetSetting(key string) (string, error) {
	return f.values[key], nil
}

func TestLoadCoreStorageConfig(t *testing.T) {
	cases := []struct {
		name      string
		values    map[string]string
		want      CoreStorageConfig
		wantValid bool
	}{
		{
			name:      "all keys empty is unconfigured and invalid",
			values:    map[string]string{},
			want:      CoreStorageConfig{},
			wantValid: false,
		},
		{
			name: "valid path config",
			values: map[string]string{
				keyCoreStorageBackend:     "path",
				keyCoreStoragePath:        "/mnt/shared",
				keyCoreStoragePathConfirm: "true",
			},
			want: CoreStorageConfig{
				Backend:       "path",
				Path:          "/mnt/shared",
				PathConfirmed: true,
			},
			wantValid: true,
		},
		{
			name: "valid s3 config",
			values: map[string]string{
				keyCoreStorageBackend:     "s3",
				keyCoreStorageS3Bucket:    "my-bucket",
				keyCoreStorageS3AccessKey: "AKIAEXAMPLE",
				keyCoreStorageS3SecretKey: "s3cr3t",
			},
			want: CoreStorageConfig{
				Backend:     "s3",
				S3Bucket:    "my-bucket",
				S3AccessKey: "AKIAEXAMPLE",
				S3SecretKey: "s3cr3t",
				S3SecretSet: true,
			},
			wantValid: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := &AppState{Store: &coreStorageFakeStore{values: c.values}}
			got := s.LoadCoreStorageConfig()
			if got != c.want {
				t.Fatalf("LoadCoreStorageConfig() = %+v, want %+v", got, c.want)
			}
			if valid := s.CoreStorageConfigured(); valid != c.wantValid {
				t.Fatalf("CoreStorageConfigured() = %v, want %v", valid, c.wantValid)
			}
		})
	}
}

// firstMissingAncestor walks up from target to find the highest-level
// directory in its ancestor chain that does not yet exist on disk. This is
// the exact directory os.MkdirAll would create as a side effect, so a test
// can remove precisely that (and nothing that pre-existed) during cleanup.
// Returns "" if target and all its ancestors already exist.
func firstMissingAncestor(t *testing.T, target string) string {
	t.Helper()
	abs, err := filepath.Abs(target)
	if err != nil {
		t.Fatalf("filepath.Abs(%q): %v", target, err)
	}
	var chain []string
	for cur := abs; ; {
		chain = append(chain, cur)
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}
	for i := len(chain) - 1; i >= 0; i-- {
		if _, err := os.Stat(chain[i]); os.IsNotExist(err) {
			return chain[i]
		}
	}
	return ""
}

var coreStoragePrefixes = []string{
	CoreStoragePrefixLibrary,
	CoreStoragePrefixAttachments,
	CoreStoragePrefixBackups,
}

// TestBuildCoreStorageProvider_UnconfiguredFallsBackToLegacyPath is the core
// regression guard: with no (or an invalid) shared-storage config, existing
// installs must keep browsing/downloading from today's on-disk layout,
// dylaris_data/<subPrefix>, scoped per subsystem.
func TestBuildCoreStorageProvider_UnconfiguredFallsBackToLegacyPath(t *testing.T) {
	baseDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}
	// buildCoreStorageProvider derives baseDir the same way (os.Getwd()) and
	// MkdirAlls dylaris_data/<subPrefix> under it as a side effect; clean up
	// whatever this test freshly creates.
	if root := firstMissingAncestor(t, filepath.Join(baseDir, "dylaris_data")); root != "" {
		t.Cleanup(func() { os.RemoveAll(root) })
	}

	for _, prefix := range coreStoragePrefixes {
		t.Run(prefix, func(t *testing.T) {
			s := &AppState{Store: &coreStorageFakeStore{values: map[string]string{}}}
			p, err := s.buildCoreStorageProvider(prefix)
			if err != nil {
				t.Fatalf("buildCoreStorageProvider(%q): %v", prefix, err)
			}
			lp, ok := p.(*storage.LocalProvider)
			if !ok {
				t.Fatalf("buildCoreStorageProvider(%q) = %T, want *storage.LocalProvider", prefix, p)
			}
			want := filepath.Join(baseDir, "dylaris_data", prefix)
			if lp.BasePath != want {
				t.Fatalf("BasePath = %q, want %q", lp.BasePath, want)
			}
		})
	}
}

// TestBuildCoreStorageProvider_ConfiguredPathBackendScopesBySubPrefix asserts
// that a configured path backend scopes each subsystem under its own
// subdirectory of the operator-configured shared path, rather than all
// subsystems colliding into one root.
func TestBuildCoreStorageProvider_ConfiguredPathBackendScopesBySubPrefix(t *testing.T) {
	const cfgPath = "/mnt/shared"

	// Same MkdirAll side effect as the unconfigured case, rooted at cfgPath
	// instead of dylaris_data; clean up only what this test freshly creates.
	if root := firstMissingAncestor(t, filepath.Join(cfgPath)); root != "" {
		t.Cleanup(func() { os.RemoveAll(root) })
	}

	for _, prefix := range coreStoragePrefixes {
		t.Run(prefix, func(t *testing.T) {
			s := &AppState{Store: &coreStorageFakeStore{values: map[string]string{
				keyCoreStorageBackend:     "path",
				keyCoreStoragePath:        cfgPath,
				keyCoreStoragePathConfirm: "true",
			}}}
			p, err := s.buildCoreStorageProvider(prefix)
			if err != nil {
				t.Fatalf("buildCoreStorageProvider(%q): %v", prefix, err)
			}
			lp, ok := p.(*storage.LocalProvider)
			if !ok {
				t.Fatalf("buildCoreStorageProvider(%q) = %T, want *storage.LocalProvider", prefix, p)
			}
			want := filepath.Join(cfgPath, prefix)
			if lp.BasePath != want {
				t.Fatalf("BasePath = %q, want %q", lp.BasePath, want)
			}
		})
	}
}

// TestBuildCoreStorageProvider_ConfiguredS3BackendBuildsProvider asserts that
// a valid s3 config builds a (non-local) provider without error. Constructing
// the AWS SDK client from static credentials does not touch the network, so
// this must succeed offline.
func TestBuildCoreStorageProvider_ConfiguredS3BackendBuildsProvider(t *testing.T) {
	s := &AppState{Store: &coreStorageFakeStore{values: map[string]string{
		keyCoreStorageBackend:     "s3",
		keyCoreStorageS3Bucket:    "my-bucket",
		keyCoreStorageS3AccessKey: "AKIAEXAMPLE",
		keyCoreStorageS3SecretKey: "s3cr3t",
	}}}

	p, err := s.buildCoreStorageProvider(CoreStoragePrefixLibrary)
	if err != nil {
		t.Fatalf("buildCoreStorageProvider(s3): %v", err)
	}
	if p == nil {
		t.Fatal("buildCoreStorageProvider(s3) returned a nil provider")
	}
	if _, ok := p.(*storage.LocalProvider); ok {
		t.Fatal("buildCoreStorageProvider(s3) returned a *storage.LocalProvider, want an s3 provider")
	}
}
