package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"

	"dylaris-core/authz"
	"dylaris-core/models"
	"dylaris-core/store"
)

// apiKeysAuthFakeStore embeds store.Store (nil) so it satisfies the full
// interface at compile time; only the methods the external API-key auth
// chain (APIKeyMiddleware) and Create touch are overridden. Any other call
// would panic - these tests never make one.
type apiKeysAuthFakeStore struct {
	store.Store

	keysByHash map[string]*models.APIKey

	servers      map[string]*models.Server // by UUID, for Create's non-admin scoping check
	getServerErr error

	// grants lets a test hand a caller an explicit per-server grant for the
	// resolver-based delegation subset check in Create. Key: grantKey(serverID, userID).
	grants map[string]*store.ServerGrant

	createCalls []*models.APIKey
	createErr   error
	nextID      int
}

// grantKey lets a test hand a friend an explicit per-server grant.
// Absent = no grant (deny).
func grantKey(serverID int, userID string) string { return fmt.Sprintf("%d:%s", serverID, userID) }

func (f *apiKeysAuthFakeStore) GetAPIKeyByHash(hash string) (*models.APIKey, error) {
	k, ok := f.keysByHash[hash]
	if !ok {
		return nil, errors.New("not found")
	}
	return k, nil
}

func (f *apiKeysAuthFakeStore) TouchAPIKey(id int) error { return nil }

func (f *apiKeysAuthFakeStore) GetServerByUUID(uuid string) (*models.Server, error) {
	if f.getServerErr != nil {
		return nil, f.getServerErr
	}
	srv, ok := f.servers[uuid]
	if !ok {
		return nil, errors.New("server not found")
	}
	return srv, nil
}

// GetInvite: the non-admin scoping check in Create falls through to this
// whenever the caller is not the server owner. None of these tests exercise
// an actual invite grant, so it always reports "no invite" (deny).
func (f *apiKeysAuthFakeStore) GetInvite(serverID int, userID string) (*models.ServerInvite, error) {
	return nil, errors.New("no invite")
}

// The methods below satisfy authz.Store so h.state.Authz.Resolve does not
// panic on the embedded nil store.Store. Deny-by-default except GetServerByID
// (backed by the same `servers` map keyed by UUID, looked up by ID here) and
// GetServerGrant (backed by the configurable `grants` map).
func (f *apiKeysAuthFakeStore) GetUserPanelAuthz(userID string) (*int, store.CapOverrides, error) {
	return nil, store.CapOverrides{}, nil
}

func (f *apiKeysAuthFakeStore) GetServerByID(id int) (*models.Server, error) {
	for _, s := range f.servers {
		if s.ID == id {
			return s, nil
		}
	}
	return nil, errors.New("server not found")
}

func (f *apiKeysAuthFakeStore) GetServerGrant(serverID int, userID string) (*store.ServerGrant, error) {
	if g, ok := f.grants[grantKey(serverID, userID)]; ok {
		return g, nil
	}
	return nil, nil
}

func (f *apiKeysAuthFakeStore) GetAccountGrant(ownerUserID, userID string) (*store.ServerGrant, error) {
	return nil, nil
}

func (f *apiKeysAuthFakeStore) CreateAPIKey(k *models.APIKey) (int, error) {
	if f.createErr != nil {
		return 0, f.createErr
	}
	f.nextID++
	f.createCalls = append(f.createCalls, k)
	return f.nextID, nil
}

func newAPIKeysAuthHandler(fs *apiKeysAuthFakeStore) *APIKeysHandler {
	return NewAPIKeysHandler(&AppState{Store: fs, Authz: authz.NewResolver(fs)})
}

// sentinelStatus/sentinelBody mark that the wrapped inner handler (the real
// external endpoint) was actually reached, as opposed to the middleware
// answering the request itself.
const (
	sentinelStatus = http.StatusTeapot
	sentinelBody   = "inner-handler-reached"
)

func sentinelInner(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(sentinelStatus)
	w.Write([]byte(sentinelBody))
}

