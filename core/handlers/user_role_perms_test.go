package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"

	"dylaris-core/models"
	"dylaris-core/store"
)

// userRoleFakeStore embeds store.Store (nil) so it satisfies the full
// interface at compile time; only the methods SetUserRoleHandler and
// SetUserPermissionsHandler touch are overridden. Any other call would
// panic - these tests never make one.
type userRoleFakeStore struct {
	store.Store
	users      []models.User
	targetUser *models.User
	getUserErr error

	setRoleErr   error
	setRoleCalls []setRoleCall

	setPermsCalls []setPermsCall
}

type setRoleCall struct {
	userID string
	role   string
}

type setPermsCall struct {
	userID             string
	canDeleteServers   bool
	canChangeResources bool
	supportTeam        string
}

func (f *userRoleFakeStore) ListUsers() ([]models.User, error) { return f.users, nil }
func (f *userRoleFakeStore) GetUserByID(string) (*models.User, error) {
	return f.targetUser, f.getUserErr
}
func (f *userRoleFakeStore) SetUserRole(userID, role string) error {
	f.setRoleCalls = append(f.setRoleCalls, setRoleCall{userID, role})
	return f.setRoleErr
}
func (f *userRoleFakeStore) SetUserPermissionFlags(userID string, canDeleteServers, canChangeResources bool, supportTeam string) error {
	f.setPermsCalls = append(f.setPermsCalls, setPermsCall{userID, canDeleteServers, canChangeResources, supportTeam})
	return nil
}
func (f *userRoleFakeStore) InsertAuditIdentity(*models.AuditEventIdentity) error { return nil }

const testTargetID = "11111111-1111-1111-1111-111111111111"
const testOtherAdminID = "22222222-2222-2222-2222-222222222222"
const testOtherUserID = "33333333-3333-3333-3333-333333333333"

func setRoleReq(targetID, actorID string, isAdmin bool, body map[string]interface{}) *http.Request {
	b, _ := json.Marshal(body)
	r := httptest.NewRequest("PUT", "/api/admin/users/"+targetID+"/role", bytes.NewReader(b))
	r = mux.SetURLVars(r, map[string]string{"id": targetID})
	ctx := context.WithValue(r.Context(), "isAdmin", isAdmin)
	if actorID != "" {
		ctx = context.WithValue(ctx, "userID", actorID)
	}
	return r.WithContext(ctx)
}

// TestSetUserRoleHandler_SelfDemotionGuard covers the meaningful decision
// logic of SetUserRoleHandler: an admin may not demote themselves if it
// would leave zero admins, but every other combination (demoting someone
// else, or a no-op self-reassignment to admin) is unaffected by admin count.
func TestSetUserRoleHandler_SelfDemotionGuard(t *testing.T) {
	cases := []struct {
		name          string
		targetID      string
		users         []models.User
		reqRole       string
		wantStatus    int
		wantSetCalled bool
	}{
		{
			name:          "self-demote as the last admin is blocked",
			targetID:      testTargetID,
			users:         []models.User{{ID: testTargetID, IsAdmin: true}},
			reqRole:       "user",
			wantStatus:    http.StatusConflict,
			wantSetCalled: false,
		},
		{
			name:          "self-demote with another admin remaining is allowed",
			targetID:      testTargetID,
			users:         []models.User{{ID: testTargetID, IsAdmin: true}, {ID: testOtherAdminID, IsAdmin: true}},
			reqRole:       "user",
			wantStatus:    http.StatusOK,
			wantSetCalled: true,
		},
		{
			name:          "self-reassignment to admin is a no-op and skips the guard",
			targetID:      testTargetID,
			users:         []models.User{{ID: testTargetID, IsAdmin: true}},
			reqRole:       "admin",
			wantStatus:    http.StatusOK,
			wantSetCalled: true,
		},
		{
			name:          "demoting a different user skips the guard entirely",
			targetID:      testOtherUserID,
			users:         []models.User{{ID: testTargetID, IsAdmin: true}},
			reqRole:       "user",
			wantStatus:    http.StatusOK,
			wantSetCalled: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fs := &userRoleFakeStore{
				users:      tc.users,
				targetUser: &models.User{ID: tc.targetID, Role: "admin"},
			}
			h := NewUserHandler(&AppState{Store: fs})
			rec := httptest.NewRecorder()

			h.SetUserRoleHandler(rec, setRoleReq(tc.targetID, testTargetID, true, map[string]interface{}{"role": tc.reqRole}))

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if got := len(fs.setRoleCalls) == 1; got != tc.wantSetCalled {
				t.Fatalf("setRoleCalls = %+v, wantCalled %v", fs.setRoleCalls, tc.wantSetCalled)
			}
		})
	}
}

