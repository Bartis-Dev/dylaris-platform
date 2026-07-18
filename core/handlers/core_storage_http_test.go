package handlers

import (
	"bytes"
	"encoding/json"
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
	"dylaris-core/store"
)

// coreStorageHTTPFakeStore is a read/write settings fake for the CRUD +
// test-connection handler tests below. Distinct from coreStorageFakeStore
// (core_storage_builder_test.go, GetSetting-only, read-side tests) and from
// modeFakeStore (permissions_mode_test.go, always returns one fixed value):
// this one records SetSetting writes into a map so a save-then-get round
// trip can be asserted.
type coreStorageHTTPFakeStore struct {
	store.Store
	kv map[string]string
}

func newCoreStorageHTTPFakeStore() *coreStorageHTTPFakeStore {
	return &coreStorageHTTPFakeStore{kv: map[string]string{}}
}

func (f *coreStorageHTTPFakeStore) GetSetting(key string) (string, error) { return f.kv[key], nil }
func (f *coreStorageHTTPFakeStore) SetSetting(key, value string) error {
	f.kv[key] = value
	return nil
}

func TestCoreStorage_SaveThenGet_BlanksSecretAndKeepsExisting(t *testing.T) {
	fs := newCoreStorageHTTPFakeStore()
	h := NewCoreStorageHandler(&AppState{Store: fs})

	// Save an s3 config with a secret.
	body, _ := json.Marshal(CoreStorageConfig{
		Backend: "s3", S3Bucket: "b", S3AccessKey: "k", S3SecretKey: "sekret",
	})
	rw := httptest.NewRecorder()
	h.SaveConfig(rw, httptest.NewRequest(http.MethodPost, "/api/settings/core-storage", bytes.NewReader(body)))
	if rw.Code != http.StatusOK {
		t.Fatalf("SaveConfig status = %d, want 200 (%s)", rw.Code, rw.Body.String())
	}
	if fs.kv[keyCoreStorageS3SecretKey] != "sekret" {
		t.Fatalf("secret not persisted, got %q", fs.kv[keyCoreStorageS3SecretKey])
	}

	// GET must not leak the secret but must flag it as set.
	rw = httptest.NewRecorder()
	h.GetConfig(rw, httptest.NewRequest(http.MethodGet, "/api/settings/core-storage", nil))
	var got struct {
		Settings CoreStorageConfig `json:"settings"`
	}
	_ = json.Unmarshal(rw.Body.Bytes(), &got)
	if got.Settings.S3SecretKey != "" {
		t.Errorf("GET leaked secret: %q", got.Settings.S3SecretKey)
	}
	if !got.Settings.S3SecretSet {
		t.Errorf("GET S3SecretSet = false, want true")
	}

	// Re-save with a blank secret: the stored one must survive, other fields update.
	body, _ = json.Marshal(CoreStorageConfig{Backend: "s3", S3Bucket: "b2", S3AccessKey: "k", S3SecretKey: ""})
	rw = httptest.NewRecorder()
	h.SaveConfig(rw, httptest.NewRequest(http.MethodPost, "/api/settings/core-storage", bytes.NewReader(body)))
	if rw.Code != http.StatusOK {
		t.Fatalf("re-save status = %d, want 200 (%s)", rw.Code, rw.Body.String())
	}
	if fs.kv[keyCoreStorageS3SecretKey] != "sekret" {
		t.Errorf("blank re-save wiped the secret, got %q", fs.kv[keyCoreStorageS3SecretKey])
	}
	if fs.kv[keyCoreStorageS3Bucket] != "b2" {
		t.Errorf("bucket not updated on re-save, got %q", fs.kv[keyCoreStorageS3Bucket])
	}
}

func TestCoreStorage_SaveRejectsInvalid(t *testing.T) {
	fs := newCoreStorageHTTPFakeStore()
	h := NewCoreStorageHandler(&AppState{Store: fs})
	body, _ := json.Marshal(CoreStorageConfig{Backend: "path", Path: "relative", PathConfirmed: true})
	rw := httptest.NewRecorder()
	h.SaveConfig(rw, httptest.NewRequest(http.MethodPost, "/api/settings/core-storage", bytes.NewReader(body)))
	if rw.Code != http.StatusBadRequest {
		t.Fatalf("SaveConfig invalid status = %d, want 400", rw.Code)
	}
	if fs.kv[keyCoreStorageBackend] != "" {
		t.Errorf("invalid config was partially persisted: %v", fs.kv)
	}
}

