package handlers

import (
	"encoding/json"
	"errors"
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

// TestMigrateLocalDirToProvider_NestedDestinationRefused guards Fix 2's
// second, cheap-but-real refusal: a destination nested INSIDE the source
// tree (not the exact same directory sameStorageDir already catches) must
// also be refused outright. Without this, filepath.WalkDir would walk into
// the destination dir it is concurrently writing under and re-ingest the
// files this same run just wrote (bounded duplication).
func TestMigrateLocalDirToProvider_NestedDestinationRefused(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "a.jar"), []byte("aaa"), 0644); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(src, "nested-dest") // deliberately INSIDE src, not yet created
	prov := &storage.LocalProvider{BasePath: dest}

	res, err := migrateLocalDirToProvider(src, prov)
	if err == nil {
		t.Fatal("expected an error when the destination is nested inside the source")
	}
	if res.Copied != 0 || res.Failed != 1 {
		t.Fatalf("result = %+v, want Copied=0 Failed=1", res)
	}
	got, readErr := os.ReadFile(filepath.Join(src, "a.jar"))
	if readErr != nil || string(got) != "aaa" {
		t.Fatalf("a.jar = %q, err=%v, want untouched aaa (nested-dir guard must refuse before writing anything)", got, readErr)
	}
	if _, statErr := os.Stat(dest); statErr == nil {
		t.Errorf("nested destination dir %q was created even though the migration was refused", dest)
	}
}

// TestNestedStorageDir unit-tests the nested-path helper directly: source
// inside destination, destination inside source, unrelated siblings (never
// nested), equal paths (not "nested" - that's sameStorageDir's job), and a
// sibling that merely shares a string prefix (must stay separator-bounded,
// same discipline as isSubPath's doc comment).
func TestNestedStorageDir(t *testing.T) {
	base := t.TempDir()
	a := filepath.Join(base, "a")
	b := filepath.Join(base, "b")
	aSub := filepath.Join(a, "sub")
	aSibling := filepath.Join(base, "a-sibling")

	tests := []struct {
		name string
		x, y string
		want bool
	}{
		{"y inside x", a, aSub, true},
		{"x inside y (argument order does not matter)", aSub, a, true},
		{"unrelated siblings", a, b, false},
		{`equal paths are not "nested"`, a, a, false},
		{"string-prefix sibling is not nested (separator-bounded)", a, aSibling, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := nestedStorageDir(tt.x, tt.y); got != tt.want {
				t.Errorf("nestedStorageDir(%q, %q) = %v, want %v", tt.x, tt.y, got, tt.want)
			}
		})
	}
}

