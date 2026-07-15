package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"dylaris-core/models"
	"dylaris-core/store"

	"github.com/gorilla/mux"
)

// rconConfigFakeStore embeds store.Store (nil) so it satisfies the full
// interface at compile time; only the methods SetConfig (and the
// checkServerAccess helper it calls) touch are overridden. Any other call
// would panic - these tests never make one.
type rconConfigFakeStore struct {
	store.Store

	server    *models.Server
	serverErr error

	invite    *models.ServerInvite
	inviteErr error

	rconEnabled  bool
	rconPort     int
	rconPassword string
	getRconErr   error

	setRconCalls []rconSetCall
	setRconErr   error
}

type rconSetCall struct {
	serverID int
	enabled  bool
	port     int
	password string
}

func (f *rconConfigFakeStore) GetServerByID(id int) (*models.Server, error) {
	return f.server, f.serverErr
}

func (f *rconConfigFakeStore) GetInvite(serverID int, userID string) (*models.ServerInvite, error) {
	return f.invite, f.inviteErr
}

func (f *rconConfigFakeStore) GetServerRconConfig(serverID int) (bool, int, string, error) {
	return f.rconEnabled, f.rconPort, f.rconPassword, f.getRconErr
}

func (f *rconConfigFakeStore) SetServerRconConfig(serverID int, enabled bool, port int, password string) error {
	f.setRconCalls = append(f.setRconCalls, rconSetCall{serverID, enabled, port, password})
	return f.setRconErr
}

func newRconConfigHandler(fs *rconConfigFakeStore) *RconHandler {
	return &RconHandler{state: &AppState{Store: fs}}
}

func rconSetConfigReq(serverID int, username string, isAdmin bool, userID string, body map[string]interface{}) *http.Request {
	b, _ := json.Marshal(body)
	r := httptest.NewRequest("PUT", "/api/servers/"+strconv.Itoa(serverID)+"/rcon/config", bytes.NewReader(b))
	r = mux.SetURLVars(r, map[string]string{"id": strconv.Itoa(serverID)})
	ctx := context.WithValue(r.Context(), "username", username)
	ctx = context.WithValue(ctx, "isAdmin", isAdmin)
	ctx = context.WithValue(ctx, "userID", userID)
	return r.WithContext(ctx)
}

func decodeRconConfigResp(t *testing.T, rec *httptest.ResponseRecorder) rconConfigResponse {
	t.Helper()
	var out rconConfigResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v (body=%s)", err, rec.Body.String())
	}
	return out
}

// --- Access control (same checkServerAccess gate GetConfig/ExecForUser use) ---

