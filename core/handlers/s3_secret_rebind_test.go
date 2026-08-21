package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"dylaris-core/models"

	"github.com/gorilla/mux"
)

// The platform has four places that store an s3 credential: core storage,
// backup storages, modpack storage and the shared storage connections. All
// four hide the secret on every read and treat a blank secret on write as
// "keep the stored one" - which is what makes the identity fields
// (endpoint, bucket, access key) the thing that decides WHERE that hidden
// secret gets used.
//
// Two of the four checked that those fields had not moved
// (mergeCoreStorageCandidate, and mergeBackupStorageSecret since the backup
// pass). Modpack storage and storage connections did not: a settings.write
// holder who cannot READ the secret could submit a new endpoint with the
// secret field left blank, and the operator's credential would sign requests
// to a host of the caller's choosing. These pin the two that were missing.

func TestModpackS3SecretRebound(t *testing.T) {
	stored := map[string]string{
		"modpack_storage_s3_endpoint":   "https://s3.eu-central-1.amazonaws.com",
		"modpack_storage_s3_bucket":     "dylaris-packs",
		"modpack_storage_s3_access_key": "AKIAREAL",
		"modpack_storage_s3_secret_key": "the-real-secret",
	}
	base := modpackSettings{
		S3Endpoint:  stored["modpack_storage_s3_endpoint"],
		S3Bucket:    stored["modpack_storage_s3_bucket"],
		S3AccessKey: stored["modpack_storage_s3_access_key"],
	}
	get := func(k string) string { return stored[k] }

	cases := []struct {
		name   string
		mutate func(*modpackSettings)
		noSec  bool // drop the stored secret for this case
		want   bool
	}{
		{"unchanged identity, blank secret", func(*modpackSettings) {}, false, false},
		{"endpoint moved", func(s *modpackSettings) { s.S3Endpoint = "https://attacker.example" }, false, true},
		{"bucket moved", func(s *modpackSettings) { s.S3Bucket = "someone-elses" }, false, true},
		{"access key moved", func(s *modpackSettings) { s.S3AccessKey = "AKIAOTHER" }, false, true},
		{"a submitted secret is a rotation, always allowed", func(s *modpackSettings) {
			s.S3Endpoint = "https://attacker.example"
			s.S3SecretKey = "brand-new"
		}, false, false},
		{"whitespace-only secret is still blank", func(s *modpackSettings) {
			s.S3Endpoint = "https://attacker.example"
			s.S3SecretKey = "   "
		}, false, true},
		{"nothing stored means nothing to rebind", func(s *modpackSettings) {
			s.S3Endpoint = "https://attacker.example"
		}, true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := base
			c.mutate(&req)
			g := get
			if c.noSec {
				g = func(k string) string {
					if k == "modpack_storage_s3_secret_key" {
						return ""
					}
					return stored[k]
				}
			}
			if got := modpackS3SecretRebound(req, g); got != c.want {
				t.Fatalf("modpackS3SecretRebound = %v, want %v", got, c.want)
			}
		})
	}
}

func connConfig(endpoint, bucket string) json.RawMessage {
	b, _ := json.Marshal(storageConnectionConfig{Endpoint: endpoint, Bucket: bucket})
	return b
}