// TestSameStorageDir_DeviceInodeCatchesAliasedPaths pins the os.SameFile
// (device+inode) half of Fix 2: two DIFFERENT path strings that resolve to
// the identical underlying file must compare equal, which the pre-fix plain
// Abs+Clean string comparison could never see (that's exactly what let a
// symlink or a bind mount pointing at the same directory slip through).
//
// A real directory symlink/bind mount could not be created portably in this
// test environment: os.Symlink on this Windows dev host fails without the
// elevated SeCreateSymbolicLinkPrivilege (verified separately - "Dem Client
// fehlt ein erforderliches Recht" / a required privilege is missing), so
// this is a hard link between two FILE paths instead. os.SameFile compares
// only device+inode; it has no notion of symlink vs. bind mount vs. hard
// link vs. junction, so exercising it via a hard link tests the exact same
// comparison migrateLocalDirToProvider relies on for directories in
// production. Skips (not fails) if this filesystem can't hard-link either.
func TestSameStorageDir_DeviceInodeCatchesAliasedPaths(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.txt")
	if err := os.WriteFile(target, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(dir, "alias.txt")
	if err := os.Link(target, alias); err != nil {
		t.Skipf("hard links not supported in this environment: %v", err)
	}

	if !sameStorageDir(target, alias) {
		t.Errorf("sameStorageDir(%q, %q) = false, want true (os.SameFile must catch the alias)", target, alias)
	}

	other := filepath.Join(dir, "other.txt")
	if err := os.WriteFile(other, []byte("y"), 0644); err != nil {
		t.Fatal(err)
	}
	if sameStorageDir(target, other) {
		t.Errorf("sameStorageDir(%q, %q) = true, want false (distinct files, not aliased)", target, other)
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

// migrateGetErrorProvider wraps memProvider so GetFile (the skip-if-exists
// destination probe) can be made to return an arbitrary error for specific
// keys, simulating a transient backend failure (S3 503/timeout/throttle/
// permission) that is NOT a recognised not-found. Distinct from
// migrateFailOnceProvider above (which forces a WriteFile failure, not a
// GetFile one).
type migrateGetErrorProvider struct {
	*memProvider
	getErrKeys map[string]error
}

func (p *migrateGetErrorProvider) GetFile(key string) (io.ReadCloser, error) {
	if err, ok := p.getErrKeys[key]; ok {
		return nil, err
	}
	return p.memProvider.GetFile(key)
}

// TestMigrateLocalDirToProvider_DestinationProbe is the Fix 1 regression
// test: only a RECOGNISED not-found from the destination probe (GetFile) may
// mean "copy it". A genuine not-found (errors.Is(err, fs.ErrNotExist), what
// memProvider.GetFile already returns for a missing key) DOES copy, per the
// pre-existing skip-if-exists contract. Any OTHER error - standing in for a
// transient S3 503/timeout/throttle/permission failure, which looks exactly
// like "missing" to a caller that does not discriminate - must be recorded
// as a per-file Failed and must NEVER write, leaving the destination
// byte-for-byte exactly as it was.
//
// Before the fix, migrateLocalDirToProvider treated ANY GetFile error as
// "not present yet" and always proceeded to WriteFile, so the transient case
// below silently overwrote the pre-existing (correct, newer) destination
// content with the stale source copy and reported it as a successful,
// zero-Failed copy - this test is RED against that code and GREEN against
// the fix (see the task report for the captured before/after test output).
func TestMigrateLocalDirToProvider_DestinationProbe(t *testing.T) {
	tests := []struct {
		name              string
		getErr            error
		preSeedDest       bool
		wantCopied        int
		wantFailed        int
		wantDestUnchanged bool
	}{
		{
			name:       "genuine not-found copies",
			getErr:     os.ErrNotExist,
			wantCopied: 1,
			wantFailed: 0,
		},
		{
			name:              "transient probe error fails without writing, destination left untouched",
			getErr:            errors.New("simulated transient error: 503 service unavailable"),
			preSeedDest:       true,
			wantCopied:        0,
			wantFailed:        1,
			wantDestUnchanged: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := t.TempDir()
			if err := os.WriteFile(filepath.Join(src, "a.jar"), []byte("new-from-source"), 0644); err != nil {
				t.Fatal(err)
			}

			dst := &migrateGetErrorProvider{memProvider: newMemProvider(), getErrKeys: map[string]error{"a.jar": tt.getErr}}
			if tt.preSeedDest {
				dst.m["a.jar"] = []byte("existing-dest-content")
			}

			res, err := migrateLocalDirToProvider(src, dst)
			if tt.wantFailed > 0 && err == nil {
				t.Fatal("expected a non-nil error")
			}
			if res.Copied != tt.wantCopied || res.Failed != tt.wantFailed {
				t.Fatalf("result = %+v, want Copied=%d Failed=%d", res, tt.wantCopied, tt.wantFailed)
			}
			if tt.wantDestUnchanged {
				if string(dst.m["a.jar"]) != "existing-dest-content" {
					t.Errorf("destination a.jar = %q, want untouched %q (a non-not-found probe error must never write)", dst.m["a.jar"], "existing-dest-content")
				}
			} else if tt.wantCopied > 0 {
				if string(dst.m["a.jar"]) != "new-from-source" {
					t.Errorf("destination a.jar = %q, want %q (genuine not-found must still copy)", dst.m["a.jar"], "new-from-source")
				}
			}

			// Original source is always untouched (rule 1), regardless of outcome.
			gotSrc, readErr := os.ReadFile(filepath.Join(src, "a.jar"))
			if readErr != nil || string(gotSrc) != "new-from-source" {
				t.Errorf("original source a.jar = %q, err=%v, want untouched %q", gotSrc, readErr, "new-from-source")
			}
		})
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
	// Migrate derives its source root from os.Getwd(); t.Chdir into a
	// private temp dir first so this test's seeded legacy files (and the
	// fallback provider's MkdirAll side effects) land somewhere t.TempDir()
	// auto-cleans, instead of leaking into this package's own directory.
	t.Chdir(t.TempDir())
	baseDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}
	srcRoot := filepath.Join(baseDir, "dylaris_data")

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
		filepath.Join(destDir, CoreStoragePrefixAttachments, "att.txt"):       "A",
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
		filepath.Join(srcRoot, CoreStoragePrefixAttachments, "att.txt"):       "A",
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
	// See TestCoreStorageHandler_Migrate_HappyPathThenIdempotent: isolate cwd
	// so this test's legacy dylaris_data tree lands in an auto-cleaned temp
	// dir, not this package's own directory.
	t.Chdir(t.TempDir())
	baseDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}
	srcRoot := filepath.Join(baseDir, "dylaris_data")

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
