package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"dylaris-core/models"
	"dylaris-core/store"

	"github.com/gorilla/mux"
)

// ownStorageFakeStore holds storages by id and records what was written.
type ownStorageFakeStore struct {
	store.Store
	byID    map[int]*models.BackupStorage
	created []models.BackupStorage
	updated []models.BackupStorage
	deleted []int
}

func (f *ownStorageFakeStore) GetBackupStorage(id int) (*models.BackupStorage, error) {
	bs, ok := f.byID[id]
	if !ok {
		return nil, nil
	}
	return bs, nil
}

func (f *ownStorageFakeStore) ListBackupStoragesByOwner(ownerID string) ([]models.BackupStorage, error) {
	var out []models.BackupStorage
	for _, bs := range f.byID {
		if bs.OwnerID != nil && *bs.OwnerID == ownerID {
			out = append(out, *bs)
		}
	}
	return out, nil
}

func (f *ownStorageFakeStore) CreateBackupStorage(bs *models.BackupStorage) (int, error) {
	f.created = append(f.created, *bs)
	return 100 + len(f.created), nil
}

func (f *ownStorageFakeStore) UpdateBackupStorage(bs *models.BackupStorage) error {
	f.updated = append(f.updated, *bs)
	return nil
}

func (f *ownStorageFakeStore) DeleteBackupStorage(id int) error {
	f.deleted = append(f.deleted, id)
	return nil
}

func asUser(r *http.Request, userID string) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), "userID", userID)) //nolint:staticcheck // matches the middleware's key
}

func s3Body(name string, extra map[string]any) []byte {
	body := map[string]any{
		"name":     name,
		"provider": "s3",
		"config":   json.RawMessage(`{"endpoint":"https://s3.example.com","bucket":"b","accessKeyId":"AK","secretAccessKey":"sk"}`),
	}
	for k, v := range extra {
		body[k] = v
	}
	b, _ := json.Marshal(body)
	return b
}

// The owner is taken from the SESSION, never from the payload.
//
// A payload-supplied owner is the whole attack surface of this route: it would
// let anyone holding the capability write a row into another account, or - by
// sending null - into the PLATFORM scope, where it becomes a storage admins can
// select as a target for everybody's backups.
func TestCreateOwnStorageIgnoresAnOwnerInTheBody(t *testing.T) {
	st := &ownStorageFakeStore{byID: map[int]*models.BackupStorage{}}
	h := &BackupHandler{state: &AppState{Store: st}}

	rec := httptest.NewRecorder()
	req := asUser(httptest.NewRequest(http.MethodPost, "/api/me/backup-storages",
		bytes.NewReader(s3Body("mine", map[string]any{"ownerId": "someone-else"}))), "u1")
	h.CreateOwnStorage(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("create returned %d: %s", rec.Code, rec.Body.String())
	}
	if len(st.created) != 1 {
		t.Fatalf("created %d rows", len(st.created))
	}
	got := st.created[0]
	if got.OwnerID == nil || *got.OwnerID != "u1" {
		t.Errorf("owner = %v, want the caller (u1)", got.OwnerID)
	}
}

// Only s3. The platform's other providers address storage that is not the
// tenant's: "local" and "shared" write to a path on Core's own filesystem,
// "node-local" onto a machine they may not own, and "connection" dereferences a
// config an admin made. Each is a way out of the account through a config blob.
func TestCreateOwnStorageRefusesPlatformProviders(t *testing.T) {
	for _, provider := range []string{"local", "shared", "node-local", "core-storage", "connection", ""} {
		t.Run(provider, func(t *testing.T) {
			st := &ownStorageFakeStore{byID: map[int]*models.BackupStorage{}}
			h := &BackupHandler{state: &AppState{Store: st}}
			body, _ := json.Marshal(map[string]any{
				"name": "x", "provider": provider,
				"config": json.RawMessage(`{"basePath":"/var/lib/dylaris"}`),
			})
			rec := httptest.NewRecorder()
			h.CreateOwnStorage(rec, asUser(httptest.NewRequest(http.MethodPost, "/x", bytes.NewReader(body)), "u1"))
			if rec.Code != http.StatusBadRequest {
				t.Errorf("provider %q returned %d, want 400", provider, rec.Code)
			}
			if len(st.created) != 0 {
				t.Errorf("provider %q was written anyway: %+v", provider, st.created)
			}
		})
	}
}

