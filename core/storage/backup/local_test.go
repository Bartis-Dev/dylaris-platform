package backup

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLocalStorage_resolveKey_ContainsTraversal documents the REAL guard:
// resolveKey prefixes the key with "/" before filepath.Clean, so any ".."
// segment collapses at that synthetic root and can never walk above
// BasePath - the key is silently and safely remapped to stay inside
// BasePath rather than rejected with an error. See the finding in this
// task's plan entry for why the HasPrefix error branch is otherwise
// unreachable via any string key.
func TestLocalStorage_resolveKey_ContainsTraversal(t *testing.T) {
	base := t.TempDir()
	absBase, err := filepath.Abs(base)
	if err != nil {
		t.Fatalf("filepath.Abs: %v", err)
	}
	l := &LocalStorage{BasePath: absBase}

	cases := []struct {
		name string
		key  string
	}{
		{"parent traversal", "../../etc/passwd"},
		{"embedded traversal", "backups/../../etc/passwd"},
		{"single parent segment", "../secret.txt"},
		{"bare double-dot", ".."},
		{"deep traversal past root", "a/b/../../../c"},
		{"leading slash (already absolute-looking)", "/backups/x.tar"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			full, err := l.resolveKey(tc.key)
			if err != nil {
				t.Fatalf("resolveKey(%q) returned an error: %v (expected safe containment, not rejection)", tc.key, err)
			}
			if full != absBase && !strings.HasPrefix(full, absBase+string(filepath.Separator)) {
				t.Errorf("resolveKey(%q) = %q, escaped BasePath %q", tc.key, full, absBase)
			}
		})
	}
}

func TestLocalStorage_resolveKey_HappyPath(t *testing.T) {
	base := t.TempDir()
	absBase, err := filepath.Abs(base)
	if err != nil {
		t.Fatalf("filepath.Abs: %v", err)
	}
	l := &LocalStorage{BasePath: absBase}

	full, err := l.resolveKey("backups/2026-07-12.tar.gz")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := filepath.Join(absBase, "backups", "2026-07-12.tar.gz")
	if full != want {
		t.Errorf("resolveKey = %q, want %q", full, want)
	}
}

// TestLocalStorage_resolveKey_UnreachableGuard is the one way to actually
// exercise the HasPrefix error branch: give it a BasePath that is NOT
// clean (violating the invariant NewLocal enforces via filepath.Abs, which
// always returns a cleaned path). This is a synthetic misuse case, not a
// traversal-via-key scenario - see the finding above.
//
// The BasePath must contain a ".." segment so the trigger is OS-independent:
// filepath.Join always returns a cleaned path (no ".."), so a raw BasePath
// that still contains "/.." can never be a prefix of the joined result on
// EITHER separator. A merely non-absolute BasePath like "relative/base" would
// only fire on Windows (where the joined "\..." mismatches the "/"-form
// prefix by accident) and would spuriously pass on Linux/macOS - i.e. red CI.
func TestLocalStorage_resolveKey_UnreachableGuard(t *testing.T) {
	l := &LocalStorage{BasePath: "/base/sub/.."}
	if _, err := l.resolveKey("a/b.txt"); err == nil {
		t.Error("expected an error when BasePath itself is not clean")
	}
}

// errReader yields `remaining` bytes of 'a' then fails, simulating a client or
// a mounted share that drops mid-transfer.
type errReader struct{ remaining int }

func (e *errReader) Read(p []byte) (int, error) {
	if e.remaining <= 0 {
		return 0, errors.New("simulated mid-transfer failure")
	}
	n := min(len(p), e.remaining)
	for i := 0; i < n; i++ {
		p[i] = 'a'
	}
	e.remaining -= n
	return n, nil
}

func TestLocalStorage_Put_HappyPath(t *testing.T) {
	l := &LocalStorage{BasePath: t.TempDir()}
	payload := []byte("backup archive bytes")
	if err := l.Put(context.Background(), "server-1/2026-07-21.tar.gz", bytes.NewReader(payload), int64(len(payload))); err != nil {
		t.Fatalf("Put: %v", err)
	}
	rc, err := l.Get(context.Background(), "server-1/2026-07-21.tar.gz")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer rc.Close()
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("read back %q, want %q", got, payload)
	}
}

// A write that dies mid-transfer must never publish a truncated archive under
// the real key. Staging plus rename is what guarantees it.
func TestLocalStorage_Put_InterruptedWriteLeavesNoFileUnderRealName(t *testing.T) {
	base := t.TempDir()
	l := &LocalStorage{BasePath: base}
	key := "server-1/partial.tar.gz"
	if err := l.Put(context.Background(), key, &errReader{remaining: 8}, -1); err == nil {
		t.Fatal("Put returned nil on a failing reader, expected an error")
	}
	full := filepath.Join(base, "server-1", "partial.tar.gz")
	if _, statErr := os.Stat(full); statErr == nil {
		t.Errorf("a truncated file was published under the real name %q; staging must prevent this", full)
	} else if !os.IsNotExist(statErr) {
		t.Fatalf("unexpected stat error: %v", statErr)
	}
}

// A failed OVERWRITE must not destroy the existing good archive. Truncate in
// place does; staging plus rename does not, because the real name is never
// touched until a complete file is ready.
func TestLocalStorage_Put_InterruptedOverwriteKeepsOldContent(t *testing.T) {
	base := t.TempDir()
	l := &LocalStorage{BasePath: base}
	key := "server-1/keep.tar.gz"
	original := []byte("the good backup that must not be destroyed")
	if err := l.Put(context.Background(), key, bytes.NewReader(original), int64(len(original))); err != nil {
		t.Fatalf("initial Put: %v", err)
	}
	if err := l.Put(context.Background(), key, &errReader{remaining: 4}, -1); err == nil {
		t.Fatal("overwrite Put returned nil on a failing reader, expected an error")
	}
	rc, err := l.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("Get after failed overwrite: %v", err)
	}
	defer rc.Close()
	got, _ := io.ReadAll(rc)
	if !bytes.Equal(got, original) {
		t.Errorf("failed overwrite corrupted the existing archive: got %q, want %q", got, original)
	}
}

// A leftover staging temp file (an orphan from an interrupted Put) must not be
// listed as if it were a real backup archive.
func TestLocalStorage_List_HidesStagingTempFiles(t *testing.T) {
	base := t.TempDir()
	l := &LocalStorage{BasePath: base}
	if err := l.Put(context.Background(), "server-1/real.tar.gz", strings.NewReader("x"), 1); err != nil {
		t.Fatalf("Put: %v", err)
	}
	dir := filepath.Join(base, "server-1")
	tmp, err := os.CreateTemp(dir, backupUploadTempPrefix+"*")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	tmp.Close()

	objs, err := l.List(context.Background(), "server-1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	sawReal := false
	for _, o := range objs {
		if strings.Contains(filepath.Base(o.Key), backupUploadTempPrefix) {
			t.Errorf("List returned a staging temp file: %q", o.Key)
		}
		if strings.HasSuffix(o.Key, "real.tar.gz") {
			sawReal = true
		}
	}
	if !sawReal {
		t.Error("List did not return the real archive")
	}
}