func TestRconSetConfig_AccessControl(t *testing.T) {
	t.Run("server not found", func(t *testing.T) {
		fs := &rconConfigFakeStore{serverErr: errors.New("no rows")}
		h := newRconConfigHandler(fs)
		rec := httptest.NewRecorder()
		h.SetConfig(rec, rconSetConfigReq(1, "alice", false, "u1", map[string]interface{}{"enabled": true, "port": 25575}))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("non-owner without invite is forbidden", func(t *testing.T) {
		fs := &rconConfigFakeStore{
			server:    &models.Server{ID: 1, OwnerName: "alice"},
			inviteErr: errors.New("no invite"),
		}
		h := newRconConfigHandler(fs)
		rec := httptest.NewRecorder()
		h.SetConfig(rec, rconSetConfigReq(1, "mallory", false, "u2", map[string]interface{}{"enabled": true, "port": 25575}))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403: %s", rec.Code, rec.Body.String())
		}
		if len(fs.setRconCalls) != 0 {
			t.Fatalf("expected no SetServerRconConfig calls, got %+v", fs.setRconCalls)
		}
	})

	t.Run("invited member without power permission is forbidden", func(t *testing.T) {
		fs := &rconConfigFakeStore{
			server: &models.Server{ID: 1, OwnerName: "alice"},
			invite: &models.ServerInvite{Permissions: models.TabPermissions{Power: false}},
		}
		h := newRconConfigHandler(fs)
		rec := httptest.NewRecorder()
		h.SetConfig(rec, rconSetConfigReq(1, "bob", false, "u3", map[string]interface{}{"enabled": true, "port": 25575}))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("owner (non-admin) passes", func(t *testing.T) {
		fs := &rconConfigFakeStore{server: &models.Server{ID: 1, OwnerName: "alice"}}
		h := newRconConfigHandler(fs)
		rec := httptest.NewRecorder()
		h.SetConfig(rec, rconSetConfigReq(1, "alice", false, "u1", map[string]interface{}{"enabled": false, "port": 25575}))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
		}
	})
}

func TestRconSetConfig_InvalidJSON(t *testing.T) {
	fs := &rconConfigFakeStore{server: &models.Server{ID: 1, OwnerName: "alice"}}
	h := newRconConfigHandler(fs)
	r := httptest.NewRequest("PUT", "/api/servers/1/rcon/config", bytes.NewReader([]byte("not json")))
	r = mux.SetURLVars(r, map[string]string{"id": "1"})
	ctx := context.WithValue(r.Context(), "username", "alice")
	ctx = context.WithValue(ctx, "isAdmin", false)
	ctx = context.WithValue(ctx, "userID", "u1")
	r = r.WithContext(ctx)
	rec := httptest.NewRecorder()
	h.SetConfig(rec, r)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

// --- Port-range guard ---

func TestRconSetConfig_PortRangeGuard(t *testing.T) {
	cases := []struct {
		name string
		port int
		want int
	}{
		{"negative port rejected", -1, http.StatusBadRequest},
		{"port over 65535 rejected", 65536, http.StatusBadRequest},
		{"port 0 (use existing) accepted", 0, http.StatusOK},
		{"port 25575 accepted", 25575, http.StatusOK},
		{"port 65535 (max valid) accepted", 65535, http.StatusOK},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fs := &rconConfigFakeStore{server: &models.Server{ID: 1, OwnerName: "alice"}}
			h := newRconConfigHandler(fs)
			rec := httptest.NewRecorder()
			h.SetConfig(rec, rconSetConfigReq(1, "alice", false, "u1", map[string]interface{}{"enabled": false, "port": c.port}))
			if rec.Code != c.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, c.want, rec.Body.String())
			}
			if c.want == http.StatusBadRequest && len(fs.setRconCalls) != 0 {
				t.Fatalf("expected no persist on rejected port, got %+v", fs.setRconCalls)
			}
		})
	}
}

// --- Password regen/keep branching (rcon.go:241-254) ---