// TestAPIKeyMiddleware_Chain covers every rejection branch of the external
// auth chain (api_keys.go: APIKeyMiddleware) plus the success passthrough.
// Each case gets its own fresh handler/limiters so per-IP and per-key
// rate-limit state never leaks between cases; the dedicated rate-limit tests
// below cover the throttle branches on purpose.
func TestAPIKeyMiddleware_Chain(t *testing.T) {
	future := time.Now().Add(time.Hour)
	past := time.Now().Add(-time.Hour)

	cases := []struct {
		name           string
		authHeader     string
		setAuthHeader  bool
		key            *models.APIKey
		requiredPerm   string
		uuidVar        string
		wantStatus     int
		wantBodySub    string
		wantInnerCalled bool
	}{
		{
			name:          "missing Authorization header",
			setAuthHeader: false,
			wantStatus:    http.StatusUnauthorized,
			wantBodySub:   "Missing Authorization",
		},
		{
			name:          "Authorization without Bearer prefix",
			setAuthHeader: true,
			authHeader:    "Token abc123",
			wantStatus:    http.StatusUnauthorized,
			wantBodySub:   "Missing Authorization",
		},
		{
			name:          "Bearer with only whitespace trims to an empty key",
			setAuthHeader: true,
			authHeader:    "Bearer    ",
			wantStatus:    http.StatusUnauthorized,
			wantBodySub:   "Invalid key",
		},
		{
			name:          "Bearer dyl_ prefix stripped, hash unknown to the store",
			setAuthHeader: true,
			authHeader:    "Bearer dyl_unknowntoken",
			wantStatus:    http.StatusUnauthorized,
			wantBodySub:   "Invalid key",
		},
		{
			name:          "Bearer without the dyl_ prefix still resolves (prefix strip is optional)",
			setAuthHeader: true,
			authHeader:    "Bearer noprefixtoken",
			key:           &models.APIKey{ID: 1, RatePerMin: 1000, Scope: models.APIKeyScope{Permissions: []string{"rcon.exec"}}},
			requiredPerm:  "rcon.exec",
			wantInnerCalled: true,
		},
		{
			name:          "revoked key rejected",
			setAuthHeader: true,
			authHeader:    "Bearer revokedtoken",
			key:           &models.APIKey{ID: 2, RatePerMin: 1000, RevokedAt: &past, Scope: models.APIKeyScope{Permissions: []string{"rcon.exec"}}},
			requiredPerm:  "rcon.exec",
			wantStatus:    http.StatusUnauthorized,
			wantBodySub:   "Key revoked",
		},
		{
			name:          "expired key rejected",
			setAuthHeader: true,
			authHeader:    "Bearer expiredtoken",
			key:           &models.APIKey{ID: 3, RatePerMin: 1000, ExpiresAt: &past, Scope: models.APIKeyScope{Permissions: []string{"rcon.exec"}}},
			requiredPerm:  "rcon.exec",
			wantStatus:    http.StatusUnauthorized,
			wantBodySub:   "Key expired",
		},
		{
			name:          "not-yet-expired key (ExpiresAt in the future) is accepted",
			setAuthHeader: true,
			authHeader:    "Bearer notyetexpired",
			key:           &models.APIKey{ID: 4, RatePerMin: 1000, ExpiresAt: &future, Scope: models.APIKeyScope{Permissions: []string{"rcon.exec"}}},
			requiredPerm:  "rcon.exec",
			wantInnerCalled: true,
		},
		{
			name:          "key lacks the required permission",
			setAuthHeader: true,
			authHeader:    "Bearer nopermtoken",
			key:           &models.APIKey{ID: 5, RatePerMin: 1000, Scope: models.APIKeyScope{Permissions: []string{"other.perm"}}},
			requiredPerm:  "rcon.exec",
			wantStatus:    http.StatusForbidden,
			wantBodySub:   "Key lacks required permission",
		},
		{
			name:          "path server uuid outside the key's scope",
			setAuthHeader: true,
			authHeader:    "Bearer scopedtoken",
			key:           &models.APIKey{ID: 6, RatePerMin: 1000, Scope: models.APIKeyScope{Permissions: []string{"rcon.exec"}, Servers: []string{"other-server-uuid"}}},
			requiredPerm:  "rcon.exec",
			uuidVar:       "target-server-uuid",
			wantStatus:    http.StatusForbidden,
			wantBodySub:   "Key not scoped to this server",
		},
		{
			name:          "path server uuid within the key's scope succeeds",
			setAuthHeader: true,
			authHeader:    "Bearer inscopetoken",
			key:           &models.APIKey{ID: 7, RatePerMin: 1000, Scope: models.APIKeyScope{Permissions: []string{"rcon.exec"}, Servers: []string{"target-server-uuid"}}},
			requiredPerm:  "rcon.exec",
			uuidVar:       "target-server-uuid",
			wantInnerCalled: true,
		},
		{
			name:          "no server uuid in the path skips the AllowsServer check entirely",
			setAuthHeader: true,
			authHeader:    "Bearer noserverintoken",
			key:           &models.APIKey{ID: 8, RatePerMin: 1000, Scope: models.APIKeyScope{Permissions: []string{"rcon.exec"}}},
			requiredPerm:  "rcon.exec",
			wantInnerCalled: true,
		},
		{
			name:          "non-rcon server cap honored",
			setAuthHeader: true,
			authHeader:    "Bearer powerstarttoken",
			key:           &models.APIKey{ID: 9, RatePerMin: 1000, Scope: models.APIKeyScope{Permissions: []string{"power.start"}}},
			requiredPerm:  "power.start",
			wantInnerCalled: true,
		},
		{
			name:          "server cap honored within scope",
			setAuthHeader: true,
			authHeader:    "Bearer filesreadtoken",
			key:           &models.APIKey{ID: 10, RatePerMin: 1000, Scope: models.APIKeyScope{Permissions: []string{"files.read"}, Servers: []string{"target-server-uuid"}}},
			requiredPerm:  "files.read",
			uuidVar:       "target-server-uuid",
			wantInnerCalled: true,
		},
		{
			name:          "panel cap on a key is never honored",
			setAuthHeader: true,
			authHeader:    "Bearer usersreadtoken",
			key:           &models.APIKey{ID: 11, RatePerMin: 1000, Scope: models.APIKeyScope{Permissions: []string{"users.read"}}},
			requiredPerm:  "users.read",
			wantStatus:    http.StatusForbidden,
			wantBodySub:   "Key lacks required permission",
		},
		{
			name:          "key carrying rcon.exec denied a different required cap",
			setAuthHeader: true,
			authHeader:    "Bearer rconexectoken",
			key:           &models.APIKey{ID: 12, RatePerMin: 1000, Scope: models.APIKeyScope{Permissions: []string{"rcon.exec"}}},
			requiredPerm:  "files.read",
			wantStatus:    http.StatusForbidden,
			wantBodySub:   "Key lacks required permission",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fs := &apiKeysAuthFakeStore{keysByHash: map[string]*models.APIKey{}}
			if c.key != nil {
				plaintext := strings.TrimPrefix(strings.TrimSpace(strings.TrimPrefix(c.authHeader, "Bearer ")), "dyl_")
				fs.keysByHash[HashAPIKey(plaintext)] = c.key
			}
			h := newAPIKeysAuthHandler(fs)
			wrapped := h.APIKeyMiddleware(c.requiredPerm)(sentinelInner)

			r := httptest.NewRequest("POST", "/api/external/rcon", nil)
			if c.setAuthHeader {
				r.Header.Set("Authorization", c.authHeader)
			}
			if c.uuidVar != "" {
				r = mux.SetURLVars(r, map[string]string{"uuid": c.uuidVar})
			}
			rec := httptest.NewRecorder()

			wrapped(rec, r)

			if c.wantInnerCalled {
				if rec.Code != sentinelStatus || rec.Body.String() != sentinelBody {
					t.Fatalf("expected inner handler to be reached (status %d, body %q), got status %d body %q",
						sentinelStatus, sentinelBody, rec.Code, rec.Body.String())
				}
				return
			}
			if rec.Code != c.wantStatus {
				t.Fatalf("status = %d, want %d: %s", rec.Code, c.wantStatus, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), c.wantBodySub) {
				t.Fatalf("body = %s, want substring %q", rec.Body.String(), c.wantBodySub)
			}
		})
	}
}

