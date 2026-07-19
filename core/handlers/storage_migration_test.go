package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dylaris-core/models"
	"dylaris-core/services"
	"dylaris-core/store"

	"github.com/gorilla/mux"
)

// storageMigrationFakeStore serves settings, backup storages and manifests
// from maps. Local to this file; never redeclared.
type storageMigrationFakeStore struct {
	store.Store
	kv        map[string]string
	storages  []models.BackupStorage
	manifests []models.StorageManifest
	entries   map[int][]models.StorageManifestEntry
	deleted   []int
}

func newStorageMigrationFakeStore() *storageMigrationFakeStore {
	return &storageMigrationFakeStore{
		kv:      map[string]string{},
		entries: map[int][]models.StorageManifestEntry{},
	}
}

func (f *storageMigrationFakeStore) GetSetting(k string) (string, error) { return f.kv[k], nil }
func (f *storageMigrationFakeStore) SetSetting(k, v string) error {
	f.kv[k] = v
	return nil
}
func (f *storageMigrationFakeStore) ListBackupStorages() ([]models.BackupStorage, error) {
	return f.storages, nil
}
func (f *storageMigrationFakeStore) ListStorageManifests(dataSet string, limit int) ([]models.StorageManifest, error) {
	if dataSet == "" {
		return f.manifests, nil
	}
	out := []models.StorageManifest{}
	for _, m := range f.manifests {
		if m.DataSet == dataSet {
			out = append(out, m)
		}
	}
	return out, nil
}
func (f *storageMigrationFakeStore) GetStorageManifest(id int) (*models.StorageManifest, error) {
	for i := range f.manifests {
		if f.manifests[i].ID == id {
			return &f.manifests[i], nil
		}
	}
	return nil, nil
}
func (f *storageMigrationFakeStore) ListStorageManifestEntries(id int) ([]models.StorageManifestEntry, error) {
	return f.entries[id], nil
}
func (f *storageMigrationFakeStore) DeleteStorageManifest(id int) error {
	f.deleted = append(f.deleted, id)
	return nil
}
func (f *storageMigrationFakeStore) InsertAuditIdentity(*models.AuditEventIdentity) error { return nil }

// storageMigrationTestRoot returns a Linux-absolute test-scoped directory.
// validateCoreStorageConfig deliberately requires a leading "/" (the config
// always names a path on the Core container's filesystem, which is Linux-only
// - see its doc comment), so a raw t.TempDir() - NOT "/"-prefixed on Windows -
// would fail that check before ResolveTarget ever reached the same-location
// comparison these tests exercise, rather than genuinely exercising it. Same
// convention as testConnectionProbeDir in core_storage_http_test.go: a
// leading "/" resolves under the current drive root on Windows (os.MkdirAll
// still succeeds there) while satisfying the string-prefix check either way.
func storageMigrationTestRoot(t *testing.T, name string) string {
	t.Helper()
	root := filepath.Join("/", "dylaris-storage-migration-test-tmp")
	dir := filepath.ToSlash(filepath.Join(root, t.Name(), name))
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", dir, err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	return dir
}

func configuredPathStorage(t *testing.T, fs *storageMigrationFakeStore) {
	t.Helper()
	fs.kv[keyCoreStorageBackend] = "path"
	fs.kv[keyCoreStoragePath] = "/mnt/shared"
	fs.kv[keyCoreStoragePathConfirm] = "true"
}

func TestCoreStorageBackendLabel_CarriesNoCredentials(t *testing.T) {
	cases := []struct {
		name string
		cfg  CoreStorageConfig
		sub  string
		want string
	}{
		{
			"path",
			CoreStorageConfig{Backend: "path", Path: "/mnt/shared"},
			"library",
			"path:/mnt/shared/library",
		},
		{
			"s3 with prefix",
			CoreStorageConfig{Backend: "s3", S3Endpoint: "https://s3.example.com", S3Bucket: "dylaris", S3Prefix: "prod"},
			"modpacks",
			"s3:https://s3.example.com/dylaris/prod/modpacks",
		},
		{
			"s3 without prefix",
			CoreStorageConfig{Backend: "s3", S3Endpoint: "https://s3.example.com", S3Bucket: "dylaris"},
			"library",
			"s3:https://s3.example.com/dylaris/library",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := coreStorageBackendLabel(c.cfg, c.sub)
			if got != c.want {
				t.Fatalf("coreStorageBackendLabel = %q, want %q", got, c.want)
			}
		})
	}
}