// Another tenant's storage answers 404, and so does a platform one.
//
// 404 rather than 403 on purpose: a distinct "forbidden" confirms that the id
// exists, which is exactly what walking /api/me/backup-storages/{id} is trying
// to learn.
func TestOwnStorageRoutesRefuseSomebodyElsesRow(t *testing.T) {
	other := "u2"
	st := &ownStorageFakeStore{byID: map[int]*models.BackupStorage{
		7: {ID: 7, Name: "theirs", Provider: "s3", OwnerID: &other},
		8: {ID: 8, Name: "the platform's", Provider: "s3"}, // OwnerID nil
	}}
	h := &BackupHandler{state: &AppState{Store: st}}

	for _, id := range []string{"7", "8", "999"} {
		t.Run("delete "+id, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := mux.SetURLVars(asUser(httptest.NewRequest(http.MethodDelete, "/x", nil), "u1"), map[string]string{"id": id})
			h.DeleteOwnStorage(rec, req)
			if rec.Code != http.StatusNotFound {
				t.Errorf("delete of %s returned %d, want 404", id, rec.Code)
			}
		})
		t.Run("update "+id, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := mux.SetURLVars(
				asUser(httptest.NewRequest(http.MethodPatch, "/x", bytes.NewReader(s3Body("renamed", nil))), "u1"),
				map[string]string{"id": id})
			h.UpdateOwnStorage(rec, req)
			if rec.Code != http.StatusNotFound {
				t.Errorf("update of %s returned %d, want 404", id, rec.Code)
			}
		})
	}
	if len(st.deleted) != 0 || len(st.updated) != 0 {
		t.Errorf("a foreign row was written: deleted=%v updated=%+v", st.deleted, st.updated)
	}
}

// An id in the PAYLOAD must not redirect the write. The ownership check passed
// on the row in the URL, so that is the row that gets edited.
func TestUpdateOwnStorageIgnoresIdentityInTheBody(t *testing.T) {
	me := "u1"
	other := "u2"
	st := &ownStorageFakeStore{byID: map[int]*models.BackupStorage{
		4: {ID: 4, Name: "mine", Provider: "s3", OwnerID: &me,
			Config: json.RawMessage(`{"endpoint":"https://s3.example.com","bucket":"b","accessKeyId":"AK","secretAccessKey":"sk"}`)},
		7: {ID: 7, Name: "theirs", Provider: "s3", OwnerID: &other},
	}}
	h := &BackupHandler{state: &AppState{Store: st}}

	rec := httptest.NewRecorder()
	req := mux.SetURLVars(
		asUser(httptest.NewRequest(http.MethodPatch, "/x",
			bytes.NewReader(s3Body("mine", map[string]any{"id": 7, "ownerId": other}))), "u1"),
		map[string]string{"id": "4"})
	h.UpdateOwnStorage(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("update returned %d: %s", rec.Code, rec.Body.String())
	}
	if len(st.updated) != 1 {
		t.Fatalf("wrote %d rows", len(st.updated))
	}
	got := st.updated[0]
	if got.ID != 4 {
		t.Errorf("wrote row %d, want the one in the URL (4)", got.ID)
	}
	if got.OwnerID == nil || *got.OwnerID != "u1" {
		t.Errorf("owner = %v, want it unchanged (u1)", got.OwnerID)
	}
}

// The list is the caller's own and nobody else's, platform rows included.
func TestListOwnStoragesIsScopedToTheCaller(t *testing.T) {
	me, other := "u1", "u2"
	st := &ownStorageFakeStore{byID: map[int]*models.BackupStorage{
		1: {ID: 1, Name: "platform", Provider: "s3"},
		4: {ID: 4, Name: "mine", Provider: "s3", OwnerID: &me},
		7: {ID: 7, Name: "theirs", Provider: "s3", OwnerID: &other},
	}}
	h := &BackupHandler{state: &AppState{Store: st}}

	rec := httptest.NewRecorder()
	h.ListOwnStorages(rec, asUser(httptest.NewRequest(http.MethodGet, "/x", nil), "u1"))

	var body struct {
		Storages []models.BackupStorage `json:"storages"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Storages) != 1 || body.Storages[0].ID != 4 {
		t.Errorf("listed %+v, want only the caller's own row", body.Storages)
	}
}
