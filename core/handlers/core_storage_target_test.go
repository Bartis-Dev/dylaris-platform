package handlers

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dylaris-core/services"
	"dylaris-core/store"
)

// coreStorageTargetFakeStore is a settings-only store for this file. Local
// here; never redeclared elsewhere.
type coreStorageTargetFakeStore struct {
	store.Store
	kv map[string]string
}

func newCoreStorageTargetFakeStore() *coreStorageTargetFakeStore {
	return &coreStorageTargetFakeStore{kv: map[string]string{}}
}

func (f *coreStorageTargetFakeStore) GetSetting(k string) (string, error) { return f.kv[k], nil }
func (f *coreStorageTargetFakeStore) SetSetting(k, v string) error        { f.kv[k] = v; return nil }

func TestCoreStorageConfigFromTarget_MapsEveryFieldAndTrims(t *testing.T) {
	got := coreStorageConfigFromTarget(services.StorageTargetConfig{
		Backend: " s3 ", Path: " /mnt/new ", PathConfirmed: true,
		S3Endpoint: " https://new.example.com ", S3Bucket: " dylaris-new ",
		S3Region: "eu-central-1", S3AccessKey: " AKIA ", S3SecretKey: " sec ",
		S3PathStyle: true, S3Prefix: "prod",
	})
	want := CoreStorageConfig{
		Backend: "s3", Path: "/mnt/new", PathConfirmed: true,
		S3Endpoint: "https://new.example.com", S3Bucket: "dylaris-new",
		S3Region: "eu-central-1", S3AccessKey: "AKIA", S3SecretKey: "sec",
		S3PathStyle: true, S3Prefix: "prod",
	}
	// S3SecretSet is derived, not carried on the wire.
	want.S3SecretSet = false
	got.S3SecretSet = false
	if got != want {
		t.Fatalf("coreStorageConfigFromTarget = %+v, want %+v", got, want)
	}
}

func TestBuildTargetStorageProvider_RejectsAnInvalidConfigBeforeAnythingElse(t *testing.T) {
	cases := []struct {
		name string
		cfg  CoreStorageConfig
	}{
		{"s3 without a bucket", CoreStorageConfig{Backend: "s3", S3AccessKey: "k", S3SecretKey: "s"}},
		{"s3 without credentials", CoreStorageConfig{Backend: "s3", S3Bucket: "b"}},
		{"path that is relative", CoreStorageConfig{Backend: "path", Path: "relative/dir", PathConfirmed: true}},
		{"path that is not confirmed", CoreStorageConfig{Backend: "path", Path: "/mnt/new"}},
		{"unknown backend", CoreStorageConfig{Backend: "webdav", Path: "/mnt/new"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := buildTargetStorageProvider(c.cfg, "library"); err == nil {
				t.Fatal("buildTargetStorageProvider err = nil, want the config rejected before a provider is built")
			}
		})
	}
}

func TestBuildTargetStorageProvider_BuildsAWorkingUnsavedPathProvider(t *testing.T) {
	// The whole point of the ad-hoc target: a config that was never saved
	// still yields a usable provider, exactly as TestConnection already does.
	//
	// testConnectionProbeDir (core_storage_http_test.go, same package) is used
	// instead of a raw t.TempDir(): buildTargetStorageProvider runs
	// validateCoreStorageConfig first, which enforces a deliberately
	// Linux-only absolute-path check ("/...") that a real Windows temp dir
	// (e.g. "C:\Users\...") fails, for the exact reason documented on that
	// helper.
	root := testConnectionProbeDir(t)
	prov, err := buildTargetStorageProvider(
		CoreStorageConfig{Backend: "path", Path: root, PathConfirmed: true}, "library")
	if err != nil {
		t.Fatalf("buildTargetStorageProvider: %v", err)
	}
	if err := prov.WriteFile("a.jar", strings.NewReader("hello")); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	rc, err := prov.GetFile("a.jar")
	if err != nil {
		t.Fatalf("GetFile: %v", err)
	}
	b, _ := io.ReadAll(rc)
	rc.Close()
	if string(b) != "hello" {
		t.Errorf("read back %q, want %q", b, "hello")
	}
	// It landed under the data set's sub-prefix, not at the bare root.
	if _, err := os.Stat(filepath.Join(root, "library", "a.jar")); err != nil {
		t.Errorf("object not found under the library sub-prefix: %v", err)
	}
}

func TestEffectiveS3Prefix(t *testing.T) {
	cases := []struct {
		name   string
		prefix string
		sub    string
		want   string
	}{
		{"no configured prefix", "", "library", "library"},
		{"configured prefix", "prod", "library", "prod/library"},
		{"prefix with stray slashes", "/prod/", "library", "prod/library"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := effectiveS3Prefix(CoreStorageConfig{Backend: "s3", S3Prefix: c.prefix}, c.sub)
			if got != c.want {
				t.Fatalf("effectiveS3Prefix = %q, want %q", got, c.want)
			}
		})
	}
}

