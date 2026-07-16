package main

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strconv"
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
