package backup

import (
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
