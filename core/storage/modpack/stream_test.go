package modpack

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dylaris-core/storage"
)

// Stream exists so a handler can serve a pack without holding it in memory.
// These pin the three properties a caller depends on for that: the reader is
// the object, the size describes THAT object, and a missing key is
// distinguishable from a broken backend.

func writeAt(t *testing.T, base, key, content string) {
	t.Helper()
	full := filepath.Join(base, filepath.FromSlash(key))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func readAllAndClose(t *testing.T, rc io.ReadCloser) string {
	t.Helper()
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read stream: %v", err)
	}
	return string(data)
}

// TestLocalProvider_StreamFindsTheKeyAcrossMirrorPaths: Get probes the
// configured paths in order and returns the first hit, and Stream has to search
// the same way or the two disagree about which object exists.
func TestLocalProvider_StreamFindsTheKeyAcrossMirrorPaths(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()
	writeAt(t, second, "modpacks/pack.mrpack", "second-path-bytes")

	p := &LocalProvider{Paths: []string{first, second}}

	rc, size, err := p.Stream(context.Background(), "modpacks/pack.mrpack")
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if size != int64(len("second-path-bytes")) {
		t.Errorf("size = %d, want %d", size, len("second-path-bytes"))
	}
	if got := readAllAndClose(t, rc); got != "second-path-bytes" {
		t.Errorf("body = %q, want the object from the second path", got)
	}
}

// TestLocalProvider_StreamReportsAMissingKey: absence has to be the sentinel,
// not a generic error, because callers turn it into a 404.
func TestLocalProvider_StreamReportsAMissingKey(t *testing.T) {
	p := &LocalProvider{Paths: []string{t.TempDir()}}

	_, _, err := p.Stream(context.Background(), "modpacks/gone.mrpack")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound", err)
	}
}

// TestLocalProvider_StreamSizeMatchesTheBody guards the header the mirror sets
// from the returned size. A size taken from a separate lookup could describe a
// different version of the object than the one being read; taking it from the
// open file cannot.
func TestLocalProvider_StreamSizeMatchesTheBody(t *testing.T) {
	base := t.TempDir()
	writeAt(t, base, "modpacks/pack.mrpack", "exactly-these-bytes")

	p := &LocalProvider{Paths: []string{base}}

	rc, size, err := p.Stream(context.Background(), "modpacks/pack.mrpack")
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	body := readAllAndClose(t, rc)
	if size != int64(len(body)) {
		t.Fatalf("size = %d but the body is %d bytes: a client trusting Content-Length would hang or truncate", size, len(body))
	}
}

// TestLocalProvider_DownloadURLIsEmpty: these are paths on Core's own disk, so
// there is no URL to hand out. Empty-with-no-error is the documented way to say
// that, and a caller treats it as "stream it yourself".
func TestLocalProvider_DownloadURLIsEmpty(t *testing.T) {
	p := &LocalProvider{Paths: []string{t.TempDir()}}

	url, err := p.DownloadURL(context.Background(), "modpacks/pack.mrpack", time.Hour)
	if err != nil {
		t.Fatalf("DownloadURL: %v", err)
	}
	if url != "" {
		t.Fatalf("url = %q, want empty", url)
	}
}

