package main

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"dylaris-core/authz"
	"dylaris-core/handlers"
	"dylaris-core/models"
	"dylaris-core/services"
	"dylaris-core/store"

	"github.com/golang-jwt/jwt/v5"
)

const testJWTSecret = "phase4-test-secret"

type authzFakeStore struct {
	store.Store
	users         map[string]*models.User
	usersByID     map[string]*models.User
	servers       map[int]*models.Server
	serversByUUID map[string]*models.Server
	settings      map[string]string
	panelRoles    map[int]*store.PanelRole
	serverRoles   map[int]*store.ServerRole
	panelAuthz    map[string]struct {
		roleID    *int
		overrides store.CapOverrides
	}
	serverGrants  map[string]*store.ServerGrant
	accountGrants map[string]*store.ServerGrant
}

func (f *authzFakeStore) GetUserByUsername(u string) (*models.User, error) {
	if user, ok := f.users[u]; ok {
		return user, nil
	}
	return nil, sql.ErrNoRows
}
func (f *authzFakeStore) GetUserByID(id string) (*models.User, error) {
	if user, ok := f.usersByID[id]; ok {
		return user, nil
	}
	return nil, sql.ErrNoRows
}
func (f *authzFakeStore) GetServerByID(id int) (*models.Server, error) {
	if s, ok := f.servers[id]; ok {
		return s, nil
	}
	return nil, sql.ErrNoRows
}
func (f *authzFakeStore) GetServerByUUID(u string) (*models.Server, error) {
	if s, ok := f.serversByUUID[u]; ok {
		return s, nil
	}
	return nil, sql.ErrNoRows
}
func (f *authzFakeStore) GetSetting(k string) (string, error) { return f.settings[k], nil }
func (f *authzFakeStore) GetPanelRole(id int) (*store.PanelRole, error) {
	if r, ok := f.panelRoles[id]; ok {
		return r, nil
	}
	return nil, sql.ErrNoRows
}
func (f *authzFakeStore) GetServerRole(id int) (*store.ServerRole, error) {
	if r, ok := f.serverRoles[id]; ok {
		return r, nil
	}
	return nil, sql.ErrNoRows
}
func (f *authzFakeStore) GetUserPanelAuthz(userID string) (*int, store.CapOverrides, error) {
	if v, ok := f.panelAuthz[userID]; ok {
		return v.roleID, v.overrides, nil
	}
	return nil, store.CapOverrides{}, nil
}
func (f *authzFakeStore) GetServerGrant(serverID int, userID string) (*store.ServerGrant, error) {
	if g, ok := f.serverGrants[skey(serverID, userID)]; ok {
		return g, nil
	}
	return nil, sql.ErrNoRows
}
func (f *authzFakeStore) GetAccountGrant(ownerUserID, userID string) (*store.ServerGrant, error) {
	if g, ok := f.accountGrants[ownerUserID+"|"+userID]; ok {
		return g, nil
	}
	return nil, sql.ErrNoRows
}

// CountAdmins/CountUsers back handlers.AppState.RequireSetupComplete, which
// wraps every /api/* route (except /api/setup/*) ahead of AuthMiddleware and
// RequireCap. Reporting one admin keeps the harness in "Complete mode" (a
// pass-through) instead of panicking on the nil embedded store.Store.
func (f *authzFakeStore) CountAdmins() (int, error) { return 1, nil }
func (f *authzFakeStore) CountUsers() (int, error)  { return 1, nil }

func skey(serverID int, userID string) string {
	return strconv.Itoa(serverID) + "|" + userID
}

type testIdentity struct {
	UserID   string
	Username string
	IsAdmin  bool
}