// TestCoreStorage_SaveRejectsInvalid_DoesNotWipeExistingValidConfig guards the
// "reject before persist" contract from the other direction: an admin who
// already has a valid config saved and submits a bad edit must not lose the
// previously-good settings.
func TestCoreStorage_SaveRejectsInvalid_DoesNotWipeExistingValidConfig(t *testing.T) {
	fs := newCoreStorageHTTPFakeStore()
	fs.kv[keyCoreStorageBackend] = "path"
	fs.kv[keyCoreStoragePath] = "/mnt/shared"
	fs.kv[keyCoreStoragePathConfirm] = "true"
	h := NewCoreStorageHandler(&AppState{Store: fs})

	body, _ := json.Marshal(CoreStorageConfig{Backend: "path", Path: "relative", PathConfirmed: true})
	rw := httptest.NewRecorder()
	h.SaveConfig(rw, httptest.NewRequest(http.MethodPost, "/api/settings/core-storage", bytes.NewReader(body)))
	if rw.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rw.Code)
	}
	if fs.kv[keyCoreStoragePath] != "/mnt/shared" {
		t.Errorf("invalid save clobbered the existing good path, got %q", fs.kv[keyCoreStoragePath])
	}
}

func TestCoreStorage_GetConfig_NeverConfigured(t *testing.T) {
	fs := newCoreStorageHTTPFakeStore()
	h := NewCoreStorageHandler(&AppState{Store: fs})
	rw := httptest.NewRecorder()
	h.GetConfig(rw, httptest.NewRequest(http.MethodGet, "/api/settings/core-storage", nil))
	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rw.Code)
	}
	var got struct {
		Settings CoreStorageConfig `json:"settings"`
	}
	_ = json.Unmarshal(rw.Body.Bytes(), &got)
	if got.Settings.S3SecretSet {
		t.Errorf("S3SecretSet = true on a never-configured store, want false")
	}
}

// testConnectionProbeDir returns an absolute-looking POSIX path ("/...")
// that os.MkdirAll can still create on a Windows dev box (Windows treats a
// leading "/" as drive-root-relative), matching validateCoreStorageConfig's
// deliberately Linux-only absolute-path check (see its doc comment). Same
// convention as the "/mnt/shared" literal already used in
// core_storage_builder_test.go; a real t.TempDir() would fail the "/" check
// on Windows and was deliberately not used here for that reason.
func testConnectionProbeDir(t *testing.T) string {
	t.Helper()
	root := filepath.Join("/", "dylaris-test-tmp")
	dir := filepath.ToSlash(filepath.Join(root, t.Name()))
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", dir, err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	return dir
}

func TestCoreStorage_TestConnection_PathProbe(t *testing.T) {
	fs := newCoreStorageHTTPFakeStore()
	dir := testConnectionProbeDir(t)
	fs.kv[keyCoreStorageBackend] = "path"
	fs.kv[keyCoreStoragePath] = dir
	fs.kv[keyCoreStoragePathConfirm] = "true"
	h := NewCoreStorageHandler(&AppState{Store: fs})

	rw := httptest.NewRecorder()
	h.TestConnection(rw, httptest.NewRequest(http.MethodPost, "/api/settings/core-storage/test", nil))
	if rw.Code != http.StatusOK {
		t.Fatalf("TestConnection status = %d, want 200 (%s)", rw.Code, rw.Body.String())
	}
	if !strings.Contains(rw.Body.String(), "\"ok\":true") {
		t.Errorf("TestConnection body = %s, want ok:true", rw.Body.String())
	}
}

// TestCoreStorage_TestConnection_UsesCandidateFromBody_NotStoredConfig is the
// key regression guard for "test before saving": the store has no valid
// config at all, so a success here can only come from the request body.
func TestCoreStorage_TestConnection_UsesCandidateFromBody_NotStoredConfig(t *testing.T) {
	fs := newCoreStorageHTTPFakeStore()
	dir := testConnectionProbeDir(t)
	h := NewCoreStorageHandler(&AppState{Store: fs})

	body, _ := json.Marshal(CoreStorageConfig{Backend: "path", Path: dir, PathConfirmed: true})
	rw := httptest.NewRecorder()
	h.TestConnection(rw, httptest.NewRequest(http.MethodPost, "/api/settings/core-storage/test", bytes.NewReader(body)))
	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rw.Code, rw.Body.String())
	}
	if !strings.Contains(rw.Body.String(), "\"ok\":true") {
		t.Errorf("body = %s, want ok:true", rw.Body.String())
	}
}

// TestCoreStorage_TestConnection_InvalidCandidateReportsFailure_NotHTTPError
// asserts an invalid candidate reports ok:false in a 200 JSON body (a "test"
// result), not a hard HTTP error - the panel renders this inline.
func TestCoreStorage_TestConnection_InvalidCandidateReportsFailure_NotHTTPError(t *testing.T) {
	fs := newCoreStorageHTTPFakeStore()
	h := NewCoreStorageHandler(&AppState{Store: fs})
	body, _ := json.Marshal(CoreStorageConfig{Backend: "path", Path: "relative", PathConfirmed: true})
	rw := httptest.NewRecorder()
	h.TestConnection(rw, httptest.NewRequest(http.MethodPost, "/api/settings/core-storage/test", bytes.NewReader(body)))
	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (invalid candidate reports ok:false in-band)", rw.Code)
	}
	if !strings.Contains(rw.Body.String(), "\"ok\":false") {
		t.Errorf("body = %s, want ok:false", rw.Body.String())
	}
}