func TestStorageConnectionSecretRebound(t *testing.T) {
	existing := &models.StorageConnection{
		ID:        5,
		Config:    connConfig("https://s3.example", "packs"),
		AccessKey: "AKIAREAL",
		SecretSet: true,
	}
	cases := []struct {
		name     string
		req      storageConnectionRequest
		existing *models.StorageConnection
		want     bool
	}{
		{"unchanged identity, blank secret",
			storageConnectionRequest{Config: connConfig("https://s3.example", "packs"), AccessKey: "AKIAREAL"}, existing, false},
		{"endpoint moved",
			storageConnectionRequest{Config: connConfig("https://attacker.example", "packs"), AccessKey: "AKIAREAL"}, existing, true},
		{"bucket moved",
			storageConnectionRequest{Config: connConfig("https://s3.example", "theirs"), AccessKey: "AKIAREAL"}, existing, true},
		{"access key moved",
			storageConnectionRequest{Config: connConfig("https://s3.example", "packs"), AccessKey: "AKIAOTHER"}, existing, true},
		{"an omitted config would blank the endpoint",
			storageConnectionRequest{AccessKey: "AKIAREAL"}, existing, true},
		{"a submitted secret is a rotation, always allowed",
			storageConnectionRequest{Config: connConfig("https://attacker.example", "x"), AccessKey: "z", SecretAccessKey: "new"}, existing, false},
		{"no stored secret means nothing to rebind",
			storageConnectionRequest{Config: connConfig("https://attacker.example", "x")},
			&models.StorageConnection{ID: 5, SecretSet: false}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := storageConnectionSecretRebound(c.req, c.existing); got != c.want {
				t.Fatalf("storageConnectionSecretRebound = %v, want %v", got, c.want)
			}
		})
	}
}

// End-to-end on the wire for the modpack surface: the refusal has to land
// before the settings writes, or the endpoint is already rebound by the time
// the caller sees the 400.
func TestModpackSettingsSet_RefusesARebindOnTheWire(t *testing.T) {
	fs := newCoreStorageHTTPFakeStore()
	fs.kv["modpack_storage_s3_endpoint"] = "https://s3.example"
	fs.kv["modpack_storage_s3_bucket"] = "packs"
	fs.kv["modpack_storage_s3_access_key"] = "AKIAREAL"
	fs.kv["modpack_storage_s3_secret_key"] = "the-real-secret"
	h := NewModpackSettingsHandler(&AppState{Store: fs})

	body, _ := json.Marshal(modpackSettings{
		Provider:    "s3",
		S3Endpoint:  "https://attacker.example",
		S3Bucket:    "packs",
		S3AccessKey: "AKIAREAL",
		// no S3SecretKey: the caller cannot read it, and is not supplying one
	})
	rw := httptest.NewRecorder()
	h.Set(rw, httptest.NewRequest(http.MethodPut, "/api/admin/settings/modpacks", bytes.NewReader(body)))

	if rw.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (%s)", rw.Code, rw.Body.String())
	}
	if got := fs.kv["modpack_storage_s3_endpoint"]; got != "https://s3.example" {
		t.Fatalf("endpoint was rebound anyway: %q", got)
	}
	if got := fs.kv["modpack_storage_s3_secret_key"]; got != "the-real-secret" {
		t.Fatalf("stored secret changed: %q", got)
	}
}

// End-to-end on the wire: the refusal has to happen before the metadata write,
// or the endpoint is already rebound by the time the caller sees the 400.
func TestUpdateConnection_RefusesARebindOnTheWire(t *testing.T) {
	fs := &connFakeStore{existing: &models.StorageConnection{
		ID:        5,
		Config:    connConfig("https://s3.example", "packs"),
		AccessKey: "AKIAREAL",
		SecretSet: true,
	}}
	h := NewStorageConnectionsHandler(&AppState{Store: fs})

	body, _ := json.Marshal(storageConnectionRequest{
		Name: "nas", Provider: "s3",
		Config:    connConfig("https://attacker.example", "packs"),
		AccessKey: "AKIAREAL",
	})
	req := httptest.NewRequest(http.MethodPatch, "/api/storage-connections/5", bytes.NewReader(body))
	req = mux.SetURLVars(req, map[string]string{"id": "5"})
	rw := httptest.NewRecorder()
	h.UpdateConnection(rw, req)

	if rw.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (%s)", rw.Code, rw.Body.String())
	}
	if fs.updated != 0 {
		t.Fatalf("the endpoint was persisted anyway (%d updates)", fs.updated)
	}
}
