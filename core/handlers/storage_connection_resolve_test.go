package handlers

import (
	"encoding/json"
	"errors"
	"testing"

	"dylaris-core/models"
	"dylaris-core/store"
)

// resolveFakeStore serves settings + one storage connection. Embeds store.Store
// so only the two methods the resolver touches need bodies.
type resolveFakeStore struct {
	store.Store
	settings map[string]string
	conn     *models.StorageConnection
	connErr  error
	askedID  int
}

func (f *resolveFakeStore) GetSetting(k string) (string, error) { return f.settings[k], nil }
func (f *resolveFakeStore) GetStorageConnection(id int) (*models.StorageConnection, error) {
	f.askedID = id
	if f.connErr != nil {
		return nil, f.connErr
	}
	return f.conn, nil
}

func TestSelectedStorageConnection_NoneSelected(t *testing.T) {
	for _, raw := range []string{"", "0", "  "} {
		fs := &resolveFakeStore{settings: map[string]string{keyCoreStorageConnectionID: raw}}
		s := &AppState{Store: fs}
		conn, ok, err := s.selectedStorageConnection(keyCoreStorageConnectionID)
		if err != nil || ok || conn != nil {
			t.Errorf("selected(%q) = (%v, %v, %v), want (nil, false, nil)", raw, conn, ok, err)
		}
	}
}

func TestSelectedStorageConnection_Loads(t *testing.T) {
	fs := &resolveFakeStore{
		settings: map[string]string{keyCoreStorageConnectionID: "3"},
		conn:     &models.StorageConnection{ID: 3, Name: "nas", Provider: "s3"},
	}
	s := &AppState{Store: fs}
	conn, ok, err := s.selectedStorageConnection(keyCoreStorageConnectionID)
	if err != nil || !ok || conn == nil || conn.ID != 3 {
		t.Fatalf("selected = (%v, %v, %v), want the id-3 connection", conn, ok, err)
	}
	if fs.askedID != 3 {
		t.Errorf("store asked for id %d, want 3", fs.askedID)
	}
}

func TestSelectedStorageConnection_BadIDErrors(t *testing.T) {
	fs := &resolveFakeStore{settings: map[string]string{keyCoreStorageConnectionID: "not-a-number"}}
	s := &AppState{Store: fs}
	if _, ok, err := s.selectedStorageConnection(keyCoreStorageConnectionID); err == nil || ok {
		t.Fatalf("selected(bad id) = (ok=%v, err=%v), want an error and ok=false", ok, err)
	}
}

func TestSelectedStorageConnection_LoadErrorSurfaces(t *testing.T) {
	fs := &resolveFakeStore{
		settings: map[string]string{keyCoreStorageConnectionID: "9"},
		connErr:  errors.New("no such row"),
	}
	s := &AppState{Store: fs}
	if _, ok, err := s.selectedStorageConnection(keyCoreStorageConnectionID); err == nil || ok {
		t.Fatalf("selected(missing conn) = (ok=%v, err=%v), want an error (not a silent inline fallback)", ok, err)
	}
}

