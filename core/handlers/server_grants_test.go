package handlers

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"dylaris-core/authz"
	"dylaris-core/models"
	"dylaris-core/store"
)

// grantFakeStore embeds store.Store (nil). It backs BOTH the assign handler and
// the Phase-1 resolver (authz.NewResolver(fake)): it overrides GetUserByUsername
// + GetServerByID for the handler and the resolver read methods for the actor's
// cap resolution. UpsertServerGrant / DeleteServerGrant record their calls.
type grantFakeStore struct {
	store.Store
	target      *models.User
	server      *models.Server
	actorGrant  *store.ServerGrant // returned by GetServerGrant for the actor
	serverRole  *store.ServerRole
	upserts     []upsertCall
	deleteCalls int
	deleteErr   error
}

type upsertCall struct {
	serverID     *int
	userID       string
	ownerUserID  string
	serverRoleID *int
	overrides    store.CapOverrides
	inherit      bool
}

func (f *grantFakeStore) GetUserByUsername(string) (*models.User, error) {
	if f.target == nil {
		return nil, sql.ErrNoRows
	}
	return f.target, nil
}
func (f *grantFakeStore) GetServerByID(int) (*models.Server, error) {
	if f.server == nil {
		return nil, sql.ErrNoRows
	}
	return f.server, nil
}
func (f *grantFakeStore) GetUserPanelAuthz(string) (*int, store.CapOverrides, error) {
	return nil, store.CapOverrides{}, nil
}
func (f *grantFakeStore) GetServerGrant(int, string) (*store.ServerGrant, error) {
	if f.actorGrant == nil {
		return nil, sql.ErrNoRows
	}
	return f.actorGrant, nil
}
func (f *grantFakeStore) GetAccountGrant(string, string) (*store.ServerGrant, error) {
	return nil, sql.ErrNoRows
}
func (f *grantFakeStore) GetServerRole(int) (*store.ServerRole, error) {
	if f.serverRole == nil {
		return nil, sql.ErrNoRows
	}
	return f.serverRole, nil
}
func (f *grantFakeStore) UpsertServerGrant(serverID *int, userID, ownerUserID string, serverRoleID *int, overrides store.CapOverrides, inherit bool) error {
	f.upserts = append(f.upserts, upsertCall{serverID, userID, ownerUserID, serverRoleID, overrides, inherit})
	return nil
}
func (f *grantFakeStore) DeleteServerGrant(*int, string, string) error {
	f.deleteCalls++
	return f.deleteErr
}

func grantState(fs *grantFakeStore) *AppState {
	return &AppState{Store: fs, Authz: authz.NewResolver(fs)}
}

func grantReq(method string, actorID string, isAdmin bool, body interface{}) *http.Request {
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	r := httptest.NewRequest(method, "/api/grants", &buf)
	ctx := context.WithValue(r.Context(), "userID", actorID)
	ctx = context.WithValue(ctx, "username", "actor")
	ctx = context.WithValue(ctx, "isAdmin", isAdmin)
	return r.WithContext(ctx)
}

const (
	ownerA  = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa" // server owner
	actorB  = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb" // non-owner assigner
	friendC = "cccccccc-cccc-cccc-cccc-cccccccccccc" // target friend
)

func serverOwnedBy(owner string) *models.Server {
	return &models.Server{ID: 42, OwnerID: owner, OwnerName: "ownerA"}
}

// Non-owner holding members.write but NOT files.delete cannot grant files.delete.
func TestAssignGrant_NonOwnerCannotGrantCapTheyLack(t *testing.T) {
	sid := 42
	fs := &grantFakeStore{
		target: &models.User{ID: friendC, Username: "friend"},
		server: serverOwnedBy(ownerA),
		actorGrant: &store.ServerGrant{
			ServerID:     &sid,
			UserID:       actorB,
			CapOverrides: store.CapOverrides{Grant: []string{"members.write", "files.read"}},
		},
	}
	h := NewServerRolesHandler(grantState(fs))
	rec := httptest.NewRecorder()
	h.AssignGrant(rec, grantReq("POST", actorB, false, map[string]interface{}{
		"username": "friend", "serverId": 42, "grantCaps": []string{"files.delete"},
	}))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", rec.Code, rec.Body.String())
	}
	if len(fs.upserts) != 0 {
		t.Fatalf("expected no upsert, got %d", len(fs.upserts))
	}
}

// Non-owner with members.write cannot smuggle a cap they lack by REFERENCING a
// server-role that contains it (roleCaps are folded into the delegation subset check).
func TestAssignGrant_NonOwnerCannotSmuggleCapViaRole(t *testing.T) {
	sid := 42
	fs := &grantFakeStore{
		target:     &models.User{ID: friendC, Username: "friend"},
		server:     serverOwnedBy(ownerA),
		serverRole: &store.ServerRole{ID: 7, OwnerUserID: ownerA, Capabilities: []string{"files.delete"}},
		actorGrant: &store.ServerGrant{
			ServerID:     &sid,
			UserID:       actorB,
			CapOverrides: store.CapOverrides{Grant: []string{"members.write", "files.read"}},
		},
	}
	h := NewServerRolesHandler(grantState(fs))
	rec := httptest.NewRecorder()
	h.AssignGrant(rec, grantReq("POST", actorB, false, map[string]interface{}{
		"username": "friend", "serverId": 42, "serverRoleId": 7,
	}))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", rec.Code, rec.Body.String())
	}
	if len(fs.upserts) != 0 {
		t.Fatalf("expected no upsert, got %d", len(fs.upserts))
	}
}

