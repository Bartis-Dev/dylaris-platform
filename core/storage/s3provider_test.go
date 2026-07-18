package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"testing"
	"time"

	"dylaris-core/storage/backup"
)

// fakeObjectStore is an in-memory objectStore for S3Provider tests. It is
// local to this file and never redeclared elsewhere.
type fakeObjectStore struct {
	m map[string][]byte
}

func newFakeObjectStore() *fakeObjectStore { return &fakeObjectStore{m: map[string][]byte{}} }

func (f *fakeObjectStore) Put(_ context.Context, key string, r io.Reader, _ int64) error {
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	f.m[key] = b
	return nil
}

func (f *fakeObjectStore) Get(_ context.Context, key string) (io.ReadCloser, error) {
	b, ok := f.m[key]
	if !ok {
		return nil, fmt.Errorf("not found: %s", key)
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

func (f *fakeObjectStore) Delete(_ context.Context, key string) error {
	delete(f.m, key)
	return nil
}

func (f *fakeObjectStore) List(_ context.Context, prefix string) ([]backup.Object, error) {
	var out []backup.Object
	for k, v := range f.m {
		if strings.HasPrefix(k, prefix) {
			out = append(out, backup.Object{Key: k, Size: int64(len(v))})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

func (f *fakeObjectStore) DownloadURL(_ context.Context, key string, _ time.Duration) (string, error) {
	return "https://signed.example/" + key, nil
}

func TestS3Provider_WriteGetDelete_AppliesPrefix(t *testing.T) {
	fos := newFakeObjectStore()
	p := &S3Provider{os: fos, prefix: "library"}

	if err := p.WriteFile("dir/a.txt", strings.NewReader("payload")); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, ok := fos.m["library/dir/a.txt"]; !ok {
		t.Fatalf("stored keys = %v, want library/dir/a.txt (prefix applied)", keys(fos.m))
	}
	rc, err := p.GetFile("dir/a.txt")
	if err != nil {
		t.Fatalf("GetFile: %v", err)
	}
	got, _ := io.ReadAll(rc)
	rc.Close()
	if string(got) != "payload" {
		t.Errorf("GetFile = %q, want payload", got)
	}
	if err := p.DeletePath("dir/a.txt"); err != nil {
		t.Fatalf("DeletePath: %v", err)
	}
	if _, ok := fos.m["library/dir/a.txt"]; ok {
		t.Errorf("key still present after DeletePath")
	}
}

func TestS3Provider_DownloadURL_ReturnsSignedPrefixedKey(t *testing.T) {
	p := &S3Provider{os: newFakeObjectStore(), prefix: "library"}
	url, err := p.DownloadURL("dir/a.txt", time.Minute)
	if err != nil {
		t.Fatalf("DownloadURL: %v", err)
	}
	if url != "https://signed.example/library/dir/a.txt" {
		t.Errorf("DownloadURL = %q, want signed prefixed key", url)
	}
}

func TestS3Provider_ListFiles_SynthesizesImmediateChildren(t *testing.T) {
	fos := newFakeObjectStore()
	p := &S3Provider{os: fos, prefix: "library"}
	_ = p.WriteFile("root.txt", strings.NewReader("r"))
	_ = p.WriteFile("mods/one.jar", strings.NewReader("j"))
	_ = p.WriteFile("mods/two.jar", strings.NewReader("j"))

	files, err := p.ListFiles("/")
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	var names []string
	dirs := map[string]bool{}
	for _, f := range files {
		names = append(names, f.Name)
		if f.IsDir {
			dirs[f.Name] = true
		}
	}
	sort.Strings(names)
	if len(names) != 2 || names[0] != "mods" || names[1] != "root.txt" {
		t.Fatalf("ListFiles names = %v, want [mods root.txt]", names)
	}
	if !dirs["mods"] {
		t.Errorf("expected mods to be a synthesized dir")
	}
}

func TestS3Provider_CreateDirIsNoop(t *testing.T) {
	p := &S3Provider{os: newFakeObjectStore(), prefix: "library"}
	if err := p.CreateDir("anything"); err != nil {
		t.Errorf("CreateDir = %v, want nil (no-op for object stores)", err)
	}
}

func TestNewProvider_S3RequiresBucket(t *testing.T) {
	if _, err := NewProvider("s3", "", map[string]string{OptS3AccessKey: "k", OptS3SecretKey: "s"}); err == nil {
		t.Fatal("NewProvider s3 without bucket err = nil, want error")
	}
}

func keys(m map[string][]byte) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