// TestEffectiveCoreStorageConfig_ConnectionOverridesInline is the core guard: a
// selected connection's credentials must win over the inline core_storage_s3_*
// settings entirely.
func TestEffectiveCoreStorageConfig_ConnectionOverridesInline(t *testing.T) {
	connCfg, _ := json.Marshal(storageConnectionConfig{
		Endpoint: "https://conn.example", Region: "eu-central", Bucket: "conn-bucket",
		ForcePathStyle: true, Prefix: "px",
	})
	fs := &resolveFakeStore{
		settings: map[string]string{
			keyCoreStorageConnectionID: "3",
			// Inline config is DIFFERENT, to prove it is not used.
			keyCoreStorageBackend:     "s3",
			keyCoreStorageS3Bucket:    "inline-bucket",
			keyCoreStorageS3AccessKey: "INLINE_AK",
			keyCoreStorageS3SecretKey: "inline-secret",
		},
		conn: &models.StorageConnection{
			ID: 3, Name: "nas", Provider: "s3",
			Config: connCfg, AccessKey: "CONN_AK", SecretAccessKey: "conn-secret",
		},
	}
	s := &AppState{Store: fs}
	cfg, err := s.effectiveCoreStorageConfig()
	if err != nil {
		t.Fatalf("effectiveCoreStorageConfig: %v", err)
	}
	if cfg.Backend != "s3" || cfg.S3Bucket != "conn-bucket" || cfg.S3AccessKey != "CONN_AK" || cfg.S3SecretKey != "conn-secret" {
		t.Fatalf("effective config did not come from the connection: %+v", cfg)
	}
	if cfg.S3Endpoint != "https://conn.example" || cfg.S3Region != "eu-central" || !cfg.S3PathStyle || cfg.S3Prefix != "px" {
		t.Fatalf("connection config fields not mapped: %+v", cfg)
	}
	if !cfg.S3SecretSet {
		t.Error("S3SecretSet = false, want true (the connection has a secret)")
	}
}

// TestBuildModpackStorageProvider_UsesSelectedConnection: with a modpack
// connection selected, a provider is built from it (not from the inline modpack
// settings).
func TestBuildModpackStorageProvider_UsesSelectedConnection(t *testing.T) {
	connCfg, _ := json.Marshal(storageConnectionConfig{Bucket: "mp-bucket", Region: "eu"})
	fs := &resolveFakeStore{
		settings: map[string]string{keyModpackStorageConnectionID: "5"},
		conn: &models.StorageConnection{
			ID: 5, Name: "nas", Provider: "s3",
			Config: connCfg, AccessKey: "AK", SecretAccessKey: "SK",
		},
	}
	s := &AppState{Store: fs}
	prov, err := s.buildModpackStorageProvider()
	if err != nil {
		t.Fatalf("buildModpackStorageProvider: %v", err)
	}
	if prov == nil {
		t.Fatal("provider is nil; a selected connection must yield a provider")
	}
}

// TestBuildModpackStorageProvider_DelegatesWhenNoConnection: with nothing
// selected it falls through to the inline settings, which here are unconfigured,
// so the factory's (nil, nil) "not configured" result is returned unchanged.
func TestBuildModpackStorageProvider_DelegatesWhenNoConnection(t *testing.T) {
	fs := &resolveFakeStore{settings: map[string]string{}} // no connection, no modpack settings
	s := &AppState{Store: fs}
	prov, err := s.buildModpackStorageProvider()
	if err != nil {
		t.Fatalf("buildModpackStorageProvider: %v", err)
	}
	if prov != nil {
		t.Fatalf("want nil (unconfigured inline), got a provider - delegation path is wrong")
	}
}

func TestBuildModpackStorageProvider_BadIDErrors(t *testing.T) {
	fs := &resolveFakeStore{settings: map[string]string{keyModpackStorageConnectionID: "nope"}}
	s := &AppState{Store: fs}
	if _, err := s.buildModpackStorageProvider(); err == nil {
		t.Fatal("buildModpackStorageProvider(bad id) err = nil, want an error")
	}
}

func TestEffectiveCoreStorageConfig_FallsBackToInline(t *testing.T) {
	fs := &resolveFakeStore{
		settings: map[string]string{
			keyCoreStorageConnectionID: "", // nothing selected
			keyCoreStorageBackend:      "s3",
			keyCoreStorageS3Bucket:     "inline-bucket",
			keyCoreStorageS3SecretKey:  "inline-secret",
		},
	}
	s := &AppState{Store: fs}
	cfg, err := s.effectiveCoreStorageConfig()
	if err != nil {
		t.Fatalf("effectiveCoreStorageConfig: %v", err)
	}
	if cfg.S3Bucket != "inline-bucket" || cfg.S3SecretKey != "inline-secret" {
		t.Fatalf("no-connection case did not fall back to the inline config: %+v", cfg)
	}
}