func TestEnsureDistinctCoreStorageLocation_S3(t *testing.T) {
	src := CoreStorageConfig{
		Backend: "s3", S3Endpoint: "https://s3.example.com", S3Bucket: "dylaris",
		S3Prefix: "prod", S3AccessKey: "k", S3SecretKey: "s",
	}
	cases := []struct {
		name    string
		tgt     CoreStorageConfig
		wantErr bool
	}{
		{"identical endpoint + bucket + prefix", src, true},
		{
			"same triple written with stray slashes",
			CoreStorageConfig{Backend: "s3", S3Endpoint: "https://s3.example.com/", S3Bucket: "dylaris", S3Prefix: "/prod/", S3AccessKey: "k", S3SecretKey: "s"},
			true,
		},
		{
			// Different credentials pointing at the SAME bucket and prefix is
			// still the same location. Credentials are not identity.
			"same triple, different credentials",
			CoreStorageConfig{Backend: "s3", S3Endpoint: "https://s3.example.com", S3Bucket: "dylaris", S3Prefix: "prod", S3AccessKey: "other", S3SecretKey: "other"},
			true,
		},
		{
			"same bucket, different prefix is a legitimate target",
			CoreStorageConfig{Backend: "s3", S3Endpoint: "https://s3.example.com", S3Bucket: "dylaris", S3Prefix: "archive", S3AccessKey: "k", S3SecretKey: "s"},
			false,
		},
		{
			"different bucket",
			CoreStorageConfig{Backend: "s3", S3Endpoint: "https://s3.example.com", S3Bucket: "dylaris-new", S3Prefix: "prod", S3AccessKey: "k", S3SecretKey: "s"},
			false,
		},
		{
			"different endpoint",
			CoreStorageConfig{Backend: "s3", S3Endpoint: "https://new.example.com", S3Bucket: "dylaris", S3Prefix: "prod", S3AccessKey: "k", S3SecretKey: "s"},
			false,
		},
		{
			"a path target is never the same location as an s3 source",
			CoreStorageConfig{Backend: "path", Path: "/mnt/new", PathConfirmed: true},
			false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ensureDistinctCoreStorageLocation(src, c.tgt, "library")
			if (err != nil) != c.wantErr {
				t.Fatalf("ensureDistinctCoreStorageLocation err = %v, wantErr %v", err, c.wantErr)
			}
			if c.wantErr && !errors.Is(err, ErrTargetSameLocation) {
				t.Errorf("err = %v, want it to wrap ErrTargetSameLocation", err)
			}
		})
	}
}

func TestEnsureDistinctCoreStorageLocation_PathIdenticalString(t *testing.T) {
	root := t.TempDir()
	cfg := CoreStorageConfig{Backend: "path", Path: root, PathConfirmed: true}
	if err := ensureDistinctCoreStorageLocation(cfg, cfg, "library"); !errors.Is(err, ErrTargetSameLocation) {
		t.Fatalf("err = %v, want ErrTargetSameLocation", err)
	}
}