func TestRconSetConfig_PasswordBranching(t *testing.T) {
	t.Run("regenerate=true always mints a new password even with one already stored", func(t *testing.T) {
		fs := &rconConfigFakeStore{
			server:       &models.Server{ID: 1, OwnerName: "alice"},
			rconEnabled:  true,
			rconPassword: "old-password",
		}
		h := newRconConfigHandler(fs)
		rec := httptest.NewRecorder()
		h.SetConfig(rec, rconSetConfigReq(1, "alice", false, "u1", map[string]interface{}{"enabled": true, "port": 25575, "regenerate": true}))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
		}
		if len(fs.setRconCalls) != 1 {
			t.Fatalf("setRconCalls = %+v, want 1 call", fs.setRconCalls)
		}
		persisted := fs.setRconCalls[0].password
		if persisted == "" || persisted == "old-password" {
			t.Fatalf("persisted password = %q, want a freshly generated value different from the old one", persisted)
		}
		resp := decodeRconConfigResp(t, rec)
		if resp.Password != persisted {
			t.Fatalf("response.Password = %q, want the newly minted password %q to be exposed once", resp.Password, persisted)
		}
	})

	t.Run("enabled=true with no stored password auto-regenerates", func(t *testing.T) {
		fs := &rconConfigFakeStore{
			server:       &models.Server{ID: 1, OwnerName: "alice"},
			rconEnabled:  false,
			rconPassword: "",
		}
		h := newRconConfigHandler(fs)
		rec := httptest.NewRecorder()
		h.SetConfig(rec, rconSetConfigReq(1, "alice", false, "u1", map[string]interface{}{"enabled": true, "port": 25575}))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
		}
		if len(fs.setRconCalls) != 1 || fs.setRconCalls[0].password == "" {
			t.Fatalf("setRconCalls = %+v, want 1 call with a non-empty generated password", fs.setRconCalls)
		}
		resp := decodeRconConfigResp(t, rec)
		if resp.Password == "" || !resp.HasSecret {
			t.Fatalf("response = %+v, want a newly minted password exposed once", resp)
		}
	})

	t.Run("enabled=true with an existing stored password and regenerate=false keeps it", func(t *testing.T) {
		fs := &rconConfigFakeStore{
			server:       &models.Server{ID: 1, OwnerName: "alice"},
			rconEnabled:  true,
			rconPassword: "existing-secret",
		}
		h := newRconConfigHandler(fs)
		rec := httptest.NewRecorder()
		h.SetConfig(rec, rconSetConfigReq(1, "alice", false, "u1", map[string]interface{}{"enabled": true, "port": 25575}))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
		}
		if len(fs.setRconCalls) != 1 || fs.setRconCalls[0].password != "existing-secret" {
			t.Fatalf("setRconCalls = %+v, want the existing password kept unchanged", fs.setRconCalls)
		}
		resp := decodeRconConfigResp(t, rec)
		if resp.Password != "" {
			t.Fatalf("response.Password = %q, want empty (never re-exposing a kept password)", resp.Password)
		}
		if !resp.HasSecret {
			t.Fatalf("response.HasSecret = false, want true (a password is set)")
		}
	})

	t.Run("an explicit request password is used verbatim when not regenerating and enabled requires none", func(t *testing.T) {
		fs := &rconConfigFakeStore{
			server:       &models.Server{ID: 1, OwnerName: "alice"},
			rconEnabled:  false,
			rconPassword: "",
		}
		h := newRconConfigHandler(fs)
		rec := httptest.NewRecorder()
		// enabled=false here, so the "Enabled && password==''" auto-regen branch
		// does not fire; the explicit req.Password is used instead.
		h.SetConfig(rec, rconSetConfigReq(1, "alice", false, "u1", map[string]interface{}{"enabled": false, "port": 25575, "password": "caller-supplied"}))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
		}
		if len(fs.setRconCalls) != 1 || fs.setRconCalls[0].password != "caller-supplied" {
			t.Fatalf("setRconCalls = %+v, want the caller-supplied password persisted verbatim", fs.setRconCalls)
		}
	})

	t.Run("disabling with no password and no regenerate leaves password empty", func(t *testing.T) {
		fs := &rconConfigFakeStore{server: &models.Server{ID: 1, OwnerName: "alice"}}
		h := newRconConfigHandler(fs)
		rec := httptest.NewRecorder()
		h.SetConfig(rec, rconSetConfigReq(1, "alice", false, "u1", map[string]interface{}{"enabled": false, "port": 25575}))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
		}
		if len(fs.setRconCalls) != 1 || fs.setRconCalls[0].password != "" {
			t.Fatalf("setRconCalls = %+v, want an empty persisted password", fs.setRconCalls)
		}
		resp := decodeRconConfigResp(t, rec)
		if resp.HasSecret {
			t.Fatalf("response.HasSecret = true, want false (no password set)")
		}
	})

	t.Run("port 0 falls back to the existing stored port", func(t *testing.T) {
		fs := &rconConfigFakeStore{
			server:      &models.Server{ID: 1, OwnerName: "alice"},
			rconEnabled: true,
			rconPort:    25580,
		}
		h := newRconConfigHandler(fs)
		rec := httptest.NewRecorder()
		h.SetConfig(rec, rconSetConfigReq(1, "alice", false, "u1", map[string]interface{}{"enabled": true, "port": 0}))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
		}
		if len(fs.setRconCalls) != 1 || fs.setRconCalls[0].port != 25580 {
			t.Fatalf("setRconCalls = %+v, want port 25580 (the existing stored value)", fs.setRconCalls)
		}
	})
}

func TestRconSetConfig_PersistFailure(t *testing.T) {
	fs := &rconConfigFakeStore{server: &models.Server{ID: 1, OwnerName: "alice"}, setRconErr: errors.New("db down")}
	h := newRconConfigHandler(fs)
	rec := httptest.NewRecorder()
	h.SetConfig(rec, rconSetConfigReq(1, "alice", false, "u1", map[string]interface{}{"enabled": false, "port": 25575}))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: %s", rec.Code, rec.Body.String())
	}
}
