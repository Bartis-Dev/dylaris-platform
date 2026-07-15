package services

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"testing"
	"time"

	"dylaris-core/models"
	"dylaris-core/store"
)

// storageFakeStore embeds store.Store (nil) so it satisfies the full
// interface at compile time; only GetSetting is overridden.
type storageFakeStore struct {
	store.Store
	settings map[string]string
}

func (f *storageFakeStore) GetSetting(key string) (string, error) {
	v, ok := f.settings[key]
	if !ok {
		return "", errors.New("not found")
	}
	return v, nil
}

func TestPresignTTL(t *testing.T) {
	cases := []struct {
		name     string
		isBYON   bool
		settings map[string]string
		want     time.Duration
	}{
		{"node default, no setting", false, nil, 60 * time.Minute},
		{"byon default, no setting", true, nil, 360 * time.Minute},
		{"node custom valid", false, map[string]string{"r2.presign_ttl_node_minutes": "15"}, 15 * time.Minute},
		{"byon custom valid", true, map[string]string{"r2.presign_ttl_byon_minutes": "120"}, 120 * time.Minute},
		{"node non-numeric falls back", false, map[string]string{"r2.presign_ttl_node_minutes": "abc"}, 60 * time.Minute},
		{"node zero falls back", false, map[string]string{"r2.presign_ttl_node_minutes": "0"}, 60 * time.Minute},
		{"node negative falls back", false, map[string]string{"r2.presign_ttl_node_minutes": "-5"}, 60 * time.Minute},
		{"byon setting does not affect node key", false, map[string]string{"r2.presign_ttl_byon_minutes": "999"}, 60 * time.Minute},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := &storageFakeStore{settings: c.settings}
			got := presignTTL(st, c.isBYON)
			if got != c.want {
				t.Errorf("presignTTL(isBYON=%v) = %v, want %v", c.isBYON, got, c.want)
			}
		})
	}
}

func s3Storage(config string) *models.BackupStorage {
	return &models.BackupStorage{ID: 1, Provider: "s3", Config: json.RawMessage(config)}
}

func byonNode() *models.Node {
	owner := "owner-1"
	return &models.Node{ID: 1, OwnerID: &owner}
}

func operatorNode() *models.Node {
	return &models.Node{ID: 1, OwnerID: nil}
}

func TestPrepareNodeStorage_OperatorNode_ReturnsFullBlobNoURL(t *testing.T) {
	storage := s3Storage(`{"bucket":"b","region":"us-east-1","accessKeyId":"AKIA","secretAccessKey":"secret"}`)
	st := &storageFakeStore{}

	blob, presigned := PrepareNodeStorage(context.Background(), st, storage, operatorNode(), "key", "get")

	if presigned != "" {
		t.Errorf("presignedURL = %q, want empty for an operator node", presigned)
	}
	want, _ := json.Marshal(storage)
	if string(blob) != string(want) {
		t.Errorf("blob = %s, want the full unstripped storage blob %s", blob, want)
	}
}

func TestPrepareNodeStorage_BYONNonS3Provider_ReturnsFullBlobNoURL(t *testing.T) {
	storage := &models.BackupStorage{ID: 2, Provider: "local", Config: json.RawMessage(`{"path":"/data/backups"}`)}
	st := &storageFakeStore{}

	blob, presigned := PrepareNodeStorage(context.Background(), st, storage, byonNode(), "key", "get")

	if presigned != "" {
		t.Errorf("presignedURL = %q, want empty for a non-s3 provider", presigned)
	}
	want, _ := json.Marshal(storage)
	if string(blob) != string(want) {
		t.Errorf("blob = %s, want the full unstripped storage blob %s", blob, want)
	}
}

func TestPrepareNodeStorage_NilStorage_BYON_ReturnsNullNoURL(t *testing.T) {
	st := &storageFakeStore{}

	blob, presigned := PrepareNodeStorage(context.Background(), st, nil, byonNode(), "key", "get")

	if presigned != "" {
		t.Errorf("presignedURL = %q, want empty for nil storage", presigned)
	}
	if string(blob) != "null" {
		t.Errorf("blob = %s, want null", blob)
	}
}

