package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dylaris-core/storage"
)

// --- migrateLocalDirToProvider (pure-ish walk+copy helper) ---

func TestMigrateLocalDirToProvider(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "a.jar"), []byte("aaa"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(src, "mods"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "mods", "b.jar"), []byte("bb"), 0644); err != nil {
		t.Fatal(err)
	}

	dst := newMemProvider() // from ticket_backup_provider_test.go (same package)
	res, err := migrateLocalDirToProvider(src, dst)
	if err != nil {
		t.Fatalf("migrateLocalDirToProvider: %v", err)
	}
	if res.Copied != 2 || res.Skipped != 0 || res.Failed != 0 {
		t.Fatalf("result = %+v, want Copied=2 Skipped=0 Failed=0", res)
	}
	if string(dst.m["a.jar"]) != "aaa" {
		t.Errorf("a.jar = %q, want aaa", dst.m["a.jar"])
	}
	if string(dst.m["mods/b.jar"]) != "bb" {
		t.Errorf("mods/b.jar = %q (keys=%v), want bb", dst.m["mods/b.jar"], dst.m)
	}

	// Originals must remain, byte-identical (rule 1: never touch the source).
	gotA, err := os.ReadFile(filepath.Join(src, "a.jar"))
	if err != nil || string(gotA) != "aaa" {
		t.Errorf("original a.jar = %q, err=%v, want aaa untouched", gotA, err)
	}
	gotB, err := os.ReadFile(filepath.Join(src, "mods", "b.jar"))
	if err != nil || string(gotB) != "bb" {
		t.Errorf("original mods/b.jar = %q, err=%v, want bb untouched", gotB, err)
	}
}

func TestMigrateLocalDirToProvider_MissingSourceIsNoop(t *testing.T) {
	dst := newMemProvider()
	res, err := migrateLocalDirToProvider(filepath.Join(t.TempDir(), "does-not-exist"), dst)
	if err != nil {
		t.Fatalf("missing source should be a no-op, got err %v", err)
	}
	if res.Copied != 0 || res.Skipped != 0 || res.Failed != 0 {
		t.Errorf("result = %+v, want all-zero for a missing source dir", res)
	}
}

func TestMigrateLocalDirToProvider_EmptyDirIsNoop(t *testing.T) {
	src := t.TempDir() // exists, but empty
	dst := newMemProvider()
	res, err := migrateLocalDirToProvider(src, dst)
	if err != nil {
		t.Fatalf("empty source dir: %v", err)
	}
	if res.Copied != 0 || res.Skipped != 0 || res.Failed != 0 {
		t.Errorf("result = %+v, want all-zero for an empty source dir", res)
	}
}

// TestMigrateLocalDirToProvider_Idempotent_SecondRunSkips is the overwrite-
// policy contract test: SKIP-IF-EXISTS means a second run against the same
// destination reports every file as skipped, copies nothing again, and never
// produces a duplicated-with-suffix copy (the dst map still has exactly the
// original 2 keys).
func TestMigrateLocalDirToProvider_Idempotent_SecondRunSkips(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "a.jar"), []byte("aaa"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(src, "mods"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "mods", "b.jar"), []byte("bb"), 0644); err != nil {
		t.Fatal(err)
	}
	dst := newMemProvider()

	first, err := migrateLocalDirToProvider(src, dst)
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	if first.Copied != 2 {
		t.Fatalf("first run copied = %d, want 2", first.Copied)
	}

	second, err := migrateLocalDirToProvider(src, dst)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if second.Copied != 0 || second.Skipped != 2 || second.Failed != 0 {
		t.Fatalf("second run = %+v, want Copied=0 Skipped=2 Failed=0 (re-run must skip already-migrated files)", second)
	}
	if len(dst.m) != 2 {
		t.Fatalf("dst has %d keys after two runs, want 2 (no duplicated-with-suffix copies): %v", len(dst.m), dst.m)
	}
	if string(dst.m["a.jar"]) != "aaa" || string(dst.m["mods/b.jar"]) != "bb" {
		t.Errorf("dst content changed across runs: %v", dst.m)
	}
}