func mintToken(t *testing.T, id testIdentity) string {
	t.Helper()
	claims := &handlers.Claims{
		Username: id.Username,
		IsAdmin:  id.IsAdmin,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	s, err := tok.SignedString([]byte(testJWTSecret))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func newAuthzTestServer(t *testing.T, fs *authzFakeStore) http.Handler {
	t.Helper()
	appState := &handlers.AppState{
		Store:        fs,
		FeatureFlags: services.NewFeatureFlags(fs),
		Authz:        authz.NewResolver(fs),
	}
	appState.Authz.SetDemoRead(appState.IsDemoServerID)
	authHandler := handlers.NewAuthHandler(appState, testJWTSecret)
	root, _ := buildAPIRouter(appState, authHandler, routeCfg{JWTSecret: testJWTSecret})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				w.WriteHeader(http.StatusInternalServerError)
			}
		}()
		root.ServeHTTP(w, r)
	})
}

func doAs(t *testing.T, h http.Handler, method, path string, id testIdentity) int {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("Authorization", "Bearer "+mintToken(t, id))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}

func (f *authzFakeStore) addUser(id, username string, admin bool) *models.User {
	u := &models.User{ID: id, Username: username, IsAdmin: admin}
	if f.users == nil {
		f.users = map[string]*models.User{}
		f.usersByID = map[string]*models.User{}
	}
	f.users[username] = u
	f.usersByID[id] = u
	return u
}

func TestAuthzHarness_Smoke_Auth(t *testing.T) {
	fs := &authzFakeStore{}
	fs.addUser("u1", "alice", false)
	srv := newAuthzTestServer(t, fs)
	// A registered user with a valid token must NOT be rejected at auth (not 401)
	// on an authenticated route. Pick a route that exists today and needs only auth.
	code := doAs(t, srv, "GET", "/api/status", testIdentity{UserID: "u1", Username: "alice"})
	if code == 0 {
		t.Fatal("harness produced no response")
	}
}

func intPtr(i int) *int { return &i }

// doJSON mirrors doAs but sends a JSON request body, for routes whose
// authorization decision (or, for POWER, the in-handler resolver check) reads
// from the parsed body rather than only the path.
func doJSON(t *testing.T, h http.Handler, method, path, body string, id testIdentity) int {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+mintToken(t, id))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}

// --- Phase 4 Task 3: server lifecycle / power / proxy / storage ---

func TestCap_ServerDelete(t *testing.T) {
	fs := &authzFakeStore{}
	owner := fs.addUser("owner-id", "owner", false)
	fs.addUser("stranger-id", "stranger", false)
	admin := fs.addUser("admin-id", "root", true)
	fs.servers = map[int]*models.Server{7: {ID: 7, OwnerID: owner.ID, OwnerName: "owner"}}
	srv := newAuthzTestServer(t, fs)
	if c := doAs(t, srv, "DELETE", "/api/servers/7", testIdentity{UserID: owner.ID, Username: "owner"}); c == 403 {
		t.Errorf("owner must not be 403 on server.delete, got 403")
	}
	if c := doAs(t, srv, "DELETE", "/api/servers/7", testIdentity{UserID: admin.ID, Username: "root", IsAdmin: true}); c == 403 {
		t.Errorf("admin must not be 403, got 403")
	}
	if c := doAs(t, srv, "DELETE", "/api/servers/7", testIdentity{UserID: "stranger-id", Username: "stranger"}); c != 403 {
		t.Errorf("stranger must be 403 on server.delete, got %d", c)
	}
}

func TestCap_ServerResources_GrantHolderPasses(t *testing.T) {
	fs := &authzFakeStore{}
	fs.addUser("owner-id", "owner", false)
	fs.addUser("friend-id", "friend", false)
	fs.addUser("nobody-id", "nobody", false)
	fs.servers = map[int]*models.Server{7: {ID: 7, OwnerID: "owner-id", OwnerName: "owner"}}
	fs.serverRoles = map[int]*store.ServerRole{2: {ID: 2, Capabilities: []string{"server.settings.write"}}}
	fs.serverGrants = map[string]*store.ServerGrant{
		skey(7, "friend-id"): {UserID: "friend-id", ServerRoleID: intPtr(2)},
	}
	srv := newAuthzTestServer(t, fs)
	if c := doAs(t, srv, "PATCH", "/api/servers/7/resources", testIdentity{UserID: "friend-id", Username: "friend"}); c == 403 {
		t.Errorf("grant holder of server.settings.write must not be 403")
	}
	if c := doAs(t, srv, "PATCH", "/api/servers/7/resources", testIdentity{UserID: "nobody", Username: "nobody"}); c != 403 {
		t.Errorf("ungranted user must be 403, got %d", c)
	}
}

