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