// TestAPIKeyMiddleware_PerKeyRateLimitExceeded pins the per-key sliding
// window: with RatePerMin=1, a second request for the same key inside the
// same minute is throttled with 429 + Retry-After, while the first reaches
// the inner handler.
func TestAPIKeyMiddleware_PerKeyRateLimitExceeded(t *testing.T) {
	key := &models.APIKey{ID: 42, RatePerMin: 1, Scope: models.APIKeyScope{Permissions: []string{"rcon.exec"}}}
	fs := &apiKeysAuthFakeStore{keysByHash: map[string]*models.APIKey{HashAPIKey("ratelimited"): key}}
	h := newAPIKeysAuthHandler(fs)
	wrapped := h.APIKeyMiddleware("rcon.exec")(sentinelInner)

	r1 := httptest.NewRequest("POST", "/api/external/rcon", nil)
	r1.Header.Set("Authorization", "Bearer ratelimited")
	rec1 := httptest.NewRecorder()
	wrapped(rec1, r1)
	if rec1.Code != sentinelStatus {
		t.Fatalf("first request: status = %d, want inner handler reached (%d): %s", rec1.Code, sentinelStatus, rec1.Body.String())
	}

	r2 := httptest.NewRequest("POST", "/api/external/rcon", nil)
	r2.Header.Set("Authorization", "Bearer ratelimited")
	rec2 := httptest.NewRecorder()
	wrapped(rec2, r2)
	if rec2.Code != http.StatusTooManyRequests {
		t.Fatalf("second request: status = %d, want 429: %s", rec2.Code, rec2.Body.String())
	}
	if rec2.Header().Get("Retry-After") != "60" {
		t.Fatalf("Retry-After = %q, want 60", rec2.Header().Get("Retry-After"))
	}
	if !strings.Contains(rec2.Body.String(), "Rate limit exceeded") {
		t.Fatalf("body = %s, want 'Rate limit exceeded'", rec2.Body.String())
	}
}

