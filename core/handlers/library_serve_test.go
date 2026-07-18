package handlers

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"dylaris-core/storage"
)

// urlProvider returns a fixed DownloadURL (and optionally a fixed DownloadURL
// error and GetFile payload) so we can assert both the redirect path and the
// stream-it-yourself path. Local to this file; never redeclared.
//
// When url == "" and err == nil, DownloadURL returns the LocalProvider/path-
// backend sentinel ("no presigned URL, stream it") and GetFile serves data.
// When err != nil, DownloadURL failed outright, which must NOT be conflated
// with the empty-string sentinel: the handler still has to stream.
type urlProvider struct {
	url  string
	err  error
	data string
}

func (u urlProvider) ListFiles(string) ([]storage.FileInfo, error) { return nil, nil }
func (u urlProvider) GetFile(string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader(u.data)), nil
}
func (u urlProvider) DeletePath(string) error           { return nil }
func (u urlProvider) CreateDir(string) error            { return nil }
func (u urlProvider) CopyToLocal(string, string) error  { return nil }
func (u urlProvider) WriteFile(string, io.Reader) error { return nil }
func (u urlProvider) DownloadURL(string, time.Duration) (string, error) {
	return u.url, u.err
}

func TestDownloadLibrary_RedirectsWhenProviderSignsURL(t *testing.T) {
	h := &LibraryHandler{provider: urlProvider{url: "https://signed.example/library/x.jar"}}
	rw := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/library/download?path=x.jar", nil)
	req = req.WithContext(context.WithValue(req.Context(), "isAdmin", true))
	h.DownloadLibraryHandler(rw, req)
	if rw.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302 (redirect to signed URL)", rw.Code)
	}
	if loc := rw.Header().Get("Location"); loc != "https://signed.example/library/x.jar" {
		t.Errorf("Location = %q, want signed URL", loc)
	}
}

// TestDownloadLibrary_StreamsWhenProviderDoesNotSignURL locks in the task's
// own stated highest-value non-regression: the path-backend / LocalProvider
// behavior (DownloadURL returns the ("", nil) sentinel, meaning "no URL, go
// stream it yourself") must stay byte-identical to the old code, and a
// DownloadURL error must never be conflated with that empty-string sentinel
// (an error must still fall through to streaming, not to a redirect).
func TestDownloadLibrary_StreamsWhenProviderDoesNotSignURL(t *testing.T) {
	tests := []struct {
		name     string
		provider urlProvider
	}{
		{
			name:     "empty-string sentinel: no presigned URL, stream the file",
			provider: urlProvider{url: "", err: nil, data: "hello from the path backend"},
		},
		{
			name:     "DownloadURL error is not conflated with the sentinel: still stream",
			provider: urlProvider{url: "", err: errors.New("presign not supported"), data: "hello from the path backend"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &LibraryHandler{provider: tt.provider}
			rw := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/api/library/download?path=x.jar", nil)
			req = req.WithContext(context.WithValue(req.Context(), "isAdmin", true))
			h.DownloadLibraryHandler(rw, req)

			if rw.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 (stream, not redirect)", rw.Code)
			}
			if loc := rw.Header().Get("Location"); loc != "" {
				t.Errorf("Location = %q, want empty (no redirect when streaming)", loc)
			}
			if got := rw.Body.String(); got != tt.provider.data {
				t.Errorf("body = %q, want %q", got, tt.provider.data)
			}
			if cd := rw.Header().Get("Content-Disposition"); cd != "attachment; filename=x.jar" {
				t.Errorf("Content-Disposition = %q, want %q", cd, "attachment; filename=x.jar")
			}
			if ct := rw.Header().Get("Content-Type"); ct != "application/octet-stream" {
				t.Errorf("Content-Type = %q, want %q", ct, "application/octet-stream")
			}
		})
	}
}
