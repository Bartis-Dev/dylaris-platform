package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDownloadLibrary_RedirectsWhenProviderSignsURL covers the redirect path:
// a configured s3 backend produces a real (offline-computed, no network
// needed - see TestBuildCoreStorageProvider_ConfiguredS3BackendBuildsProvider)
// pre-signed URL, so the handler must 302 straight to it instead of streaming
// through Core. Change 3 removed LibraryHandler's injectable provider field,
// so this now drives the real per-request resolution path (state ->
// buildCoreStorageProvider -> a genuine *storage.S3Provider) rather than a
// hand-written fake.
func TestDownloadLibrary_RedirectsWhenProviderSignsURL(t *testing.T) {
	s := &AppState{Store: &coreStorageFakeStore{values: map[string]string{
		keyCoreStorageBackend:     "s3",
		keyCoreStorageS3Bucket:    "my-bucket",
		keyCoreStorageS3AccessKey: "AKIAEXAMPLE",
		keyCoreStorageS3SecretKey: "s3cr3t",
	}}}
	h := &LibraryHandler{state: s}
	rw := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/library/download?path=x.jar", nil)
	req = req.WithContext(context.WithValue(req.Context(), "isAdmin", true))
	h.DownloadLibraryHandler(rw, req)

	if rw.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302 (redirect to signed URL) (%s)", rw.Code, rw.Body.String())
	}
	loc := rw.Header().Get("Location")
	if loc == "" {
		t.Fatal("Location header is empty, want a pre-signed URL")
	}
	if !strings.Contains(loc, "my-bucket") {
		t.Errorf("Location = %q, want it to reference the configured bucket", loc)
	}
	if !strings.Contains(loc, "library/x.jar") {
		t.Errorf("Location = %q, want it to reference the library-scoped key", loc)
	}
}

// TestDownloadLibrary_StreamsWhenProviderDoesNotSignURL locks in the task's
// own stated highest-value non-regression: the path-backend / LocalProvider
// behavior (DownloadURL returns the ("", nil) sentinel, meaning "no URL, go
// stream it yourself") must stay byte-identical to the old code. Drives a
// real *storage.LocalProvider rooted at a fresh test directory (Change 3
// removed the injectable provider field the old fake-based version of this
// test relied on).
func TestDownloadLibrary_StreamsWhenProviderDoesNotSignURL(t *testing.T) {
	dir := testConnectionProbeDir(t)
	libDir := filepath.Join(dir, CoreStoragePrefixLibrary)
	if err := os.MkdirAll(libDir, 0755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", libDir, err)
	}
	content := "hello from the path backend"
	if err := os.WriteFile(filepath.Join(libDir, "x.jar"), []byte(content), 0644); err != nil {
		t.Fatalf("seed x.jar: %v", err)
	}

	s := &AppState{Store: &coreStorageFakeStore{values: map[string]string{
		keyCoreStorageBackend:     "path",
		keyCoreStoragePath:        dir,
		keyCoreStoragePathConfirm: "true",
	}}}
	h := &LibraryHandler{state: s}

	rw := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/library/download?path=x.jar", nil)
	req = req.WithContext(context.WithValue(req.Context(), "isAdmin", true))
	h.DownloadLibraryHandler(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (stream, not redirect) (%s)", rw.Code, rw.Body.String())
	}
	if loc := rw.Header().Get("Location"); loc != "" {
		t.Errorf("Location = %q, want empty (no redirect when streaming)", loc)
	}
	if got := rw.Body.String(); got != content {
		t.Errorf("body = %q, want %q", got, content)
	}
	if cd := rw.Header().Get("Content-Disposition"); cd != "attachment; filename=x.jar" {
		t.Errorf("Content-Disposition = %q, want %q", cd, "attachment; filename=x.jar")
	}
	if ct := rw.Header().Get("Content-Type"); ct != "application/octet-stream" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/octet-stream")
	}
}