// TestAPIKeyMiddleware_PerIPThrottleExceeded pins the pre-lookup per-IP
// throttle (120/min, hardcoded in api_keys.go): it must trip BEFORE the
// store hash lookup so invalid-key guessing can't hammer the DB unbounded.
// All requests here use an unknown key hash, so every response up to the
// throttle threshold is "Invalid key" (401) and only the 121st request
// becomes "Too many requests" (429).
func TestAPIKeyMiddleware_PerIPThrottleExceeded(t *testing.T) {
	fs := &apiKeysAuthFakeStore{keysByHash: map[string]*models.APIKey{}}
	h := newAPIKeysAuthHandler(fs)
	wrapped := h.APIKeyMiddleware("rcon.exec")(sentinelInner)

	for i := 1; i <= 120; i++ {
		r := httptest.NewRequest("POST", "/api/external/rcon", nil)
		r.Header.Set("Authorization", "Bearer dyl_unknownguess")
		rec := httptest.NewRecorder()
		wrapped(rec, r)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("request %d: status = %d, want 401 (invalid key, IP throttle not yet tripped): %s", i, rec.Code, rec.Body.String())
		}
	}

	r := httptest.NewRequest("POST", "/api/external/rcon", nil)
	r.Header.Set("Authorization", "Bearer dyl_unknownguess")
	rec := httptest.NewRecorder()
	wrapped(rec, r)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("request 121: status = %d, want 429 (IP throttle should trip at 120/min): %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Retry-After") != "60" {
		t.Fatalf("Retry-After = %q, want 60", rec.Header().Get("Retry-After"))
	}
	if !strings.Contains(rec.Body.String(), "Too many requests") {
		t.Fatalf("body = %s, want 'Too many requests'", rec.Body.String())
	}
}

// --- Create ---

func createAPIKeyReq(userID, username string, isAdmin bool, body map[string]interface{}) *http.Request {
	b, _ := json.Marshal(body)
	r := httptest.NewRequest("POST", "/api/api-keys", bytes.NewReader(b))
	ctx := context.WithValue(r.Context(), "isAdmin", isAdmin)
	if userID != "" {
		ctx = context.WithValue(ctx, "userID", userID)
	}
	ctx = context.WithValue(ctx, "username", username)
	return r.WithContext(ctx)
}

