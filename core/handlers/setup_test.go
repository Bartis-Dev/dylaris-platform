package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"dylaris-core/models"
	"dylaris-core/store"
)

// setupFakeStore embeds store.Store (nil) so it satisfies the full interface at
// compile time; only the methods CreateAdmin touches are overridden. Any other
// call would panic - the tests never make one.
type setupFakeStore struct {
	store.Store
	userCount  int
	adminCount int
	createErr  error
	created    []string
}

func (f *setupFakeStore) CountUsers() (int, error)  { return f.userCount, nil }
func (f *setupFakeStore) CountAdmins() (int, error) { return f.adminCount, nil }

func (f *setupFakeStore) CreateFirstAdmin(username, passwordHash, totpSecret string) (*models.User, error) {
	if f.createErr != nil {
		return nil, f.createErr
	}
	f.created = append(f.created, username)
	return &models.User{ID: "id-" + username, Username: username, IsAdmin: true, Role: "admin"}, nil
}

func (f *setupFakeStore) CreateAdditionalAdmin(username, passwordHash, totpSecret string) (*models.User, error) {
	if f.createErr != nil {
		return nil, f.createErr
	}
	f.created = append(f.created, username)
	return &models.User{ID: "id2-" + username, Username: username, IsAdmin: true, Role: "admin"}, nil
}

func newSetupTestHandler(fs *setupFakeStore, adminSecret string) *SetupHandler {
	state := &AppState{Store: fs, AdminSecret: adminSecret, FrontendURL: "https://panel.example.com"}
	auth := NewAuthHandler(state, "test-jwt-secret")
	return NewSetupHandler(state, auth)
}

func postCreateAdmin(h *SetupHandler, body map[string]interface{}) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/setup/admin", bytes.NewReader(b))
	rec := httptest.NewRecorder()
	h.CreateAdmin(rec, req)
	return rec
}

func TestSetupStatus_ReportsAdminSecretConfigured(t *testing.T) {
	fs := &setupFakeStore{userCount: 0, adminCount: 0}
	h := newSetupTestHandler(fs, "correct-horse-battery-staple")
	req := httptest.NewRequest("GET", "/api/setup/status", nil)
	rec := httptest.NewRecorder()
	h.Status(rec, req)

	var out struct {
		Mode                  string `json:"mode"`
		AdminSecretConfigured bool   `json:"adminSecretConfigured"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Mode != "fresh_install" || !out.AdminSecretConfigured {
		t.Fatalf("status = %+v, want fresh_install + configured", out)
	}
	if strings.Contains(rec.Body.String(), "correct-horse-battery-staple") {
		t.Fatalf("secret leaked into status body: %s", rec.Body.String())
	}
}

func TestCreateAdmin_Matrix(t *testing.T) {
	const secret = "correct-horse-battery-staple"

	cases := []struct {
		name        string
		userCount   int
		adminCount  int
		adminSecret string // configured on the server
		body        map[string]interface{}
		createErr   error
		wantStatus  int
		wantErrCode string // "" means success
	}{
		{
			name:       "fresh install unset -> 200",
			body:       map[string]interface{}{"username": "alice", "password": "password123"},
			wantStatus: http.StatusOK,
		},
		{
			name:        "fresh install set + correct -> 200",
			adminSecret: secret,
			body:        map[string]interface{}{"username": "alice", "password": "password123", "adminSecret": secret},
			wantStatus:  http.StatusOK,
		},
		{
			name:        "fresh install set + wrong -> 403 invalid_admin_secret",
			adminSecret: secret,
			body:        map[string]interface{}{"username": "alice", "password": "password123", "adminSecret": "nope"},
			wantStatus:  http.StatusForbidden, wantErrCode: "invalid_admin_secret",
		},
		{
			name:        "fresh install set + missing -> 403 invalid_admin_secret",
			adminSecret: secret,
			body:        map[string]interface{}{"username": "alice", "password": "password123"},
			wantStatus:  http.StatusForbidden, wantErrCode: "invalid_admin_secret",
		},
		{
			name:       "lost admin unset -> 403 admin_recovery_disabled",
			userCount:  3,
			body:       map[string]interface{}{"username": "alice", "password": "password123"},
			wantStatus: http.StatusForbidden, wantErrCode: "admin_recovery_disabled",
		},
		{
			name:        "lost admin set + correct -> 200",
			userCount:   3,
			adminSecret: secret,
			body:        map[string]interface{}{"username": "alice", "password": "password123", "adminSecret": secret},
			wantStatus:  http.StatusOK,
		},
		{
			name:       "complete unset -> 403 admin_recovery_disabled",
			userCount:  1,
			adminCount: 1,
			body:       map[string]interface{}{"username": "backup", "password": "password123"},
			wantStatus: http.StatusForbidden, wantErrCode: "admin_recovery_disabled",
		},
		{
			name:        "complete set + correct -> 200 (break-glass additional admin)",
			userCount:   1,
			adminCount:  1,
			adminSecret: secret,
			body:        map[string]interface{}{"username": "backup", "password": "password123", "adminSecret": secret},
			wantStatus:  http.StatusOK,
		},
		{
			name:        "complete set + wrong -> 403 invalid_admin_secret",
			userCount:   1,
			adminCount:  1,
			adminSecret: secret,
			body:        map[string]interface{}{"username": "backup", "password": "password123", "adminSecret": "nope"},
			wantStatus:  http.StatusForbidden, wantErrCode: "invalid_admin_secret",
		},
		{
			name:       "allowed but bad username -> 400 invalid_username",
			body:       map[string]interface{}{"username": "!!", "password": "password123"},
			wantStatus: http.StatusBadRequest, wantErrCode: "invalid_username",
		},
		{
			name:       "allowed but short password -> 400 invalid_password",
			body:       map[string]interface{}{"username": "alice", "password": "short"},
			wantStatus: http.StatusBadRequest, wantErrCode: "invalid_password",
		},
		{
			name:       "first-admin race lost -> 409 setup_already_complete",
			body:       map[string]interface{}{"username": "alice", "password": "password123"},
			createErr:  store.ErrSetupAlreadyComplete,
			wantStatus: http.StatusConflict, wantErrCode: "setup_already_complete",
		},
		{
			name:        "break-glass username collision -> 409 username_taken",
			userCount:   1,
			adminCount:  1,
			adminSecret: secret,
			body:        map[string]interface{}{"username": "backup", "password": "password123", "adminSecret": secret},
			createErr:   store.ErrUsernameTaken,
			wantStatus:  http.StatusConflict, wantErrCode: "username_taken",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fs := &setupFakeStore{userCount: tc.userCount, adminCount: tc.adminCount, createErr: tc.createErr}
			h := newSetupTestHandler(fs, tc.adminSecret)
			rec := postCreateAdmin(h, tc.body)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.wantStatus, rec.Body.String())
			}

			var out struct {
				Success bool   `json:"success"`
				Error   string `json:"error"`
				Token   string `json:"token"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if tc.wantErrCode == "" {
				if !out.Success || out.Token == "" {
					t.Fatalf("expected success+token, got %s", rec.Body.String())
				}
			} else if out.Error != tc.wantErrCode {
				t.Fatalf("error = %q, want %q: %s", out.Error, tc.wantErrCode, rec.Body.String())
			}

			// The configured secret must NEVER appear in any response body.
			if tc.adminSecret != "" && strings.Contains(rec.Body.String(), tc.adminSecret) {
				t.Fatalf("secret leaked into response body: %s", rec.Body.String())
			}
		})
	}
}