// --- mergeCoreStorageCandidate (pure logic backing both SaveConfig and
// TestConnection's "blank secret keeps the stored one" rule) ---

func TestMergeCoreStorageCandidate(t *testing.T) {
	existing := CoreStorageConfig{Backend: "s3", S3Bucket: "old", S3AccessKey: "k", S3SecretKey: "stored-secret"}

	cases := []struct {
		name      string
		candidate CoreStorageConfig
		want      CoreStorageConfig
	}{
		{
			name:      "empty candidate backend reuses the stored config entirely",
			candidate: CoreStorageConfig{},
			want:      existing,
		},
		{
			name:      "candidate with blank secret keeps the stored secret",
			candidate: CoreStorageConfig{Backend: "s3", S3Bucket: "new", S3AccessKey: "k"},
			want:      CoreStorageConfig{Backend: "s3", S3Bucket: "new", S3AccessKey: "k", S3SecretKey: "stored-secret"},
		},
		{
			name:      "candidate with its own secret is not overridden",
			candidate: CoreStorageConfig{Backend: "s3", S3Bucket: "new", S3AccessKey: "k2", S3SecretKey: "new-secret"},
			want:      CoreStorageConfig{Backend: "s3", S3Bucket: "new", S3AccessKey: "k2", S3SecretKey: "new-secret"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := mergeCoreStorageCandidate(c.candidate, existing)
			if got != c.want {
				t.Errorf("mergeCoreStorageCandidate() = %+v, want %+v", got, c.want)
			}
		})
	}
}

// --- probeStorageProvider (write+read+delete probe logic), exercised
// against a hand-written fake so the mismatch/error cleanup paths are
// deterministic and do not depend on a real filesystem or network backend. ---

type fakeProbeProvider struct {
	readBack     string
	getErr       error
	deleteErr    error
	deleteCalled int
}

func (f *fakeProbeProvider) ListFiles(string) ([]storage.FileInfo, error)      { return nil, nil }
func (f *fakeProbeProvider) CreateDir(string) error                           { return nil }
func (f *fakeProbeProvider) CopyToLocal(string, string) error                 { return nil }
func (f *fakeProbeProvider) DownloadURL(string, time.Duration) (string, error) { return "", nil }
func (f *fakeProbeProvider) WriteFile(string, io.Reader) error                { return nil }

func (f *fakeProbeProvider) GetFile(string) (io.ReadCloser, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return io.NopCloser(strings.NewReader(f.readBack)), nil
}

func (f *fakeProbeProvider) DeletePath(string) error {
	f.deleteCalled++
	return f.deleteErr
}

func TestProbeStorageProvider_Success(t *testing.T) {
	fp := &fakeProbeProvider{readBack: coreStorageProbePayload}
	ok, msg := probeStorageProvider(fp)
	if !ok {
		t.Fatalf("ok = false, want true (%s)", msg)
	}
	if fp.deleteCalled != 1 {
		t.Errorf("DeletePath called %d times, want 1", fp.deleteCalled)
	}
}

func TestProbeStorageProvider_MismatchStillDeletesProbe(t *testing.T) {
	fp := &fakeProbeProvider{readBack: "not-what-was-written"}
	ok, msg := probeStorageProvider(fp)
	if ok {
		t.Fatalf("ok = true, want false on mismatch (%s)", msg)
	}
	if fp.deleteCalled != 1 {
		t.Errorf("DeletePath called %d times on mismatch, want 1 (probe must still be cleaned up)", fp.deleteCalled)
	}
}

func TestProbeStorageProvider_ReadErrorStillDeletesProbe(t *testing.T) {
	fp := &fakeProbeProvider{getErr: errors.New("boom")}
	ok, msg := probeStorageProvider(fp)
	if ok {
		t.Fatalf("ok = true, want false on read error (%s)", msg)
	}
	if fp.deleteCalled != 1 {
		t.Errorf("DeletePath called %d times on read error, want 1", fp.deleteCalled)
	}
}

func TestProbeStorageProvider_DeleteFailureAfterMatchReportsFailure(t *testing.T) {
	fp := &fakeProbeProvider{readBack: coreStorageProbePayload, deleteErr: errors.New("locked")}
	ok, msg := probeStorageProvider(fp)
	if ok {
		t.Fatalf("ok = true, want false when cleanup delete fails (%s)", msg)
	}
}