func TestAPIKeysCreate_Unauthorized(t *testing.T) {
	fs := &apiKeysAuthFakeStore{}
	h := newAPIKeysAuthHandler(fs)
	rec := httptest.NewRecorder()

	h.Create(rec, createAPIKeyReq("", "", false, map[string]interface{}{"name": "k", "permissions": []string{"rcon.exec"}}))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401: %s", rec.Code, rec.Body.String())
	}
}

func TestAPIKeysCreate_InvalidJSON(t *testing.T) {
	fs := &apiKeysAuthFakeStore{}
	h := newAPIKeysAuthHandler(fs)
	r := httptest.NewRequest("POST", "/api/api-keys", bytes.NewReader([]byte("not json")))
	r = r.WithContext(context.WithValue(context.WithValue(r.Context(), "userID", "u1"), "isAdmin", true))
	rec := httptest.NewRecorder()

	h.Create(rec, r)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

func TestAPIKeysCreate_NameRequired(t *testing.T) {
	fs := &apiKeysAuthFakeStore{}
	h := newAPIKeysAuthHandler(fs)
	rec := httptest.NewRecorder()

	h.Create(rec, createAPIKeyReq("u1", "alice", true, map[string]interface{}{"name": "   ", "permissions": []string{"rcon.exec"}}))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Name required") {
		t.Fatalf("body = %s, want 'Name required'", rec.Body.String())
	}
}

func TestAPIKeysCreate_PermissionsRequired(t *testing.T) {
	fs := &apiKeysAuthFakeStore{}
	h := newAPIKeysAuthHandler(fs)
	rec := httptest.NewRecorder()

	h.Create(rec, createAPIKeyReq("u1", "alice", true, map[string]interface{}{"name": "k", "permissions": []string{}}))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "At least one permission required") {
		t.Fatalf("body = %s, want 'At least one permission required'", rec.Body.String())
	}
}

func TestAPIKeysCreate_UnknownPermissionRejected(t *testing.T) {
	fs := &apiKeysAuthFakeStore{}
	h := newAPIKeysAuthHandler(fs)
	rec := httptest.NewRecorder()

	h.Create(rec, createAPIKeyReq("u1", "alice", true, map[string]interface{}{"name": "k", "permissions": []string{"modpack.publish"}}))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Unknown permission: modpack.publish") {
		t.Fatalf("body = %s, want 'Unknown permission: modpack.publish'", rec.Body.String())
	}
	if len(fs.createCalls) != 0 {
		t.Fatalf("expected no CreateAPIKey call, got %+v", fs.createCalls)
	}
}

func TestAPIKeysCreate_NonAdminServerNotFound(t *testing.T) {
	fs := &apiKeysAuthFakeStore{servers: map[string]*models.Server{}}
	h := newAPIKeysAuthHandler(fs)
	rec := httptest.NewRecorder()

	h.Create(rec, createAPIKeyReq("u1", "alice", false, map[string]interface{}{
		"name": "k", "permissions": []string{"rcon.exec"}, "servers": []string{"missing-uuid"},
	}))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Server not found: missing-uuid") {
		t.Fatalf("body = %s, want 'Server not found: missing-uuid'", rec.Body.String())
	}
}

func TestAPIKeysCreate_NonAdminWithoutPowerAccessForbidden(t *testing.T) {
	fs := &apiKeysAuthFakeStore{servers: map[string]*models.Server{
		"srv-1": {ID: 1, UUID: "srv-1", OwnerID: "owner-else"},
	}}
	h := newAPIKeysAuthHandler(fs)
	rec := httptest.NewRecorder()

	h.Create(rec, createAPIKeyReq("u1", "alice", false, map[string]interface{}{
		"name": "k", "permissions": []string{"rcon.exec"}, "servers": []string{"srv-1"},
	}))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "No rcon.exec access to server: srv-1") {
		t.Fatalf("body = %s, want 'No rcon.exec access to server: srv-1'", rec.Body.String())
	}
	if len(fs.createCalls) != 0 {
		t.Fatalf("expected no CreateAPIKey call, got %+v", fs.createCalls)
	}
}