func TestCoreStorageBackendLabel_NeverLeaksTheSecret(t *testing.T) {
	cfg := CoreStorageConfig{
		Backend: "s3", S3Endpoint: "https://s3.example.com", S3Bucket: "b",
		S3AccessKey: "AKIAEXAMPLEKEY", S3SecretKey: "SUPERSECRETVALUE",
	}
	label := coreStorageBackendLabel(cfg, "library")
	for _, secret := range []string{"AKIAEXAMPLEKEY", "SUPERSECRETVALUE"} {
		if strings.Contains(label, secret) {
			t.Fatalf("backend label leaked a credential: %q", label)
		}
	}
}

func TestStorageDataSetResolver_ListMarksEveryDataSetMigratable(t *testing.T) {
	fs := newStorageMigrationFakeStore()
	configuredPathStorage(t, fs)
	fs.storages = []models.BackupStorage{
		{ID: 1, Name: "Hetzner S3", Provider: "s3"},
		{ID: 2, Name: "On the node", Provider: "node-local"},
	}
	res := NewStorageDataSetResolver(&AppState{Store: fs})

	sets, err := res.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	byID := map[string]bool{}
	adHoc := map[string]bool{}
	notes := map[string]string{}
	for _, s := range sets {
		byID[s.ID] = s.Migratable
		adHoc[s.ID] = s.SupportsTargetConfig
		notes[s.ID] = s.Note
	}
	// The Core-file-storage data sets ARE migratable: their target is an
	// ad-hoc storage config supplied in the start request, not the saved
	// config (which describes where they already live).
	for _, id := range []string{"library", "ticket-attachments", "ticket-backups", "modpacks"} {
		if _, ok := byID[id]; !ok {
			t.Fatalf("%s missing from the overview", id)
		}
		if !byID[id] {
			t.Errorf("%s is marked not migratable; it moves to an ad-hoc target config", id)
		}
		if !adHoc[id] {
			t.Errorf("%s must advertise SupportsTargetConfig: it is backed by one settings-configured backend", id)
		}
	}
	if !byID["server-backups:1"] {
		t.Error("the s3 backup storage row must be migratable")
	}
	if adHoc["server-backups:1"] {
		t.Error("a backup storage row targets another ROW, not an ad-hoc config; backups are already multi-storage")
	}
	if byID["server-backups:2"] {
		t.Error("a node-local row must NOT be migratable; those archives live on Node disks")
	}
	if notes["server-backups:2"] == "" {
		t.Error("the node-local row has no explanatory note")
	}
	if notes["modpacks"] == "" {
		t.Error("modpacks must carry the database-referenced-only note")
	}
}

func TestStorageDataSetResolver_ResolveTargetRefusesTheSourceLocation(t *testing.T) {
	// The refusal must happen here, at resolve time, so Start fails before a
	// single object is read. Discovering it mid-copy would mean the engine had
	// already rewritten source objects onto themselves.
	root := storageMigrationTestRoot(t, "root")
	fs := newStorageMigrationFakeStore()
	fs.kv[keyCoreStorageBackend] = "path"
	fs.kv[keyCoreStoragePath] = root
	fs.kv[keyCoreStoragePathConfirm] = "true"
	res := NewStorageDataSetResolver(&AppState{Store: fs})

	_, _, err := res.ResolveTarget(context.Background(), "library", services.StorageTargetConfig{
		Backend: "path", Path: root, PathConfirmed: true,
	})
	if !errors.Is(err, ErrTargetSameLocation) {
		t.Fatalf("ResolveTarget err = %v, want ErrTargetSameLocation", err)
	}
}

func TestStorageDataSetResolver_ResolveTargetBuildsAnUnsavedTarget(t *testing.T) {
	srcRoot := storageMigrationTestRoot(t, "src")
	tgtRoot := storageMigrationTestRoot(t, "tgt")
	fs := newStorageMigrationFakeStore()
	fs.kv[keyCoreStorageBackend] = "path"
	fs.kv[keyCoreStoragePath] = srcRoot
	fs.kv[keyCoreStoragePathConfirm] = "true"
	res := NewStorageDataSetResolver(&AppState{Store: fs})

	ds, label, err := res.ResolveTarget(context.Background(), "library", services.StorageTargetConfig{
		Backend: "path", Path: tgtRoot, PathConfirmed: true,
	})
	if err != nil {
		t.Fatalf("ResolveTarget: %v", err)
	}
	if ds == nil {
		t.Fatal("ResolveTarget returned a nil DataSet")
	}
	if !strings.Contains(label, tgtRoot) || !strings.Contains(label, "library") {
		t.Errorf("label = %q, want it to name the target root and the sub-prefix", label)
	}
	// The saved config must be untouched: resolving a target is not a switch.
	if fs.kv[keyCoreStoragePath] != srcRoot {
		t.Errorf("the saved config changed during ResolveTarget: %q", fs.kv[keyCoreStoragePath])
	}
}

