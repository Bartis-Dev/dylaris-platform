package authz

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"

	"dylaris-core/models"
)

// mwFakeStore reuses the resolver fake surface for the middleware tests.
type mwFakeStore struct{ resolverFakeStore }

func (f *mwFakeStore) GetServerByUUID(uuid string) (*models.Server, error) {
	if s, ok := f.serversByUUID[uuid]; ok {
		return s, nil
	}
	return nil, errors.New("no server")
}

const innerStatus = http.StatusTeapot

func innerOK(w http.ResponseWriter, r *http.Request) { w.WriteHeader(innerStatus) }

func reqWithIdentity(id Identity, vars map[string]string) *http.Request {
	r := httptest.NewRequest("GET", "/api/x", nil)
	ctx := context.WithValue(r.Context(), "userID", id.UserID)
	ctx = context.WithValue(ctx, "username", id.Username)
	ctx = context.WithValue(ctx, "isAdmin", id.IsAdmin)
	r = r.WithContext(ctx)
	if vars != nil {
		r = mux.SetURLVars(r, vars)
	}
	return r
}

func TestRequireCap_UnknownCapabilityIs500(t *testing.T) {
	r := NewResolver(&mwFakeStore{})
	wrapped := r.RequireCap("does.not.exist")(innerOK)
	rec := httptest.NewRecorder()
	wrapped(rec, reqWithIdentity(Identity{UserID: "u1"}, nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

func TestRequireCap_NoIdentityIs401(t *testing.T) {
	r := NewResolver(&mwFakeStore{})
	wrapped := r.RequireCap("users.read")(innerOK)
	rec := httptest.NewRecorder()
	wrapped(rec, reqWithIdentity(Identity{}, nil)) // empty identity
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestRequireCap_AdminPanelCapPasses(t *testing.T) {
	r := NewResolver(&mwFakeStore{})
	wrapped := r.RequireCap("users.write")(innerOK)
	rec := httptest.NewRecorder()
	wrapped(rec, reqWithIdentity(Identity{UserID: "root", IsAdmin: true}, nil))
	if rec.Code != innerStatus {
		t.Fatalf("status = %d, want inner %d", rec.Code, innerStatus)
	}
}

func TestRequireCap_OrdinaryUserPanelCapDenied403(t *testing.T) {
	r := NewResolver(&mwFakeStore{})
	wrapped := r.RequireCap("users.read")(innerOK)
	rec := httptest.NewRecorder()
	wrapped(rec, reqWithIdentity(Identity{UserID: "u1"}, nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestRequireCap_ServerCapMissingPathVarDenied403(t *testing.T) {
	r := NewResolver(&mwFakeStore{})
	wrapped := r.RequireCap("files.write")(innerOK) // SERVER scope, no {id}/{uuid}
	rec := httptest.NewRecorder()
	wrapped(rec, reqWithIdentity(Identity{UserID: "u1"}, nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestRequireCap_ServerOwnerPassesViaIDVar(t *testing.T) {
	fs := &mwFakeStore{}
	fs.servers = map[int]*models.Server{5: {ID: 5, OwnerID: "u1", OwnerName: "alice"}}
	r := NewResolver(fs)
	wrapped := r.RequireCap("files.write")(innerOK)
	rec := httptest.NewRecorder()
	wrapped(rec, reqWithIdentity(Identity{UserID: "u1", Username: "alice"}, map[string]string{"id": "5"}))
	if rec.Code != innerStatus {
		t.Fatalf("status = %d, want inner %d", rec.Code, innerStatus)
	}
}

func TestRequireCap_ServerOwnerPassesViaUUIDVar(t *testing.T) {
	fs := &mwFakeStore{}
	fs.servers = map[int]*models.Server{5: {ID: 5, OwnerID: "u1", OwnerName: "alice"}}
	fs.serversByUUID = map[string]*models.Server{"srv-uuid": {ID: 5, OwnerID: "u1", OwnerName: "alice"}}
	r := NewResolver(fs)
	wrapped := r.RequireCap("files.write")(innerOK)
	rec := httptest.NewRecorder()
	wrapped(rec, reqWithIdentity(Identity{UserID: "u1", Username: "alice"}, map[string]string{"uuid": "srv-uuid"}))
	if rec.Code != innerStatus {
		t.Fatalf("status = %d, want inner %d", rec.Code, innerStatus)
	}
}