func TestEnsureDistinctCoreStorageLocation_PathViaSymlinkIsStillTheSameLocation(t *testing.T) {
	// THE load-bearing case. A string compare passes this and the copy loop
	// then rewrites every source object onto itself. Only device+inode
	// (os.SameFile) catches it.
	base := t.TempDir()
	realDir := filepath.Join(base, "real")
	if err := os.MkdirAll(filepath.Join(realDir, "library"), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	linkDir := filepath.Join(base, "link")
	if err := os.Symlink(realDir, linkDir); err != nil {
		// Unprivileged symlink creation fails on Windows; CI runs Linux and
		// exercises this for real. See plan-author note 13.
		t.Skipf("symlinks unavailable on this host: %v", err)
	}

	src := CoreStorageConfig{Backend: "path", Path: realDir, PathConfirmed: true}
	tgt := CoreStorageConfig{Backend: "path", Path: linkDir, PathConfirmed: true}
	err := ensureDistinctCoreStorageLocation(src, tgt, "library")
	if !errors.Is(err, ErrTargetSameLocation) {
		t.Fatalf("err = %v, want ErrTargetSameLocation: %q and %q are the same directory through a symlink, and a string compare would have accepted this target", err, realDir, linkDir)
	}
}

func TestEnsureDistinctCoreStorageLocation_DifferentPathsAreAccepted(t *testing.T) {
	src := CoreStorageConfig{Backend: "path", Path: t.TempDir(), PathConfirmed: true}
	tgt := CoreStorageConfig{Backend: "path", Path: t.TempDir(), PathConfirmed: true}
	if err := ensureDistinctCoreStorageLocation(src, tgt, "library"); err != nil {
		t.Fatalf("err = %v, want two distinct temp dirs accepted", err)
	}
}

func TestEnsureDistinctCoreStorageLocation_AMissingTargetDirIsNotTheSameLocation(t *testing.T) {
	// A target that does not exist yet cannot alias the source. It must be
	// accepted rather than error out, otherwise a first migration onto a fresh
	// mount is impossible.
	src := CoreStorageConfig{Backend: "path", Path: t.TempDir(), PathConfirmed: true}
	tgt := CoreStorageConfig{Backend: "path", Path: filepath.Join(t.TempDir(), "does-not-exist-yet"), PathConfirmed: true}
	if err := ensureDistinctCoreStorageLocation(src, tgt, "library"); err != nil {
		t.Fatalf("err = %v, want a not-yet-created target accepted", err)
	}
}

func TestPersistCoreStorageConfig_WritesEveryKeyAndRespectsTheSecretRule(t *testing.T) {
	fs := newCoreStorageTargetFakeStore()
	fs.kv[keyCoreStorageS3SecretKey] = "OLD-SECRET"
	st := &AppState{Store: fs}

	cfg := CoreStorageConfig{
		Backend: "s3", S3Endpoint: "https://new.example.com", S3Bucket: "dylaris-new",
		S3Region: "eu-central-1", S3AccessKey: "AKIA-NEW", S3SecretKey: "NEW-SECRET",
		S3PathStyle: true, S3Prefix: "prod",
	}
	if err := st.persistCoreStorageConfig(cfg, true); err != nil {
		t.Fatalf("persistCoreStorageConfig: %v", err)
	}
	for k, want := range map[string]string{
		keyCoreStorageBackend:     "s3",
		keyCoreStorageS3Endpoint:  "https://new.example.com",
		keyCoreStorageS3Bucket:    "dylaris-new",
		keyCoreStorageS3Region:    "eu-central-1",
		keyCoreStorageS3AccessKey: "AKIA-NEW",
		keyCoreStorageS3SecretKey: "NEW-SECRET",
		keyCoreStorageS3Prefix:    "prod",
		keyCoreStorageS3PathStyle: "true",
	} {
		if fs.kv[k] != want {
			t.Errorf("setting %s = %q, want %q", k, fs.kv[k], want)
		}
	}

	// writeSecret=false must leave the stored secret alone on an s3 config.
	fs.kv[keyCoreStorageS3SecretKey] = "KEEP-ME"
	if err := st.persistCoreStorageConfig(cfg, false); err != nil {
		t.Fatalf("persistCoreStorageConfig: %v", err)
	}
	if fs.kv[keyCoreStorageS3SecretKey] != "KEEP-ME" {
		t.Errorf("stored secret = %q, want it untouched when writeSecret is false", fs.kv[keyCoreStorageS3SecretKey])
	}

	// Switching away from s3 clears the orphaned secret, matching SaveConfig.
	if err := st.persistCoreStorageConfig(CoreStorageConfig{Backend: "path", Path: "/mnt/new", PathConfirmed: true}, false); err != nil {
		t.Fatalf("persistCoreStorageConfig: %v", err)
	}
	if fs.kv[keyCoreStorageS3SecretKey] != "" {
		t.Errorf("stored secret = %q, want it cleared when the backend is no longer s3", fs.kv[keyCoreStorageS3SecretKey])
	}
}

func TestPersistCoreStorageConfig_RoundTripsThroughLoadCoreStorageConfig(t *testing.T) {
	// The written keys must be exactly the ones LoadCoreStorageConfig reads,
	// so a switching_config phase leaves a config the rest of Core can use.
	fs := newCoreStorageTargetFakeStore()
	st := &AppState{Store: fs}
	cfg := CoreStorageConfig{
		Backend: "s3", S3Endpoint: "https://new.example.com", S3Bucket: "dylaris-new",
		S3Region: "eu-central-1", S3AccessKey: "AKIA-NEW", S3SecretKey: "NEW-SECRET",
		S3PathStyle: true, S3Prefix: "prod",
	}
	if err := st.persistCoreStorageConfig(cfg, true); err != nil {
		t.Fatalf("persistCoreStorageConfig: %v", err)
	}
	got := st.LoadCoreStorageConfig()
	if err := validateCoreStorageConfig(got); err != nil {
		t.Fatalf("the persisted config does not validate: %v", err)
	}
	if got.S3Bucket != cfg.S3Bucket || got.S3Prefix != cfg.S3Prefix || got.S3AccessKey != cfg.S3AccessKey {
		t.Errorf("round trip = %+v, want the written config", got)
	}
	if !got.S3SecretSet {
		t.Error("S3SecretSet = false after writing a secret")
	}
}