func TestStorageDataSetResolver_ResolveTargetRejectsAnInvalidConfig(t *testing.T) {
	fs := newStorageMigrationFakeStore()
	configuredPathStorage(t, fs)
	res := NewStorageDataSetResolver(&AppState{Store: fs})

	if _, _, err := res.ResolveTarget(context.Background(), "library", services.StorageTargetConfig{
		Backend: "s3", S3Bucket: "b", // no credentials
	}); err == nil {
		t.Fatal("ResolveTarget err = nil, want validateCoreStorageConfig to reject it")
	}
}

func TestStorageDataSetResolver_ResolveTargetRejectsARowBackedDataSet(t *testing.T) {
	fs := newStorageMigrationFakeStore()
	configuredPathStorage(t, fs)
	fs.storages = []models.BackupStorage{{ID: 1, Name: "Hetzner S3", Provider: "s3"}}
	res := NewStorageDataSetResolver(&AppState{Store: fs})

	if _, _, err := res.ResolveTarget(context.Background(), "server-backups:1", services.StorageTargetConfig{
		Backend: "path", Path: t.TempDir(), PathConfirmed: true,
	}); err == nil {
		t.Fatal("ResolveTarget err = nil, want a refusal: server backups target another storage ROW, not an ad-hoc config")
	}
}

func TestStorageDataSetResolver_SwitchConfigPersistsTheTarget(t *testing.T) {
	fs := newStorageMigrationFakeStore()
	configuredPathStorage(t, fs)
	st := &AppState{Store: fs}
	res := NewStorageDataSetResolver(st)

	err := res.SwitchConfig(context.Background(), "library", services.StorageTargetConfig{
		Backend: "s3", S3Endpoint: "https://new.example.com", S3Bucket: "dylaris-new",
		S3Region: "eu-central-1", S3AccessKey: "AKIA-NEW", S3SecretKey: "NEW-SECRET",
		S3Prefix: "prod",
	})
	if err != nil {
		t.Fatalf("SwitchConfig: %v", err)
	}
	got := st.LoadCoreStorageConfig()
	if got.Backend != "s3" || got.S3Bucket != "dylaris-new" || got.S3Prefix != "prod" {
		t.Fatalf("saved config = %+v, want the target", got)
	}
	// This IS the one place the secret is allowed to land.
	if got.S3SecretKey != "NEW-SECRET" {
		t.Errorf("stored secret = %q, want the target's secret persisted here", got.S3SecretKey)
	}
}

func TestStorageMigrationHandler_StartNeverEchoesOrAuditsTheTargetSecret(t *testing.T) {
	// Safety invariant 6 at the HTTP boundary. The request carries a live
	// secret; the 400 body must not repeat it back, and neither must anything
	// the handler writes. The service is deliberately nil so this stays a pure
	// input check that cannot pass by accident of Redis being down.
	const secret = "SENTINEL-HTTP-SECRET"
	fs := newStorageMigrationFakeStore()
	configuredPathStorage(t, fs)
	h := NewStorageMigrationHandler(&AppState{Store: fs})

	body, _ := json.Marshal(map[string]interface{}{
		"kind": "migrate", "dataSet": "library",
		"targetConfig": map[string]interface{}{
			"backend": "s3", "s3Endpoint": "https://new.example.com",
			"s3Bucket": "dylaris-new", "s3AccessKey": "AKIA", "s3SecretKey": secret,
		},
		"verifyMode": "sample", "deleteSource": true,
	})
	rw := httptest.NewRecorder()
	h.Start(rw, httptest.NewRequest(http.MethodPost, "/api/admin/storage/migration", bytes.NewReader(body)))

	if rw.Code != http.StatusBadRequest {
		t.Fatalf("Start status = %d, want 400 (deleteSource under a sample verify)", rw.Code)
	}
	if strings.Contains(rw.Body.String(), secret) {
		t.Fatalf("the error response echoed the target secret: %s", rw.Body.String())
	}
}

