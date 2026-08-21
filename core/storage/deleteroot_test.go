package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// rootPaths are the spellings that resolve to the scoped store's own root.
// "" is the one that shipped: a JSON body with the field omitted decodes to
// the zero value, and filepath.Join(base, "") is base.
var rootPaths = []string{"", " ", ".", "/", "./", "/.", "a/..", "//"}

func TestAddressesRoot(t *testing.T) {
	for _, p := range rootPaths {
		if !addressesRoot(p) {
			t.Errorf("addressesRoot(%q) = false, want true", p)
		}
	}
	for _, p := range []string{"a", "a/b", "/a", "./a", "..a", "a/../b", ".hidden"} {
		if addressesRoot(p) {
			t.Errorf("addressesRoot(%q) = true, want false", p)
		}
	}
}

func TestLocalProvider_DeletePathRefusesRoot(t *testing.T) {
	for _, p := range rootPaths {
		t.Run("path="+p, func(t *testing.T) {
			base := t.TempDir()
			if err := os.WriteFile(filepath.Join(base, "keep.txt"), []byte("x"), 0o644); err != nil {
				t.Fatalf("seed: %v", err)
			}
			prov := &LocalProvider{BasePath: base}

			err := prov.DeletePath(context.Background(), p)
			if !errors.Is(err, ErrDeleteRoot) {
				t.Fatalf("DeletePath(%q) err = %v, want ErrDeleteRoot", p, err)
			}
			if _, serr := os.Stat(filepath.Join(base, "keep.txt")); serr != nil {
				t.Fatalf("DeletePath(%q) removed the tree anyway: %v", p, serr)
			}
			if _, serr := os.Stat(base); serr != nil {
				t.Fatalf("DeletePath(%q) removed the root itself: %v", p, serr)
			}
		})
	}
}

// TestLocalProvider_DeletePathRefusesWalkBackIntoRoot covers the spelling
// addressesRoot structurally cannot recognise: "../<the base's own last
// segment>" is not a root spelling (it cleans to "/<segment>"), but
// filepath.Join resolves it straight back onto the base, so before validatePath
// rooted the request path this deleted the whole store and reported success.
func TestLocalProvider_DeletePathRefusesWalkBackIntoRoot(t *testing.T) {
	base := t.TempDir()
	if err := os.WriteFile(filepath.Join(base, "keep.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	prov := &LocalProvider{BasePath: base}

	// The real request against a library provider is "../library"; derive it
	// from the temp dir so the test does not depend on a fixed base name.
	back := "../" + filepath.Base(base)
	if addressesRoot(back) {
		t.Fatalf("precondition: addressesRoot(%q) already true, this test proves nothing", back)
	}

	// Not an error: the rooted join turns it into "<base>/<segment>", a
	// subdirectory that does not exist, and RemoveAll on a missing path is a
	// no-op. What matters is that the STORE survives.
	if err := prov.DeletePath(context.Background(), back); err != nil {
		t.Fatalf("DeletePath(%q) err = %v, want nil", back, err)
	}
	if _, err := os.Stat(filepath.Join(base, "keep.txt")); err != nil {
		t.Fatalf("DeletePath(%q) wiped the store: %v", back, err)
	}
}

// TestLocalProvider_ValidatePathStaysInsideBase pins the containment guard for
// both shapes it has to refuse or neutralise: an escape ABOVE the base, and a
// SIBLING whose name merely starts with the base's (a bare HasPrefix accepts
// that one).
func TestLocalProvider_ValidatePathStaysInsideBase(t *testing.T) {
	parent := t.TempDir()
	base := filepath.Join(parent, "library")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	prov := &LocalProvider{BasePath: base}

	for _, req := range []string{
		"../library",           // walks back onto the base itself
		"../library-old/x.jar", // sibling sharing the base's name prefix
		"../../etc/passwd",     // plain escape
		"../ticket-backups/a",  // a neighbouring core-storage namespace
		"a/../../b",            // escape hidden mid-path
	} {
		got, err := prov.validatePath(req)
		if err != nil {
			continue // refused outright is fine
		}
		if got != base && !strings.HasPrefix(got, base+string(os.PathSeparator)) {
			t.Errorf("validatePath(%q) = %q, which is outside %q", req, got, base)
		}
	}

	// A normal relative key must still resolve to exactly where it always did.
	got, err := prov.validatePath("mods/sub/a.jar")
	if err != nil {
		t.Fatalf("validatePath(mods/sub/a.jar): %v", err)
	}
	if want := filepath.Join(base, "mods", "sub", "a.jar"); got != want {
		t.Fatalf("validatePath(mods/sub/a.jar) = %q, want %q", got, want)
	}
}

func TestLocalProvider_DeletePathStillDeletesEntries(t *testing.T) {
	base := t.TempDir()
	prov := &LocalProvider{BasePath: base}
	if err := prov.WriteFile(context.Background(), "dir/a.txt", strings.NewReader("x")); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := prov.DeletePath(context.Background(), "dir"); err != nil {
		t.Fatalf("DeletePath(dir) err = %v, want nil", err)
	}
	if _, err := os.Stat(filepath.Join(base, "dir")); !os.IsNotExist(err) {
		t.Fatalf("dir still present after delete: %v", err)
	}
}

func TestS3Provider_DeletePathRefusesRoot(t *testing.T) {
	for _, p := range rootPaths {
		t.Run("path="+p, func(t *testing.T) {
			fos := newFakeObjectStore()
			prov := &S3Provider{os: fos, prefix: "library"}
			if err := prov.WriteFile(context.Background(), "dir/a.txt", strings.NewReader("x")); err != nil {
				t.Fatalf("seed: %v", err)
			}

			err := prov.DeletePath(context.Background(), p)
			if !errors.Is(err, ErrDeleteRoot) {
				t.Fatalf("DeletePath(%q) err = %v, want ErrDeleteRoot", p, err)
			}
			if _, ok := fos.m["library/dir/a.txt"]; !ok {
				t.Fatalf("DeletePath(%q) wiped the prefix anyway; remaining = %v", p, keys(fos.m))
			}
		})
	}
}
