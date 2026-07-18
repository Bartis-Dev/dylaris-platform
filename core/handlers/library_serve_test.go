package handlers

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"dylaris-core/storage"
)

// urlProvider returns a fixed DownloadURL so we can assert the redirect path.
// Local to this file; never redeclared.
type urlProvider struct{ url string }

func (u urlProvider) ListFiles(string) ([]storage.FileInfo, error) { return nil, nil }
func (u urlProvider) GetFile(string) (io.ReadCloser, error)        { return nil, http.ErrMissingFile }
func (u urlProvider) DeletePath(string) error                      { return nil }
func (u urlProvider) CreateDir(string) error                       { return nil }
func (u urlProvider) CopyToLocal(string, string) error             { return nil }
func (u urlProvider) WriteFile(string, io.Reader) error            { return nil }
func (u urlProvider) DownloadURL(string, time.Duration) (string, error) {
	return u.url, nil
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