func TestAPIKeysCreate_NonAdminOwnServerSucceeds(t *testing.T) {
	fs := &apiKeysAuthFakeStore{servers: map[string]*models.Server{
		"srv-1": {ID: 1, UUID: "srv-1", OwnerID: "u1"},
	}}
	h := newAPIKeysAuthHandler(fs)
	rec := httptest.NewRecorder()

	h.Create(rec, createAPIKeyReq("u1", "alice", false, map[string]interface{}{
		"name": "k", "permissions": []string{"rcon.exec"}, "servers": []string{"srv-1"},
	}))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if len(fs.createCalls) != 1 {
		t.Fatalf("expected 1 CreateAPIKey call, got %d", len(fs.createCalls))
	}
}

// TestAPIKeysCreate_PanelCapRejected pins that a PANEL-scope capability can
// never be minted onto a key, even for an admin caller: PANEL caps belong to
// staff principals, not owner-scoped automation credentials.
func TestAPIKeysCreate_PanelCapRejected(t *testing.T) {
	fs := &apiKeysAuthFakeStore{}
	h := newAPIKeysAuthHandler(fs)
	rec := httptest.NewRecorder()

	h.Create(rec, createAPIKeyReq("u1", "alice", false, map[string]interface{}{
		"name": "k", "permissions": []string{"users.read"},
	}))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Unknown permission: users.read") {
		t.Fatalf("body = %s, want 'Unknown permission: users.read'", rec.Body.String())
	}
	if len(fs.createCalls) != 0 {
		t.Fatalf("expected no CreateAPIKey call, got %+v", fs.createCalls)
	}
}

// TestAPIKeysCreate_NonAdminOwnServerBroaderCap proves the subset check
// generalizes beyond rcon.exec: the server owner holds every SERVER cap via
// the resolver's owner short-circuit, so a broader cap like files.delete is
// also allowed on their own server.
func TestAPIKeysCreate_NonAdminOwnServerBroaderCap(t *testing.T) {
	fs := &apiKeysAuthFakeStore{servers: map[string]*models.Server{
		"srv-1": {ID: 1, UUID: "srv-1", OwnerID: "u1"},
	}}
	h := newAPIKeysAuthHandler(fs)
	rec := httptest.NewRecorder()

	h.Create(rec, createAPIKeyReq("u1", "alice", false, map[string]interface{}{
		"name": "k", "permissions": []string{"files.delete"}, "servers": []string{"srv-1"},
	}))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if len(fs.createCalls) != 1 {
		t.Fatalf("expected 1 CreateAPIKey call, got %d", len(fs.createCalls))
	}
}

// TestAPIKeysCreate_FriendPartialGrantSubset is the crown-jewel per-cap
// subset test: a friend holding an explicit grant of ONLY console.read on
// someone else's server may mint a key carrying console.read, but MUST NOT be
// able to also mint files.delete on that key even though it targets the same
// server - the anti-escalation boundary is per capability, not per server.
func TestAPIKeysCreate_FriendPartialGrantSubset(t *testing.T) {
	newFakeStoreWithGrant := func() *apiKeysAuthFakeStore {
		return &apiKeysAuthFakeStore{
			servers: map[string]*models.Server{
				"srv-1": {ID: 1, UUID: "srv-1", OwnerID: "owner-else"},
			},
			grants: map[string]*store.ServerGrant{
				grantKey(1, "u1"): {CapOverrides: store.CapOverrides{Grant: []string{"console.read"}}},
			},
		}
	}

	t.Run("holds console.read: succeeds", func(t *testing.T) {
		fs := newFakeStoreWithGrant()
		h := newAPIKeysAuthHandler(fs)
		rec := httptest.NewRecorder()

		h.Create(rec, createAPIKeyReq("u1", "friend", false, map[string]interface{}{
			"name": "k", "permissions": []string{"console.read"}, "servers": []string{"srv-1"},
		}))

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
		}
		if len(fs.createCalls) != 1 {
			t.Fatalf("expected 1 CreateAPIKey call, got %d", len(fs.createCalls))
		}
	})

	t.Run("does not hold files.delete: rejected", func(t *testing.T) {
		fs := newFakeStoreWithGrant()
		h := newAPIKeysAuthHandler(fs)
		rec := httptest.NewRecorder()

		h.Create(rec, createAPIKeyReq("u1", "friend", false, map[string]interface{}{
			"name": "k", "permissions": []string{"console.read", "files.delete"}, "servers": []string{"srv-1"},
		}))

		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403: %s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "No files.delete access to server: srv-1") {
			t.Fatalf("body = %s, want 'No files.delete access to server: srv-1'", rec.Body.String())
		}
		if len(fs.createCalls) != 0 {
			t.Fatalf("expected no CreateAPIKey call, got %+v", fs.createCalls)
		}
	})
}

