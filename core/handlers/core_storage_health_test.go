package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"dylaris-core/storage"
)

// unhealthyGate returns a gate that has already been tripped, without starting
// a watcher: ReportFailure is the immediate-trip path and needs no probe loop.
func unhealthyGate(t *testing.T) *storage.Gate {
	t.Helper()
	g := storage.NewGate()
	g.ReportFailure(&os.PathError{Op: "stat", Path: "/mnt/shared", Err: syscall.EIO})
	if ok, _ := g.Healthy(); ok {
		t.Fatal("gate did not trip on an unreachable errno")
	}
	return g
}

// TestNewStorageProviderForConfigChecksGateBeforeMkdirAll pins the ORDERING,
// which is the entire point of putting the check here. newStorageProviderForConfig
// runs on every provider build and providers are built per request, so on a
// mount that has stopped answering its os.MkdirAll is itself the call that
// blocks - before any provider method is ever reached. The proof is that the
// directory does not exist afterwards: had the check come after, MkdirAll would
// have run (and, on a real wedged mount, hung) first.
func TestNewStorageProviderForConfigChecksGateBeforeMkdirAll(t *testing.T) {
	base := filepath.Join(t.TempDir(), "wedged")
	cfg := CoreStorageConfig{Backend: "path", Path: base, PathConfirmed: true}

	prov, err := newStorageProviderForConfig(cfg, CoreStoragePrefixLibrary, unhealthyGate(t), nil)
	if err == nil {
		t.Fatal("newStorageProviderForConfig err = nil behind an unhealthy gate, want an error")
	}
	if prov != nil {
		t.Fatalf("newStorageProviderForConfig = %T, want a nil provider on error", prov)
	}
	if !errors.Is(err, storage.ErrBackendUnreachable) {
		t.Errorf("err = %v, want it to match storage.ErrBackendUnreachable", err)
	}
	if _, statErr := os.Stat(base); !os.IsNotExist(statErr) {
		t.Fatalf("%q exists, so MkdirAll ran despite an unhealthy gate; the check must come BEFORE it", base)
	}
}

func TestNewStorageProviderForConfigHealthyGateBuildsGatedProvider(t *testing.T) {
	base := t.TempDir()
	cfg := CoreStorageConfig{Backend: "path", Path: base, PathConfirmed: true}

	prov, err := newStorageProviderForConfig(cfg, CoreStoragePrefixLibrary, storage.NewGate(), nil)
	if err != nil {
		t.Fatalf("newStorageProviderForConfig: %v", err)
	}
	want := filepath.Join(base, CoreStoragePrefixLibrary)
	if _, statErr := os.Stat(want); statErr != nil {
		t.Errorf("root %q was not created: %v", want, statErr)
	}
	lp, ok := storage.UnwrapGated(prov).(*storage.LocalProvider)
	if !ok {
		t.Fatalf("underlying provider = %T, want *storage.LocalProvider", storage.UnwrapGated(prov))
	}
	if lp.BasePath != want {
		t.Errorf("BasePath = %q, want %q", lp.BasePath, want)
	}
}

// TestNewStorageProviderForConfigNeverGatesS3: the watchdog watches ONE host
// path. S3 has no relationship to it, and its failures are the SDK's to judge.
func TestNewStorageProviderForConfigNeverGatesS3(t *testing.T) {
	cfg := CoreStorageConfig{
		Backend:     "s3",
		S3Bucket:    "my-bucket",
		S3AccessKey: "AKIAEXAMPLE",
		S3SecretKey: "s3cr3t",
	}
	prov, err := newStorageProviderForConfig(cfg, CoreStoragePrefixLibrary, unhealthyGate(t), nil)
	if err != nil {
		t.Fatalf("s3 build behind an unhealthy path gate: %v, want it unaffected", err)
	}
	if prov == nil {
		t.Fatal("s3 build returned a nil provider")
	}
	if storage.UnwrapGated(prov) != prov {
		t.Error("the s3 provider came back wrapped by the host-path gate")
	}
}