// TestMigrateLocalDirToProvider_SameLocationRefused guards rule 6: a
// destination that resolves to the exact same directory as the source must
// be refused outright, never attempted (LocalProvider.WriteFile truncates
// the destination path, which here IS the source file).
func TestMigrateLocalDirToProvider_SameLocationRefused(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.jar"), []byte("aaa"), 0644); err != nil {
		t.Fatal(err)
	}
	prov := &storage.LocalProvider{BasePath: dir}

	res, err := migrateLocalDirToProvider(dir, prov)
	if err == nil {
		t.Fatal("expected an error when source and destination are the same directory")
	}
	if res.Copied != 0 || res.Failed != 1 {
		t.Fatalf("result = %+v, want Copied=0 Failed=1", res)
	}
	got, readErr := os.ReadFile(filepath.Join(dir, "a.jar"))
	if readErr != nil || string(got) != "aaa" {
		t.Fatalf("a.jar = %q, err=%v, want untouched aaa (same-location guard must refuse before writing anything)", got, readErr)
	}
}

// migrateFailOnceProvider wraps memProvider (ticket_backup_provider_test.go)
// so WriteFile can be made to fail for specific keys, simulating a
// destination that rejects one particular file (e.g. a transient backend
// error) without needing a real broken backend. Distinct from
// fakeProbeProvider (core_storage_http_test.go: a single-object write/read/
// delete probe, not a multi-file walk).
type migrateFailOnceProvider struct {
	*memProvider
	failKeys map[string]bool
}

func (p *migrateFailOnceProvider) WriteFile(key string, r io.Reader) error {
	if p.failKeys[key] {
		return fmt.Errorf("simulated write failure for %s", key)
	}
	return p.memProvider.WriteFile(key, r)
}

// TestMigrateLocalDirToProvider_PartialFailureContinuesAndReports guards the
// "collect and keep going" rule: one file's write failure must not abort the
// rest of the walk, and must be reported (count + message), not swallowed.
func TestMigrateLocalDirToProvider_PartialFailureContinuesAndReports(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "a.jar"), []byte("aaa"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "b.jar"), []byte("bbb"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "c.jar"), []byte("ccc"), 0644); err != nil {
		t.Fatal(err)
	}
	dst := &migrateFailOnceProvider{memProvider: newMemProvider(), failKeys: map[string]bool{"b.jar": true}}

	res, err := migrateLocalDirToProvider(src, dst)
	if err == nil {
		t.Fatal("expected a non-nil error when one file fails to write")
	}
	if res.Copied != 2 {
		t.Errorf("copied = %d, want 2 (the two files that did not fail)", res.Copied)
	}
	if res.Failed != 1 {
		t.Fatalf("failed = %d, want 1", res.Failed)
	}
	if len(res.Errors) != 1 || !strings.Contains(res.Errors[0], "b.jar") {
		t.Errorf("errors = %v, want exactly one mentioning b.jar", res.Errors)
	}
	if string(dst.m["a.jar"]) != "aaa" || string(dst.m["c.jar"]) != "ccc" {
		t.Errorf("dst = %v, want a.jar/c.jar copied despite b.jar failing", dst.m)
	}
	if _, ok := dst.m["b.jar"]; ok {
		t.Errorf("dst has b.jar despite its write failing: %v", dst.m)
	}

	// Originals untouched even on partial failure.
	for name, want := range map[string]string{"a.jar": "aaa", "b.jar": "bbb", "c.jar": "ccc"} {
		got, readErr := os.ReadFile(filepath.Join(src, name))
		if readErr != nil || string(got) != want {
			t.Errorf("original %s = %q, err=%v, want %q untouched", name, got, readErr, want)
		}
	}
}

// --- CoreStorageHandler.Migrate (HTTP surface) ---

func TestCoreStorageHandler_Migrate_RefusesWhenUnconfigured(t *testing.T) {
	fs := newCoreStorageHTTPFakeStore()
	h := NewCoreStorageHandler(&AppState{Store: fs})

	rw := httptest.NewRecorder()
	h.Migrate(rw, httptest.NewRequest(http.MethodPost, "/api/settings/core-storage/migrate", nil))
	if rw.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (%s)", rw.Code, rw.Body.String())
	}
	if !strings.Contains(rw.Body.String(), "Configure Core file storage") {
		t.Errorf("body = %s, want a clear message to configure storage first", rw.Body.String())
	}
}

// migrateHandlerResponse mirrors the JSON shape CoreStorageHandler.Migrate
// writes, for decoding in the tests below.
type migrateHandlerResponse struct {
	Success bool                     `json:"success"`
	Results map[string]migrateResult `json:"results"`
}