// TestAPIKeysCreate_AdminBypassesScopingAndDefaultsRate pins that an admin
// skips the per-server ownership loop entirely (the server here is not even
// in the fake store, so a non-admin call would 400) and that an
// omitted/non-positive ratePerMin defaults to 60.
func TestAPIKeysCreate_AdminBypassesScopingAndDefaultsRate(t *testing.T) {
	fs := &apiKeysAuthFakeStore{servers: map[string]*models.Server{}}
	h := newAPIKeysAuthHandler(fs)
	rec := httptest.NewRecorder()

	h.Create(rec, createAPIKeyReq("admin-1", "root", true, map[string]interface{}{
		"name": "broad-key", "permissions": []string{"rcon.exec"}, "servers": []string{"not-in-store"},
	}))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if len(fs.createCalls) != 1 {
		t.Fatalf("expected 1 CreateAPIKey call, got %d", len(fs.createCalls))
	}
	call := fs.createCalls[0]
	if call.UserID != "admin-1" || call.Name != "broad-key" || call.RatePerMin != 60 {
		t.Fatalf("call = %+v, want UserID=admin-1 Name=broad-key RatePerMin=60", call)
	}
	if len(call.Scope.Servers) != 1 || call.Scope.Servers[0] != "not-in-store" {
		t.Fatalf("call.Scope.Servers = %+v, want [not-in-store]", call.Scope.Servers)
	}

	var resp createAPIKeyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Success || !strings.HasPrefix(resp.Plaintext, "dyl_") {
		t.Fatalf("response = %+v, want Success=true and a dyl_-prefixed Plaintext", resp)
	}
	// KeyHash carries `json:"-"` (never serialized - the response only ever
	// carries the one-time plaintext), so pin the hash relationship against
	// what was actually persisted via CreateAPIKey, not the JSON response:
	// the stored KeyHash must be exactly HashAPIKey of the plaintext with the
	// dyl_ prefix stripped, since that is what APIKeyMiddleware re-derives
	// from the caller's Authorization header on every subsequent use.
	rawPlaintext := strings.TrimPrefix(resp.Plaintext, "dyl_")
	if call.KeyHash != HashAPIKey(rawPlaintext) {
		t.Fatalf("persisted KeyHash %q does not match HashAPIKey(rawPlaintext) %q", call.KeyHash, HashAPIKey(rawPlaintext))
	}
}

func TestAPIKeysCreate_ExplicitRatePerMinPersisted(t *testing.T) {
	fs := &apiKeysAuthFakeStore{}
	h := newAPIKeysAuthHandler(fs)
	rec := httptest.NewRecorder()

	h.Create(rec, createAPIKeyReq("admin-1", "root", true, map[string]interface{}{
		"name": "k", "permissions": []string{"rcon.exec"}, "ratePerMin": 10,
	}))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if len(fs.createCalls) != 1 || fs.createCalls[0].RatePerMin != 10 {
		t.Fatalf("createCalls = %+v, want one call with RatePerMin=10", fs.createCalls)
	}
}

func TestAPIKeysCreate_StoreErrorIsInternalServerError(t *testing.T) {
	fs := &apiKeysAuthFakeStore{createErr: errors.New("db down")}
	h := newAPIKeysAuthHandler(fs)
	rec := httptest.NewRecorder()

	h.Create(rec, createAPIKeyReq("admin-1", "root", true, map[string]interface{}{
		"name": "k", "permissions": []string{"rcon.exec"},
	}))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: %s", rec.Code, rec.Body.String())
	}
}