// TestBuildCoreStorageProviderSurfacesUnreachable drives the same thing through
// the per-request entry point every handler actually calls.
func TestBuildCoreStorageProviderSurfacesUnreachable(t *testing.T) {
	const cfgPath = "/mnt/shared-that-is-never-created"
	s := &AppState{
		Store: &coreStorageFakeStore{values: map[string]string{
			keyCoreStorageBackend:     "path",
			keyCoreStoragePath:        cfgPath,
			keyCoreStoragePathConfirm: "true",
		}},
		StorageGate: unhealthyGate(t),
	}

	prov, err := s.buildCoreStorageProvider(CoreStoragePrefixLibrary)
	if err == nil || prov != nil {
		t.Fatalf("buildCoreStorageProvider = (%T, %v), want (nil, an error)", prov, err)
	}
	if !errors.Is(err, storage.ErrBackendUnreachable) {
		t.Errorf("err = %v, want it to match storage.ErrBackendUnreachable", err)
	}
	if _, statErr := os.Stat(cfgPath); !os.IsNotExist(statErr) {
		t.Errorf("%q exists; nothing may be created on the wedged path", cfgPath)
	}
}

// TestCoreStorageUnavailableResponse pins the two 503s apart. They ask the
// operator for opposite things: one to go and configure storage, the other to
// look at a mount while leaving a correct config alone.
func TestCoreStorageUnavailableResponse(t *testing.T) {
	tests := []struct {
		name         string
		err          error
		wantError    string
		wantContains string
	}{
		{
			name:         "not configured",
			err:          errors.New("core storage: path is required for the filesystem backend"),
			wantError:    "core_storage_unavailable",
			wantContains: "not configured",
		},
		{
			name:         "no error at all still reads as not configured",
			err:          nil,
			wantError:    "core_storage_unavailable",
			wantContains: "not configured",
		},
		{
			name:         "unreachable",
			err:          fmt.Errorf("%w: stat /mnt/shared: input/output error", storage.ErrBackendUnreachable),
			wantError:    "core_storage_unreachable",
			wantContains: "UNREACHABLE",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rw := httptest.NewRecorder()
			coreStorageUnavailableResponse(rw, tt.err)

			if rw.Code != http.StatusServiceUnavailable {
				t.Errorf("status = %d, want %d", rw.Code, http.StatusServiceUnavailable)
			}
			// A 404 here would tell the operator the file is gone when the
			// truth is that Core cannot see the mount.
			if rw.Code == http.StatusNotFound || rw.Code == http.StatusInternalServerError {
				t.Fatal("an unreachable backend must not be reported as a missing file or a generic failure")
			}
			// The router-level gate's shape must stay distinguishable from this one.
			if h := rw.Header().Get("X-Feature-Disabled"); h != "" {
				t.Errorf("X-Feature-Disabled = %q; that header belongs to featureDisabledResponse alone", h)
			}

			var body struct {
				Success bool   `json:"success"`
				Error   string `json:"error"`
				Message string `json:"message"`
			}
			if err := json.Unmarshal(rw.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode body %q: %v", rw.Body.String(), err)
			}
			if body.Success {
				t.Error("success = true on a 503")
			}
			if body.Error != tt.wantError {
				t.Errorf("error = %q, want %q", body.Error, tt.wantError)
			}
			if !strings.Contains(body.Message, tt.wantContains) {
				t.Errorf("message = %q, want it to contain %q", body.Message, tt.wantContains)
			}
		})
	}
}

// TestSyncStorageGateFollowsTheConfiguredBackend: a verdict about a host path
// must not keep refusing requests once the platform stopped using that path.
func TestSyncStorageGateFollowsTheConfiguredBackend(t *testing.T) {
	tests := []struct {
		name   string
		values map[string]string
	}{
		{"switched to s3", map[string]string{
			keyCoreStorageBackend:  "s3",
			keyCoreStorageS3Bucket: "my-bucket",
		}},
		{"nothing configured", map[string]string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &AppState{
				Store:       &coreStorageFakeStore{values: tt.values},
				StorageGate: unhealthyGate(t),
			}
			s.SyncStorageGate()
			if ok, cause := s.StorageGate.Healthy(); !ok {
				t.Errorf("Healthy() = (false, %v); a stale host-path verdict must not survive %s", cause, tt.name)
			}
		})
	}
}

// TestSyncStorageGateIsNilSafe: an AppState built without a gate (only ever a
// test) must take the same path as the wired-up one rather than panic.
func TestSyncStorageGateIsNilSafe(t *testing.T) {
	s := &AppState{Store: &coreStorageFakeStore{values: map[string]string{
		keyCoreStorageBackend:     "path",
		keyCoreStoragePath:        "/mnt/shared",
		keyCoreStoragePathConfirm: "true",
	}}}
	s.SyncStorageGate()
	if ok, _ := s.StorageGate.Healthy(); !ok {
		t.Error("a nil gate must read as healthy")
	}
}