// registerLegacyDirCleanup registers t.Cleanup to remove exactly the
// per-subsystem directories a test freshly creates under the real,
// cwd-relative legacy dylaris_data tree. It checks firstMissingAncestor PER
// SUBSYSTEM (library/ticket-attachments/ticket-backups) rather than once for
// the shared "dylaris_data" parent: if that parent already exists (e.g. a
// stray leftover from an earlier test run against this same cwd), a single
// top-level check would see nothing missing and skip registering cleanup
// entirely, silently leaking every subdirectory this test goes on to create.
func registerLegacyDirCleanup(t *testing.T, srcRoot string) {
	t.Helper()
	for _, sub := range []string{CoreStoragePrefixLibrary, CoreStoragePrefixAttachments, CoreStoragePrefixBackups} {
		if root := firstMissingAncestor(t, filepath.Join(srcRoot, sub)); root != "" {
			t.Cleanup(func() { os.RemoveAll(root) })
		}
	}
}

// seedLegacyFile creates a file (with any needed parent dirs) under the
// real, cwd-relative legacy dylaris_data tree that buildCoreStorageProvider's
// fallback and Migrate's src derivation both use.
func seedLegacyFile(t *testing.T, srcRoot, rel, content string) {
	t.Helper()
	full := filepath.Join(srcRoot, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", filepath.Dir(full), err)
	}
	if err := os.WriteFile(full, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile(%q): %v", full, err)
	}
}

// TestCoreStorageHandler_Migrate_HappyPathThenIdempotent drives the real
// endpoint end to end against the real filesystem (like
// TestBuildCoreStorageProvider_UnconfiguredFallsBackToLegacyPath already
// does): seeds files under all three legacy dylaris_data/<sub> dirs
// (including a nested one), migrates into a configured "path" backend, and
// asserts same relative names + byte-identical content + nested dirs
// preserved + originals untouched, then runs it again to prove idempotency
// (skip-if-exists, no duplication).
func TestCoreStorageHandler_Migrate_HappyPathThenIdempotent(t *testing.T) {
	baseDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}
	srcRoot := filepath.Join(baseDir, "dylaris_data")
	registerLegacyDirCleanup(t, srcRoot)

	seedLegacyFile(t, srcRoot, filepath.Join(CoreStoragePrefixLibrary, "lib.txt"), "L")
	seedLegacyFile(t, srcRoot, filepath.Join(CoreStoragePrefixAttachments, "att.txt"), "A")
	seedLegacyFile(t, srcRoot, filepath.Join(CoreStoragePrefixBackups, "nested", "bak.txt"), "B")

	fs := newCoreStorageHTTPFakeStore()
	destDir := testConnectionProbeDir(t)
	fs.kv[keyCoreStorageBackend] = "path"
	fs.kv[keyCoreStoragePath] = destDir
	fs.kv[keyCoreStoragePathConfirm] = "true"
	h := NewCoreStorageHandler(&AppState{Store: fs})

	doMigrate := func() migrateHandlerResponse {
		rw := httptest.NewRecorder()
		h.Migrate(rw, httptest.NewRequest(http.MethodPost, "/api/settings/core-storage/migrate", nil))
		if rw.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (%s)", rw.Code, rw.Body.String())
		}
		var got migrateHandlerResponse
		if err := json.Unmarshal(rw.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode response: %v (%s)", err, rw.Body.String())
		}
		return got
	}

	first := doMigrate()
	if !first.Success {
		t.Fatalf("first run success = false, want true")
	}
	for _, sub := range []string{CoreStoragePrefixLibrary, CoreStoragePrefixAttachments, CoreStoragePrefixBackups} {
		r := first.Results[sub]
		if r.Copied != 1 || r.Skipped != 0 || r.Failed != 0 {
			t.Errorf("%s first run = %+v, want Copied=1 Skipped=0 Failed=0", sub, r)
		}
	}

	// Destination has the right bytes at the right (nested) relative key.
	destChecks := map[string]string{
		filepath.Join(destDir, CoreStoragePrefixLibrary, "lib.txt"):           "L",
		filepath.Join(destDir, CoreStoragePrefixAttachments, "att.txt"):      "A",
		filepath.Join(destDir, CoreStoragePrefixBackups, "nested", "bak.txt"): "B",
	}
	for path, want := range destChecks {
		got, err := os.ReadFile(path)
		if err != nil || string(got) != want {
			t.Errorf("dest %s = %q, err=%v, want %q", path, got, err, want)
		}
	}

	// Originals untouched.
	srcChecks := map[string]string{
		filepath.Join(srcRoot, CoreStoragePrefixLibrary, "lib.txt"):           "L",
		filepath.Join(srcRoot, CoreStoragePrefixAttachments, "att.txt"):      "A",
		filepath.Join(srcRoot, CoreStoragePrefixBackups, "nested", "bak.txt"): "B",
	}
	for path, want := range srcChecks {
		got, err := os.ReadFile(path)
		if err != nil || string(got) != want {
			t.Errorf("original %s = %q, err=%v, want %q untouched", path, got, err, want)
		}
	}

	second := doMigrate()
	if !second.Success {
		t.Fatalf("second run success = false, want true (skipping is not a failure)")
	}
	for _, sub := range []string{CoreStoragePrefixLibrary, CoreStoragePrefixAttachments, CoreStoragePrefixBackups} {
		r := second.Results[sub]
		if r.Copied != 0 || r.Skipped != 1 || r.Failed != 0 {
			t.Errorf("%s second run = %+v, want Copied=0 Skipped=1 Failed=0 (idempotent re-run)", sub, r)
		}
	}
}