func TestPrepareNodeStorage_BYONS3_StripsCredentials(t *testing.T) {
	storage := s3Storage(`{"bucket":"b","region":"us-east-1","accessKeyId":"AKIA_SECRET_KEY","secretAccessKey":"very-secret"}`)
	st := &storageFakeStore{}

	blob, presigned := PrepareNodeStorage(context.Background(), st, storage, byonNode(), "backups/x.tar.gz", "get")

	var stripped models.BackupStorage
	if err := json.Unmarshal(blob, &stripped); err != nil {
		t.Fatalf("unmarshal stripped blob: %v", err)
	}
	if string(stripped.Config) != "{}" {
		t.Errorf("stripped config = %s, want {} (credential-stripped)", stripped.Config)
	}
	if stripped.Provider != "s3" || stripped.ID != storage.ID {
		t.Errorf("stripped blob lost non-credential fields: %+v", stripped)
	}
	if presigned == "" {
		t.Fatal("expected a non-empty presigned URL for a valid BYON s3 request")
	}
}

func TestPrepareNodeStorage_BYONS3_OpSelectsGetVsPutSignature(t *testing.T) {
	storage := s3Storage(`{"bucket":"b","region":"us-east-1","accessKeyId":"AKIA","secretAccessKey":"secret"}`)
	st := &storageFakeStore{}

	_, getURL := PrepareNodeStorage(context.Background(), st, storage, byonNode(), "k", "get")
	_, putURL := PrepareNodeStorage(context.Background(), st, storage, byonNode(), "k", "put")

	getQ := parsePresignedQuery(t, getURL)
	putQ := parsePresignedQuery(t, putURL)

	if getQ.Get("x-id") != "GetObject" {
		t.Errorf("op=get produced x-id=%q, want GetObject", getQ.Get("x-id"))
	}
	if putQ.Get("x-id") != "PutObject" {
		t.Errorf("op=put produced x-id=%q, want PutObject", putQ.Get("x-id"))
	}
}

func TestPrepareNodeStorage_TTLThreadedFromSettings(t *testing.T) {
	storage := s3Storage(`{"bucket":"b","region":"us-east-1","accessKeyId":"AKIA","secretAccessKey":"secret"}`)
	st := &storageFakeStore{settings: map[string]string{"r2.presign_ttl_byon_minutes": "15"}}

	_, presigned := PrepareNodeStorage(context.Background(), st, storage, byonNode(), "k", "get")

	q := parsePresignedQuery(t, presigned)
	if got := q.Get("X-Amz-Expires"); got != "900" {
		t.Errorf("X-Amz-Expires = %s, want 900 (15 minutes)", got)
	}
}

func TestPrepareNodeStorage_S3OpenFails_StripsCredsButNoURL(t *testing.T) {
	// Missing "bucket" -> backupstorage.NewS3 returns an error; PrepareNodeStorage's
	// fail-safe must still strip creds but leave the URL empty rather than error out.
	storage := s3Storage(`{"accessKeyId":"AKIA","secretAccessKey":"secret"}`)
	st := &storageFakeStore{}

	blob, presigned := PrepareNodeStorage(context.Background(), st, storage, byonNode(), "k", "get")

	if presigned != "" {
		t.Errorf("presignedURL = %q, want empty when the storage provider fails to open", presigned)
	}
	var stripped models.BackupStorage
	if err := json.Unmarshal(blob, &stripped); err != nil {
		t.Fatalf("unmarshal stripped blob: %v", err)
	}
	if string(stripped.Config) != "{}" {
		t.Errorf("stripped config = %s, want {} even when presign fails (fail-safe: never leak creds)", stripped.Config)
	}
}

func parsePresignedQuery(t *testing.T, rawURL string) url.Values {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse presigned url %q: %v", rawURL, err)
	}
	return u.Query()
}