func TestStorageMigrationAuditPayload_CarriesNoTargetSecret(t *testing.T) {
	// The audit payload is built from the request, so it is the other place a
	// secret could leak. It records the credential-free description only.
	payload := storageMigrationAuditPayload(services.StorageMigrationRequest{
		Kind: services.StorageJobMigrate, DataSet: "library", VerifyMode: "full", DeleteSource: true,
		TargetConfig: &services.StorageTargetConfig{
			Backend: "s3", S3Endpoint: "https://new.example.com", S3Bucket: "dylaris-new",
			S3AccessKey: "AKIAEXAMPLEKEY", S3SecretKey: "SENTINEL-AUDIT-SECRET", S3Prefix: "prod",
		},
	}, "job-1")

	blob, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal audit payload: %v", err)
	}
	for _, secret := range []string{"SENTINEL-AUDIT-SECRET", "AKIAEXAMPLEKEY"} {
		if strings.Contains(string(blob), secret) {
			t.Fatalf("the audit payload leaks %q: %s", secret, blob)
		}
	}
	// Positive control: it still says WHERE the data went.
	if !strings.Contains(string(blob), "dylaris-new") {
		t.Errorf("the audit payload does not record the target at all: %s", blob)
	}
}

func TestStorageMigrationHandler_StartRejectsDeleteUnderSampleWith400(t *testing.T) {
	// Safety invariant 2 at the API boundary, not only in the UI. This must
	// hold even with no migration service wired, so it is a pure input check.
	fs := newStorageMigrationFakeStore()
	configuredPathStorage(t, fs)
	h := NewStorageMigrationHandler(&AppState{Store: fs})

	body, _ := json.Marshal(map[string]interface{}{
		"kind": "migrate", "dataSet": "modpacks", "targetDataSet": ModpacksCoreStorageDataSetID,
		"verifyMode": "sample", "deleteSource": true,
	})
	rw := httptest.NewRecorder()
	h.Start(rw, httptest.NewRequest(http.MethodPost, "/api/admin/storage/migration", bytes.NewReader(body)))

	if rw.Code != http.StatusBadRequest {
		t.Fatalf("Start status = %d, want 400 (%s)", rw.Code, rw.Body.String())
	}
	if !strings.Contains(rw.Body.String(), "full") {
		t.Errorf("the 400 body does not explain the full-verify requirement: %s", rw.Body.String())
	}
}

func TestStorageMigrationHandler_StartRejectsAnUnknownKind(t *testing.T) {
	fs := newStorageMigrationFakeStore()
	configuredPathStorage(t, fs)
	h := NewStorageMigrationHandler(&AppState{Store: fs})

	body, _ := json.Marshal(map[string]interface{}{"kind": "wipe", "dataSet": "modpacks"})
	rw := httptest.NewRecorder()
	h.Start(rw, httptest.NewRequest(http.MethodPost, "/api/admin/storage/migration", bytes.NewReader(body)))
	if rw.Code != http.StatusBadRequest {
		t.Fatalf("Start status = %d, want 400", rw.Code)
	}
}

func TestStorageMigrationHandler_GetJobWithoutTheServiceIs503(t *testing.T) {
	h := NewStorageMigrationHandler(&AppState{Store: newStorageMigrationFakeStore()})
	rw := httptest.NewRecorder()
	h.GetJob(rw, httptest.NewRequest(http.MethodGet, "/api/admin/storage/migration", nil))
	if rw.Code != http.StatusServiceUnavailable {
		t.Fatalf("GetJob status = %d, want 503 when the service is not wired", rw.Code)
	}
}

func TestStorageMigrationHandler_Overview(t *testing.T) {
	fs := newStorageMigrationFakeStore()
	configuredPathStorage(t, fs)
	fs.manifests = []models.StorageManifest{
		{ID: 7, DataSet: "modpacks", Algo: "sha256", CapturedAt: time.Now(), ObjectCount: 12, TotalBytes: 900},
	}
	h := NewStorageMigrationHandler(&AppState{Store: fs})

	rw := httptest.NewRecorder()
	h.Overview(rw, httptest.NewRequest(http.MethodGet, "/api/admin/storage/overview", nil))
	if rw.Code != http.StatusOK {
		t.Fatalf("Overview status = %d (%s)", rw.Code, rw.Body.String())
	}
	var got struct {
		Success  bool `json:"success"`
		DataSets []struct {
			ID             string `json:"id"`
			Label          string `json:"label"`
			BackendLabel   string `json:"backendLabel"`
			Migratable     bool   `json:"migratable"`
			Note           string `json:"note"`
			LatestManifest *struct {
				ID          int   `json:"id"`
				ObjectCount int64 `json:"objectCount"`
			} `json:"latestManifest"`
		} `json:"dataSets"`
	}
	if err := json.Unmarshal(rw.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v (%s)", err, rw.Body.String())
	}
	if !got.Success || len(got.DataSets) == 0 {
		t.Fatalf("Overview body = %s", rw.Body.String())
	}
	for _, ds := range got.DataSets {
		if ds.ID == "modpacks" {
			if ds.LatestManifest == nil || ds.LatestManifest.ID != 7 || ds.LatestManifest.ObjectCount != 12 {
				t.Errorf("modpacks latestManifest = %+v, want manifest 7 with 12 objects", ds.LatestManifest)
			}
		}
	}
}