// TestCoreStorageHandler_Migrate_OneSubsystemFailsOthersStillMigrate proves
// subsystem-level partial failure reporting: ticket-backups' legacy "dir" is
// actually a regular file (triggers migrateLocalDirToProvider's "not a
// directory" guard), while library/ticket-attachments have normal one-file
// trees. The two healthy subsystems must still fully migrate, and the
// overall response must honestly report success=false, never silently
// succeed.
func TestCoreStorageHandler_Migrate_OneSubsystemFailsOthersStillMigrate(t *testing.T) {
	baseDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}
	srcRoot := filepath.Join(baseDir, "dylaris_data")
	registerLegacyDirCleanup(t, srcRoot)

	for _, sub := range []string{CoreStoragePrefixLibrary, CoreStoragePrefixAttachments} {
		seedLegacyFile(t, srcRoot, filepath.Join(sub, "f.txt"), sub)
	}
	// ticket-backups: a REGULAR FILE where a directory is expected.
	if err := os.MkdirAll(srcRoot, 0755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", srcRoot, err)
	}
	if err := os.WriteFile(filepath.Join(srcRoot, CoreStoragePrefixBackups), []byte("not a dir"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	fs := newCoreStorageHTTPFakeStore()
	destDir := testConnectionProbeDir(t)
	fs.kv[keyCoreStorageBackend] = "path"
	fs.kv[keyCoreStoragePath] = destDir
	fs.kv[keyCoreStoragePathConfirm] = "true"
	h := NewCoreStorageHandler(&AppState{Store: fs})

	rw := httptest.NewRecorder()
	h.Migrate(rw, httptest.NewRequest(http.MethodPost, "/api/settings/core-storage/migrate", nil))
	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rw.Code, rw.Body.String())
	}
	var got migrateHandlerResponse
	if err := json.Unmarshal(rw.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v (%s)", err, rw.Body.String())
	}
	if got.Success {
		t.Fatalf("success = true, want false: one subsystem failed, must be reported as a partial failure (%s)", rw.Body.String())
	}
	if r := got.Results[CoreStoragePrefixLibrary]; r.Copied != 1 || r.Failed != 0 {
		t.Errorf("library = %+v, want Copied=1 Failed=0 (must still migrate despite the other subsystem failing)", r)
	}
	if r := got.Results[CoreStoragePrefixAttachments]; r.Copied != 1 || r.Failed != 0 {
		t.Errorf("ticket-attachments = %+v, want Copied=1 Failed=0", r)
	}
	if r := got.Results[CoreStoragePrefixBackups]; r.Failed != 1 || len(r.Errors) == 0 {
		t.Errorf("ticket-backups = %+v, want Failed=1 with an error message", r)
	}

	for _, sub := range []string{CoreStoragePrefixLibrary, CoreStoragePrefixAttachments} {
		got, err := os.ReadFile(filepath.Join(destDir, sub, "f.txt"))
		if err != nil || string(got) != sub {
			t.Errorf("dest %s/f.txt = %q, err=%v, want %q", sub, got, err, sub)
		}
	}
}
