package services

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"dylaris-core/models"
	backupstorage "dylaris-core/storage/backup"
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

	blob, presigned, err := PrepareNodeStorage(context.Background(), st, storage, operatorNode(), "key", "get", noDeps())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

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

	blob, presigned, err := PrepareNodeStorage(context.Background(), st, storage, byonNode(), "key", "get", noDeps())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

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

	blob, presigned, err := PrepareNodeStorage(context.Background(), st, nil, byonNode(), "key", "get", noDeps())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

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

	blob, presigned, err := PrepareNodeStorage(context.Background(), st, storage, byonNode(), "backups/x.tar.gz", "get", noDeps())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

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

	_, getURL, _ := PrepareNodeStorage(context.Background(), st, storage, byonNode(), "k", "get", noDeps())
	_, putURL, _ := PrepareNodeStorage(context.Background(), st, storage, byonNode(), "k", "put", noDeps())

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

	_, presigned, _ := PrepareNodeStorage(context.Background(), st, storage, byonNode(), "k", "get", noDeps())

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

	blob, presigned, err := PrepareNodeStorage(context.Background(), st, storage, byonNode(), "k", "get", noDeps())
	if err != nil {
		t.Fatalf("a BYON s3 open failure must stay a silent fail-safe, got %v", err)
	}

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

// noDeps is the zero Deps: enough for s3/local, and deliberately missing the
// builders the indirection providers need, so a test that forgets to supply one
// fails the way production did.
func noDeps() backupstorage.Deps { return backupstorage.Deps{} }

func connectionStorage() *models.BackupStorage {
	return &models.BackupStorage{
		ID: 7, Name: "R2 main", Provider: "connection",
		Config: json.RawMessage(`{"connectionId":3,"prefix":"server-backups"}`),
	}
}

// The node implements exactly four providers (backup_worker.go / backup_restore.go).
// Anything Core adds beyond them is an indirection Core must resolve BEFORE
// dispatch, and this list is what decides that. Adding a provider here without
// teaching the node about it sends a row the node answers with
// "unknown provider <x>" — which is exactly how "connection" shipped broken.
func TestNodeResolvesProviderMatchesTheNodeSwitch(t *testing.T) {
	for _, p := range []string{"local", "shared", "node-local", "s3"} {
		if !nodeResolvesProvider(p) {
			t.Errorf("nodeResolvesProvider(%q) = false, want true — the node implements it", p)
		}
	}
	for _, p := range []string{"connection", "core-storage", "", "ftp"} {
		if nodeResolvesProvider(p) {
			t.Errorf("nodeResolvesProvider(%q) = true, want false — the node has no case for it", p)
		}
	}
}

// The reported failure: an operator node got the "connection" row verbatim and
// answered "upload failed: unknown provider connection". Core resolves it and
// presigns instead, for EVERY node, because the node cannot dereference the row
// and cannot know the connection's key prefix either.
func TestPrepareNodeStorage_ConnectionProvider_PresignsForOperatorNode(t *testing.T) {
	target, err := backupstorage.NewS3(context.Background(),
		json.RawMessage(`{"bucket":"b","region":"us-east-1","accessKeyId":"AKIA","secretAccessKey":"secret"}`))
	if err != nil {
		t.Fatalf("build target: %v", err)
	}
	var gotID int
	var gotPrefix string
	deps := backupstorage.Deps{
		Connection: func(id int, prefix string) (backupstorage.Storage, error) {
			gotID, gotPrefix = id, prefix
			return target, nil
		},
	}

	blob, presigned, err := PrepareNodeStorage(context.Background(), &storageFakeStore{},
		connectionStorage(), operatorNode(), "backups/x.tar.gz", "put", deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if presigned == "" {
		t.Fatal("no presigned URL: the node would fall back to the storage blob and fail on the provider name")
	}
	if gotID != 3 || gotPrefix != "server-backups" {
		t.Errorf("resolved connection %d prefix %q, want 3 / server-backups", gotID, gotPrefix)
	}
	if q := parsePresignedQuery(t, presigned); q.Get("x-id") != "PutObject" {
		t.Errorf("x-id = %q, want PutObject", q.Get("x-id"))
	}
	var stripped models.BackupStorage
	if err := json.Unmarshal(blob, &stripped); err != nil {
		t.Fatalf("unmarshal blob: %v", err)
	}
	if string(stripped.Config) != "{}" {
		t.Errorf("config = %s, want {} — the node has no use for it once a URL is signed", stripped.Config)
	}
}

// An unresolvable indirection has no node-side fallback, so it must surface as
// an error the caller can fail the run with. Returning a blob and empty URL (the
// BYON fail-safe) would dispatch a command that can only fail on the node, with
// the reason two hops from the cause.
func TestPrepareNodeStorage_IndirectProviderWithoutBuilder_Errors(t *testing.T) {
	for _, tc := range []struct{ name, provider string }{
		{"connection", "connection"},
		{"core storage", "core-storage"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := connectionStorage()
			s.Provider = tc.provider
			_, presigned, err := PrepareNodeStorage(context.Background(), &storageFakeStore{},
				s, operatorNode(), "k", "put", noDeps())
			if err == nil {
				t.Fatal("no error: a run would be dispatched that the node cannot execute")
			}
			if presigned != "" {
				t.Errorf("presignedURL = %q, want empty", presigned)
			}
			if !strings.Contains(err.Error(), "R2 main") || !strings.Contains(err.Error(), tc.provider) {
				t.Errorf("error %q names neither the storage row nor the provider", err)
			}
		})
	}
}