func TestStorageMigrationHandler_ExportManifestStreamsCSV(t *testing.T) {
	fs := newStorageMigrationFakeStore()
	fs.manifests = []models.StorageManifest{{ID: 4, DataSet: "modpacks", Algo: "sha256", CapturedAt: time.Now()}}
	fs.entries[4] = []models.StorageManifestEntry{
		{ManifestID: 4, Key: "a.mrpack", Size: 3, Checksum: "aa"},
		{ManifestID: 4, Key: "b,with,commas.mrpack", Size: 5, Checksum: "bb"},
	}
	h := NewStorageMigrationHandler(&AppState{Store: fs})

	req := mux.SetURLVars(httptest.NewRequest(http.MethodGet, "/api/admin/storage/manifests/4/export", nil), map[string]string{"id": "4"})
	rw := httptest.NewRecorder()
	h.ExportManifest(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("ExportManifest status = %d (%s)", rw.Code, rw.Body.String())
	}
	if ct := rw.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Errorf("Content-Type = %q, want text/csv", ct)
	}
	if cd := rw.Header().Get("Content-Disposition"); !strings.Contains(cd, "attachment") {
		t.Errorf("Content-Disposition = %q, want an attachment", cd)
	}
	body := rw.Body.String()
	if !strings.HasPrefix(body, "key,size,sha256\n") {
		t.Errorf("CSV header = %q", strings.SplitN(body, "\n", 2)[0])
	}
	// encoding/csv must quote the key containing commas.
	if !strings.Contains(body, `"b,with,commas.mrpack",5,bb`) {
		t.Errorf("a key containing commas was not quoted:\n%s", body)
	}
}

func TestStorageMigrationHandler_ExportManifestUnknownIDIs404(t *testing.T) {
	h := NewStorageMigrationHandler(&AppState{Store: newStorageMigrationFakeStore()})
	req := mux.SetURLVars(httptest.NewRequest(http.MethodGet, "/api/admin/storage/manifests/99/export", nil), map[string]string{"id": "99"})
	rw := httptest.NewRecorder()
	h.ExportManifest(rw, req)
	if rw.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rw.Code)
	}
}

func TestStorageMigrationHandler_DeleteManifest(t *testing.T) {
	fs := newStorageMigrationFakeStore()
	fs.manifests = []models.StorageManifest{{ID: 4, DataSet: "modpacks"}}
	h := NewStorageMigrationHandler(&AppState{Store: fs})

	req := mux.SetURLVars(httptest.NewRequest(http.MethodDelete, "/api/admin/storage/manifests/4", nil), map[string]string{"id": "4"})
	rw := httptest.NewRecorder()
	h.DeleteManifest(rw, req)
	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d (%s)", rw.Code, rw.Body.String())
	}
	if len(fs.deleted) != 1 || fs.deleted[0] != 4 {
		t.Errorf("deleted = %v, want [4]", fs.deleted)
	}
}

func TestStorageMigrationHandler_ListManifests(t *testing.T) {
	fs := newStorageMigrationFakeStore()
	fs.manifests = []models.StorageManifest{
		{ID: 1, DataSet: "modpacks"},
		{ID: 2, DataSet: "library"},
	}
	h := NewStorageMigrationHandler(&AppState{Store: fs})

	rw := httptest.NewRecorder()
	h.ListManifests(rw, httptest.NewRequest(http.MethodGet, "/api/admin/storage/manifests?dataSet=library", nil))
	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d (%s)", rw.Code, rw.Body.String())
	}
	var got struct {
		Manifests []models.StorageManifest `json:"manifests"`
	}
	_ = json.Unmarshal(rw.Body.Bytes(), &got)
	if len(got.Manifests) != 1 || got.Manifests[0].ID != 2 {
		t.Fatalf("manifests = %+v, want only the library one", got.Manifests)
	}
}