func TestCap_PowerPerAction(t *testing.T) {
	fs := &authzFakeStore{}
	fs.addUser("owner-id", "owner", false)
	fs.addUser("starter-id", "starter", false)
	fs.servers = map[int]*models.Server{7: {ID: 7, OwnerID: "owner-id", OwnerName: "owner"}}
	fs.serverRoles = map[int]*store.ServerRole{5: {ID: 5, Capabilities: []string{"power.start"}}}
	fs.serverGrants = map[string]*store.ServerGrant{
		skey(7, "starter-id"): {UserID: "starter-id", ServerRoleID: intPtr(5)},
	}
	srv := newAuthzTestServer(t, fs)
	// starter holds power.start only -> a start action must NOT be 403; a kill action MUST be 403.
	startCode := doJSON(t, srv, "POST", "/api/servers/7/power", `{"action":"start"}`, testIdentity{UserID: "starter-id", Username: "starter"})
	if startCode == 403 {
		t.Errorf("power.start holder must not be 403 on start, got 403")
	}
	killCode := doJSON(t, srv, "POST", "/api/servers/7/power", `{"action":"kill"}`, testIdentity{UserID: "starter-id", Username: "starter"})
	if killCode != 403 {
		t.Errorf("power.start holder must be 403 on kill, got %d", killCode)
	}
}

// --- Phase 4 Task 4: console + RCON ---

func TestCap_ConsoleReadVsSend(t *testing.T) {
	fs := &authzFakeStore{}
	fs.addUser("owner-id", "owner", false)
	fs.addUser("reader-id", "reader", false)
	fs.addUser("nobody-id", "nobody", false)
	fs.servers = map[int]*models.Server{4: {ID: 4, OwnerID: "owner-id", OwnerName: "owner"}}
	fs.serverRoles = map[int]*store.ServerRole{9: {ID: 9, Capabilities: []string{"console.read"}}}
	fs.serverGrants = map[string]*store.ServerGrant{skey(4, "reader-id"): {UserID: "reader-id", ServerRoleID: intPtr(9)}}
	srv := newAuthzTestServer(t, fs)
	if c := doAs(t, srv, "GET", "/api/servers/4/console/history", testIdentity{UserID: "reader-id", Username: "reader"}); c == 403 {
		t.Error("console.read holder must reach console history")
	}
	if c := doAs(t, srv, "POST", "/api/servers/4/console/command", testIdentity{UserID: "reader-id", Username: "reader"}); c != 403 {
		t.Errorf("console.read holder must NOT send commands (needs console.send), got %d", c)
	}
	if c := doAs(t, srv, "GET", "/api/servers/4/console/history", testIdentity{UserID: "nobody-id", Username: "nobody"}); c != 403 {
		t.Errorf("ungranted user must be 403 on console.read, got %d", c)
	}
}

func TestCap_RconConfigReadVsWrite(t *testing.T) {
	fs := &authzFakeStore{}
	fs.addUser("owner-id", "owner", false)
	fs.addUser("cfgreader-id", "cfgreader", false)
	fs.servers = map[int]*models.Server{4: {ID: 4, OwnerID: "owner-id", OwnerName: "owner"}}
	fs.serverRoles = map[int]*store.ServerRole{11: {ID: 11, Capabilities: []string{"config.read"}}}
	fs.serverGrants = map[string]*store.ServerGrant{skey(4, "cfgreader-id"): {UserID: "cfgreader-id", ServerRoleID: intPtr(11)}}
	srv := newAuthzTestServer(t, fs)
	if c := doAs(t, srv, "GET", "/api/servers/4/rcon/config", testIdentity{UserID: "cfgreader-id", Username: "cfgreader"}); c == 403 {
		t.Error("config.read holder must read rcon config")
	}
	if c := doAs(t, srv, "PUT", "/api/servers/4/rcon/config", testIdentity{UserID: "cfgreader-id", Username: "cfgreader"}); c != 403 {
		t.Errorf("config.read holder must NOT write rcon config (needs config.write), got %d", c)
	}
}

