package handlers

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"dylaris-core/models"
	"dylaris-core/store"

	"github.com/gorilla/mux"
)

type regionDeleteFakeStore struct {
	store.Store

	servers, nodes       int
	serversErr, nodesErr error
	deleted              bool
}

func (f *regionDeleteFakeStore) CountServersInRegion(string) (int, error) {
	return f.servers, f.serversErr
}
func (f *regionDeleteFakeStore) CountNodesInRegion(string) (int, error) { return f.nodes, f.nodesErr }
func (f *regionDeleteFakeStore) DeleteRegion(string) error              { f.deleted = true; return nil }

// The happy path continues into identity audit logging.
func (f *regionDeleteFakeStore) InsertAuditIdentity(*models.AuditEventIdentity) error { return nil }

func regionDeleteReq(t *testing.T) *http.Request {
	t.Helper()
	r := httptest.NewRequest(http.MethodDelete, "/api/regions/eu", nil)
	return mux.SetURLVars(r, map[string]string{"id": "eu"})
}

// The two counts are the only thing standing between an admin and a region
// deleted out from under its servers. `servers.region` is a plain text column
// with no foreign key, so the database does not stop it either: the rows keep an
// id that no longer resolves, and region-based placement, beam routing and the
// per-region DNS records are all keyed on exactly that.
//
// Discarding the error made a failed count return 0, so both guards passed.
func TestDeleteRegion_RefusesWhenTheServerCountFails(t *testing.T) {
	fake := &regionDeleteFakeStore{serversErr: errors.New("connection reset")}
	h := &RegionsHandler{state: &AppState{Store: fake}}

	rec := httptest.NewRecorder()
	h.DeleteRegion(rec, regionDeleteReq(t))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	if fake.deleted {
		t.Error("region deleted without ever verifying it was empty")
	}
}

// The node count is a second, separate guard and fails the same way.
func TestDeleteRegion_RefusesWhenTheNodeCountFails(t *testing.T) {
	fake := &regionDeleteFakeStore{nodesErr: errors.New("connection reset")}
	h := &RegionsHandler{state: &AppState{Store: fake}}

	rec := httptest.NewRecorder()
	h.DeleteRegion(rec, regionDeleteReq(t))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	if fake.deleted {
		t.Error("region deleted without verifying it had no nodes")
	}
}

// Both guards must still do their actual job.
func TestDeleteRegion_RefusesAPopulatedRegion(t *testing.T) {
	for _, tc := range []struct {
		name string
		f    *regionDeleteFakeStore
	}{
		{"has servers", &regionDeleteFakeStore{servers: 3}},
		{"has nodes", &regionDeleteFakeStore{nodes: 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &RegionsHandler{state: &AppState{Store: tc.f}}
			rec := httptest.NewRecorder()
			h.DeleteRegion(rec, regionDeleteReq(t))
			if rec.Code != http.StatusConflict {
				t.Errorf("status = %d, want 409", rec.Code)
			}
			if tc.f.deleted {
				t.Error("a populated region was deleted")
			}
		})
	}
}

// And an empty one must still be deletable, or the fix is just a wall.
func TestDeleteRegion_DeletesAnEmptyRegion(t *testing.T) {
	fake := &regionDeleteFakeStore{}
	h := &RegionsHandler{state: &AppState{Store: fake}}

	rec := httptest.NewRecorder()
	h.DeleteRegion(rec, regionDeleteReq(t))

	if !fake.deleted {
		t.Errorf("an empty region was not deleted (status %d)", rec.Code)
	}
}