func TestSetUserRoleHandler_Forbidden(t *testing.T) {
	fs := &userRoleFakeStore{}
	h := NewUserHandler(&AppState{Store: fs})
	rec := httptest.NewRecorder()

	h.SetUserRoleHandler(rec, setRoleReq(testTargetID, "", false, map[string]interface{}{"role": "admin"}))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", rec.Code, rec.Body.String())
	}
	if len(fs.setRoleCalls) != 0 {
		t.Fatalf("expected no SetUserRole call, got %+v", fs.setRoleCalls)
	}
}

func TestSetUserRoleHandler_InvalidRole(t *testing.T) {
	fs := &userRoleFakeStore{targetUser: &models.User{ID: testTargetID}}
	h := NewUserHandler(&AppState{Store: fs})
	rec := httptest.NewRecorder()

	h.SetUserRoleHandler(rec, setRoleReq(testTargetID, testOtherUserID, true, map[string]interface{}{"role": "superadmin"}))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if len(fs.setRoleCalls) != 0 {
		t.Fatalf("expected no SetUserRole call, got %+v", fs.setRoleCalls)
	}
}

func TestSetUserRoleHandler_TargetNotFound(t *testing.T) {
	fs := &userRoleFakeStore{getUserErr: errors.New("no rows")}
	h := NewUserHandler(&AppState{Store: fs})
	rec := httptest.NewRecorder()

	h.SetUserRoleHandler(rec, setRoleReq(testTargetID, testOtherUserID, true, map[string]interface{}{"role": "admin"}))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", rec.Code, rec.Body.String())
	}
}

func setPermsReq(targetID string, isAdmin bool, body map[string]interface{}) *http.Request {
	b, _ := json.Marshal(body)
	r := httptest.NewRequest("PUT", "/api/admin/users/"+targetID+"/permissions", bytes.NewReader(b))
	r = mux.SetURLVars(r, map[string]string{"id": targetID})
	return r.WithContext(context.WithValue(r.Context(), "isAdmin", isAdmin))
}

func TestSetUserPermissionsHandler_Forbidden(t *testing.T) {
	fs := &userRoleFakeStore{}
	h := NewUserHandler(&AppState{Store: fs})
	rec := httptest.NewRecorder()

	h.SetUserPermissionsHandler(rec, setPermsReq(testTargetID, false, map[string]interface{}{"canDeleteServers": true}))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", rec.Code, rec.Body.String())
	}
	if len(fs.setPermsCalls) != 0 {
		t.Fatalf("expected no store mutation, got %+v", fs.setPermsCalls)
	}
}

// TestSetUserPermissionsHandler_Success is a thin passthrough (no branching
// beyond the admin gate); this pins the argument order/mapping so a future
// field-shuffle regresses loudly instead of silently swapping flags.
func TestSetUserPermissionsHandler_Success(t *testing.T) {
	fs := &userRoleFakeStore{}
	h := NewUserHandler(&AppState{Store: fs})
	rec := httptest.NewRecorder()
	body := map[string]interface{}{"canDeleteServers": true, "canChangeResources": false, "supportTeam": "billing"}

	h.SetUserPermissionsHandler(rec, setPermsReq(testTargetID, true, body))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if len(fs.setPermsCalls) != 1 {
		t.Fatalf("expected 1 store call, got %d", len(fs.setPermsCalls))
	}
	call := fs.setPermsCalls[0]
	if call.userID != testTargetID || !call.canDeleteServers || call.canChangeResources || call.supportTeam != "billing" {
		t.Fatalf("call = %+v, want userID=%s canDeleteServers=true canChangeResources=false supportTeam=billing", call, testTargetID)
	}
}