// Non-owner holding members.write AND files.delete CAN grant files.delete.
func TestAssignGrant_NonOwnerCanGrantHeldCap(t *testing.T) {
	sid := 42
	fs := &grantFakeStore{
		target: &models.User{ID: friendC, Username: "friend"},
		server: serverOwnedBy(ownerA),
		actorGrant: &store.ServerGrant{
			ServerID:     &sid,
			UserID:       actorB,
			CapOverrides: store.CapOverrides{Grant: []string{"members.write", "files.delete"}},
		},
	}
	h := NewServerRolesHandler(grantState(fs))
	rec := httptest.NewRecorder()
	h.AssignGrant(rec, grantReq("POST", actorB, false, map[string]interface{}{
		"username": "friend", "serverId": 42, "grantCaps": []string{"files.delete"},
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if len(fs.upserts) != 1 {
		t.Fatalf("expected 1 upsert, got %d", len(fs.upserts))
	}
	if fs.upserts[0].ownerUserID != ownerA || fs.upserts[0].serverID == nil || *fs.upserts[0].serverID != 42 {
		t.Fatalf("upsert unexpected: %+v", fs.upserts[0])
	}
}

// The server owner bypasses the delegation cap entirely.
func TestAssignGrant_OwnerBypassesDelegation(t *testing.T) {
	fs := &grantFakeStore{
		target: &models.User{ID: friendC, Username: "friend"},
		server: serverOwnedBy(ownerA), // actor IS ownerA
	}
	h := NewServerRolesHandler(grantState(fs))
	rec := httptest.NewRecorder()
	h.AssignGrant(rec, grantReq("POST", ownerA, false, map[string]interface{}{
		"username": "friend", "serverId": 42, "grantCaps": []string{"files.delete"},
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if len(fs.upserts) != 1 {
		t.Fatalf("expected 1 upsert, got %d", len(fs.upserts))
	}
}

// An admin bypasses the delegation cap on any realm.
func TestAssignGrant_AdminBypassesDelegation(t *testing.T) {
	fs := &grantFakeStore{
		target: &models.User{ID: friendC, Username: "friend"},
		server: serverOwnedBy(ownerA),
	}
	h := NewServerRolesHandler(grantState(fs))
	rec := httptest.NewRecorder()
	h.AssignGrant(rec, grantReq("POST", actorB, true, map[string]interface{}{
		"username": "friend", "serverId": 42, "grantCaps": []string{"files.delete"},
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if len(fs.upserts) != 1 {
		t.Fatalf("expected 1 upsert, got %d", len(fs.upserts))
	}
}

// A PANEL-scope cap is rejected before any store call.
func TestAssignGrant_RejectsPanelCap(t *testing.T) {
	fs := &grantFakeStore{target: &models.User{ID: friendC, Username: "friend"}, server: serverOwnedBy(ownerA)}
	h := NewServerRolesHandler(grantState(fs))
	rec := httptest.NewRecorder()
	h.AssignGrant(rec, grantReq("POST", ownerA, false, map[string]interface{}{
		"username": "friend", "serverId": 42, "grantCaps": []string{"users.read"},
	}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if len(fs.upserts) != 0 {
		t.Fatalf("expected no upsert, got %d", len(fs.upserts))
	}
}

// Account-wide assign targets the acting user's own realm (ownerUserID = actor).
func TestAssignGrant_AccountWideOwnRealm(t *testing.T) {
	fs := &grantFakeStore{target: &models.User{ID: friendC, Username: "friend"}}
	h := NewServerRolesHandler(grantState(fs))
	rec := httptest.NewRecorder()
	h.AssignGrant(rec, grantReq("POST", actorB, false, map[string]interface{}{
		"username": "friend", "grantCaps": []string{"modpack.read"},
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if len(fs.upserts) != 1 {
		t.Fatalf("expected 1 upsert, got %d", len(fs.upserts))
	}
	if fs.upserts[0].serverID != nil {
		t.Fatalf("account-wide upsert must have nil serverID, got %v", *fs.upserts[0].serverID)
	}
	if fs.upserts[0].ownerUserID != actorB {
		t.Fatalf("account-wide ownerUserID = %q, want acting user %q", fs.upserts[0].ownerUserID, actorB)
	}
}

// Revoke by the owner removes the per-server grant.
func TestRevokeGrant_OwnerRemoves(t *testing.T) {
	fs := &grantFakeStore{target: &models.User{ID: friendC, Username: "friend"}, server: serverOwnedBy(ownerA)}
	h := NewServerRolesHandler(grantState(fs))
	rec := httptest.NewRecorder()
	h.RevokeGrant(rec, grantReq("DELETE", ownerA, false, map[string]interface{}{
		"username": "friend", "serverId": 42,
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if fs.deleteCalls != 1 {
		t.Fatalf("expected 1 DeleteServerGrant call, got %d", fs.deleteCalls)
	}
}