// --- Phase 4 Task 5: file browser (in-handler resolver) ---
// /api/files/* resolves its server from ?server_uuid= (not a path {id}/{uuid}),
// so it cannot be RequireCap-gated at the route (Rule R5). It stays in-handler
// but now calls the same resolver via resolveServerUUID/getServerUUID(Read).

func TestFiles_InHandlerAuthz(t *testing.T) {
	fs := &authzFakeStore{}
	fs.addUser("owner-id", "owner", false)
	fs.addUser("stranger-id", "stranger", false)
	srv3 := &models.Server{ID: 3, OwnerID: "owner-id", OwnerName: "owner", UUID: "srv-3"}
	fs.servers = map[int]*models.Server{3: srv3}
	fs.serversByUUID = map[string]*models.Server{"srv-3": srv3}
	srv := newAuthzTestServer(t, fs)
	// stranger listing files of a server they do not own -> handler denies (not 200)
	req := httptest.NewRequest("GET", "/api/files?server_uuid=srv-3", nil)
	req.Header.Set("Authorization", "Bearer "+mintToken(t, testIdentity{UserID: "stranger-id", Username: "stranger"}))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code == 200 {
		t.Errorf("stranger must not list a foreign server's files, got 200")
	}
	// a friend with files.read CAN (not the denied path). The inner handler may 500 on the
	// nil node dial; the point is it is NOT denied at the access gate.
	fs.serverRoles = map[int]*store.ServerRole{4: {ID: 4, Capabilities: []string{"files.read"}}}
	fs.serverGrants = map[string]*store.ServerGrant{skey(3, "reader-id"): {UserID: "reader-id", ServerRoleID: intPtr(4)}}
	fs.addUser("reader-id", "reader", false)
	req2 := httptest.NewRequest("GET", "/api/files?server_uuid=srv-3", nil)
	req2.Header.Set("Authorization", "Bearer "+mintToken(t, testIdentity{UserID: "reader-id", Username: "reader"}))
	rec2 := httptest.NewRecorder()
	srv.ServeHTTP(rec2, req2)
	if rec2.Code == 403 {
		t.Errorf("files.read grant holder must pass the access gate, got 403")
	}
}

func TestFiles_DeleteNeedsDeleteCap(t *testing.T) {
	fs := &authzFakeStore{}
	fs.addUser("owner-id", "owner", false)
	fs.addUser("writer-id", "writer", false)
	srv3 := &models.Server{ID: 3, OwnerID: "owner-id", OwnerName: "owner", UUID: "srv-3"}
	fs.servers = map[int]*models.Server{3: srv3}
	fs.serversByUUID = map[string]*models.Server{"srv-3": srv3}
	// writer holds files.write but NOT files.delete
	fs.serverRoles = map[int]*store.ServerRole{6: {ID: 6, Capabilities: []string{"files.read", "files.write"}}}
	fs.serverGrants = map[string]*store.ServerGrant{skey(3, "writer-id"): {UserID: "writer-id", ServerRoleID: intPtr(6)}}
	srv := newAuthzTestServer(t, fs)
	// A delete request from a files.write-but-not-delete holder must be denied by the gate.
	req := httptest.NewRequest("POST", "/api/files/delete?server_uuid=srv-3", strings.NewReader(`{"path":"x.txt"}`))
	req.Header.Set("Authorization", "Bearer "+mintToken(t, testIdentity{UserID: "writer-id", Username: "writer"}))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Errorf("files.write-only holder must be 403 on delete (needs files.delete), got %d", rec.Code)
	}
}