// TestCoreStorageProvider_StreamDelegatesAndMapsAbsence: the adapter's own Get
// was where the buffering lived - the layer underneath already streams - so
// Stream must reach it, and fs.ErrNotExist has to become the package sentinel.
func TestCoreStorageProvider_StreamDelegatesAndMapsAbsence(t *testing.T) {
	base := t.TempDir()
	p := NewCoreStorageProvider(&storage.LocalProvider{BasePath: base})

	if err := p.Put(context.Background(), "modpacks/pack.mrpack", []byte("core-storage-bytes")); err != nil {
		t.Fatalf("Put: %v", err)
	}

	rc, size, err := p.Stream(context.Background(), "modpacks/pack.mrpack")
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if got := readAllAndClose(t, rc); got != "core-storage-bytes" {
		t.Errorf("body = %q, want the stored object", got)
	}
	// SizeUnknown, not the real length. This assertion used to demand the real
	// size, and satisfying it meant calling Stat while the reader returned
	// above still held one of the shared filesystem-semaphore slots - a second
	// acquisition that deadlocks the whole storage layer under concurrency.
	// The requirement was the bug, so it is the assertion that changed. The
	// deadlock guard lives in core_storage_stream_test.go.
	if size != SizeUnknown {
		t.Errorf("size = %d, want SizeUnknown: learning the real size needs a second slot acquisition", size)
	}

	if _, _, err := p.Stream(context.Background(), "modpacks/gone.mrpack"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("error for a missing key = %v, want ErrNotFound", err)
	}
}

// TestCoreStorageProvider_DownloadURLIsEmptyOnAPathBackend: a core storage on
// a host path cannot presign, and that has to surface as the empty-string
// convention rather than an error, or the mirror would fail a request it could
// have served.
func TestCoreStorageProvider_DownloadURLIsEmptyOnAPathBackend(t *testing.T) {
	p := NewCoreStorageProvider(&storage.LocalProvider{BasePath: t.TempDir()})

	url, err := p.DownloadURL(context.Background(), "modpacks/pack.mrpack", time.Hour)
	if err != nil {
		t.Fatalf("DownloadURL: %v", err)
	}
	if url != "" {
		t.Fatalf("url = %q, want empty for a path backend", url)
	}
}

// TestS3Provider_StreamReturnsTheBodyAndItsLength: the win on S3 is that the
// response body IS the stream, so the object crosses Core between two sockets
// instead of landing in the heap.
func TestS3Provider_StreamReturnsTheBodyAndItsLength(t *testing.T) {
	const body = "s3-object-bytes"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "15")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	p := newTestS3(t, srv.URL)

	rc, size, err := p.Stream(context.Background(), "modpacks/pack.mrpack")
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if size != int64(len(body)) {
		t.Errorf("size = %d, want %d", size, len(body))
	}
	if got := readAllAndClose(t, rc); got != body {
		t.Errorf("body = %q, want %q", got, body)
	}
}

// TestS3Provider_StreamReportsAMissingKey keeps absence distinguishable on the
// backend where the mirror is most likely to run.
func TestS3Provider_StreamReportsAMissingKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	p := newTestS3(t, srv.URL)

	if _, _, err := p.Stream(context.Background(), "modpacks/gone.mrpack"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound", err)
	}
}

// TestS3Provider_DownloadURLPresigns: this is the case that takes Core out of
// the data path completely. A presign contacts nothing, so it is asserted on
// its shape rather than on a round trip.
func TestS3Provider_DownloadURLPresigns(t *testing.T) {
	p := newTestS3(t, "https://objects.example")

	url, err := p.DownloadURL(context.Background(), "modpacks/pack.mrpack", time.Hour)
	if err != nil {
		t.Fatalf("DownloadURL: %v", err)
	}
	if url == "" {
		t.Fatal("url is empty, want a presigned URL from an s3 backend")
	}
	for _, want := range []string{"modpacks/pack.mrpack", "X-Amz-Signature", "X-Amz-Expires"} {
		if !strings.Contains(url, want) {
			t.Errorf("url %q does not contain %q", url, want)
		}
	}
}

// newTestS3 points a provider at a given endpoint with path-style addressing,
// so a plain httptest server can stand in for the object store.
func newTestS3(t *testing.T, endpoint string) *S3Provider {
	t.Helper()
	// One attempt rather than the SDK's default three with backoff: the
	// not-found test would otherwise retry a 404 and take seconds.
	t.Setenv("AWS_MAX_ATTEMPTS", "1")
	p, err := NewS3(endpoint, "us-east-1", "packs", "key", "secret")
	if err != nil {
		t.Fatalf("NewS3: %v", err)
	}
	return p
}
