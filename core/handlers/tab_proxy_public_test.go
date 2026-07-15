package handlers

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	nodegrpc "dylaris-core/grpc"
	"dylaris-core/models"
	"dylaris-core/services"
	"dylaris-core/store"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gorilla/mux"
)

// --- pure helpers (kept from the Task 9 brief verbatim) ---

func TestDecideProxyAccess(t *testing.T) {
	cases := []struct {
		name        string
		visibility  string
		allowPublic bool
		authed      bool
		hasAccess   bool
		wantAllow   bool
		wantStatus  int
	}{
		{"public allowed serves anon", "public", true, false, false, true, http.StatusOK},
		{"public disabled hides", "public", false, false, false, false, http.StatusNotFound},
		{"private anon -> 401", "private", false, false, false, false, http.StatusUnauthorized},
		{"private authed no access -> 403", "private", false, true, false, false, http.StatusForbidden},
		{"private authed with access", "private", false, true, true, true, http.StatusOK},
		{"public allowed ignores auth", "public", true, true, true, true, http.StatusOK},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			allow, status := decideProxyAccess(c.visibility, c.allowPublic, c.authed, c.hasAccess)
			if allow != c.wantAllow || status != c.wantStatus {
				t.Errorf("decideProxyAccess(%q,%v,%v,%v) = (%v,%d), want (%v,%d)",
					c.visibility, c.allowPublic, c.authed, c.hasAccess, allow, status, c.wantAllow, c.wantStatus)
			}
		})
	}
}

func TestShareTokenExpired(t *testing.T) {
	now := time.Unix(1000, 0)
	cases := []struct {
		name    string
		expires sql.NullTime
		want    bool
	}{
		{"null never expires", sql.NullTime{Valid: false}, false},
		{"future not expired", sql.NullTime{Valid: true, Time: now.Add(time.Hour)}, false},
		{"past expired", sql.NullTime{Valid: true, Time: now.Add(-time.Hour)}, true},
		{"exactly now expired", sql.NullTime{Valid: true, Time: now}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := shareTokenExpired(c.expires, now); got != c.want {
				t.Errorf("shareTokenExpired = %v, want %v", got, c.want)
			}
		})
	}
}

// --- resolvePublicTicket: cookie validation against an already-resolved tab,
// isolatable from the DB since the tab is constructed directly. ---

func TestResolvePublicTicket(t *testing.T) {
	_, ah := newTabProxyTestHandler(t, true, "")
	h := &ProxyHandler{state: &AppState{}, auth: ah}
	tab := &proxyTab{ID: 2, ServerID: 1}

	newReq := func(cookie *http.Cookie) *http.Request {
		r := httptest.NewRequest("GET", "/api/tabproxy/tok", nil)
		if cookie != nil {
			r.AddCookie(cookie)
		}
		return r
	}

	t.Run("no cookie", func(t *testing.T) {
		authed, hasAccess, readOnly := h.resolvePublicTicket(newReq(nil), tab)
		if authed || hasAccess || readOnly {
			t.Errorf("got (%v,%v,%v), want (false,false,false)", authed, hasAccess, readOnly)
		}
	})

	t.Run("invalid cookie value", func(t *testing.T) {
		cookie := &http.Cookie{Name: proxyCookieName, Value: "not-a-jwt"}
		authed, hasAccess, readOnly := h.resolvePublicTicket(newReq(cookie), tab)
		if authed || hasAccess || readOnly {
			t.Errorf("got (%v,%v,%v), want (false,false,false)", authed, hasAccess, readOnly)
		}
	})

	t.Run("wrong purpose (session token)", func(t *testing.T) {
		sessionTok, err := ah.IssueToken("owner", false)
		if err != nil {
			t.Fatalf("IssueToken: %v", err)
		}
		cookie := &http.Cookie{Name: proxyCookieName, Value: sessionTok}
		authed, hasAccess, readOnly := h.resolvePublicTicket(newReq(cookie), tab)
		if authed || hasAccess || readOnly {
			t.Errorf("got (%v,%v,%v), want (false,false,false)", authed, hasAccess, readOnly)
		}
	})

	t.Run("valid ticket scoped to this exact tab", func(t *testing.T) {
		ticket, err := ah.IssueTabProxyTicket("owner", false, 1, 2, false)
		if err != nil {
			t.Fatalf("IssueTabProxyTicket: %v", err)
		}
		cookie := &http.Cookie{Name: proxyCookieName, Value: ticket}
		authed, hasAccess, readOnly := h.resolvePublicTicket(newReq(cookie), tab)
		if !authed || !hasAccess || readOnly {
			t.Errorf("got (%v,%v,%v), want (true,true,false)", authed, hasAccess, readOnly)
		}
	})

	t.Run("valid read-only ticket scoped to this exact tab", func(t *testing.T) {
		ticket, err := ah.IssueTabProxyTicket("owner", false, 1, 2, true)
		if err != nil {
			t.Fatalf("IssueTabProxyTicket: %v", err)
		}
		cookie := &http.Cookie{Name: proxyCookieName, Value: ticket}
		authed, hasAccess, readOnly := h.resolvePublicTicket(newReq(cookie), tab)
		if !authed || !hasAccess || !readOnly {
			t.Errorf("got (%v,%v,%v), want (true,true,true)", authed, hasAccess, readOnly)
		}
	})

	t.Run("valid ticket, wrong server", func(t *testing.T) {
		ticket, err := ah.IssueTabProxyTicket("owner", false, 5, 2, false)
		if err != nil {
			t.Fatalf("IssueTabProxyTicket: %v", err)
		}
		cookie := &http.Cookie{Name: proxyCookieName, Value: ticket}
		authed, hasAccess, readOnly := h.resolvePublicTicket(newReq(cookie), tab)
		if !authed || hasAccess || readOnly {
			t.Errorf("got (%v,%v,%v), want (true,false,false) - valid ticket for the wrong scope", authed, hasAccess, readOnly)
		}
	})

	t.Run("valid ticket, wrong tab", func(t *testing.T) {
		ticket, err := ah.IssueTabProxyTicket("owner", false, 1, 9, false)
		if err != nil {
			t.Fatalf("IssueTabProxyTicket: %v", err)
		}
		cookie := &http.Cookie{Name: proxyCookieName, Value: ticket}
		authed, hasAccess, readOnly := h.resolvePublicTicket(newReq(cookie), tab)
		if !authed || hasAccess || readOnly {
			t.Errorf("got (%v,%v,%v), want (true,false,false) - valid ticket for the wrong scope", authed, hasAccess, readOnly)
		}
	})
}

// --- Public + MintPublicProxyAuth: endpoint-level, DB-backed via sqlmock ---

// tabProxyPublicFakeStore adds a sqlmock-backed RawDB() (so getTabByShareToken
// runs a real, if scripted, query) on top of the same server/settings/access
// fakes tab_proxy_test.go already uses for the in-dashboard path.
type tabProxyPublicFakeStore struct {
	store.Store
	db       *sql.DB
	servers  map[int]*models.Server
	settings map[string]string
}

func (f *tabProxyPublicFakeStore) RawDB() *sql.DB { return f.db }

func (f *tabProxyPublicFakeStore) GetServerByID(id int) (*models.Server, error) {
	if s, ok := f.servers[id]; ok {
		return s, nil
	}
	return nil, sql.ErrNoRows
}

func (f *tabProxyPublicFakeStore) GetInvite(serverID int, userID string) (*models.ServerInvite, error) {
	return nil, sql.ErrNoRows
}

func (f *tabProxyPublicFakeStore) GetSetting(key string) (string, error) {
	return f.settings[key], nil
}

// shareTokenRow is the fixed column shape getTabByShareToken/getTabByID scan.
type shareTokenRow struct {
	id, serverID, nodeID, targetPort int
	serverUUID, mode, targetPath     string
	surface, visibility              string
	shareExpires                     sql.NullTime
	enabled                          bool
}

func newTabProxyPublicTestHandler(t *testing.T, tabProxyEnabled, allowPublic bool) (*ProxyHandler, *AuthHandler, sqlmock.Sqlmock, *tabProxyPublicFakeStore) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	fs := &tabProxyPublicFakeStore{
		db: db,
		servers: map[int]*models.Server{
			1: {ID: 1, UUID: "srv-1-uuid", OwnerName: "owner", OwnerID: "owner-id", NodeID: 7},
		},
		settings: map[string]string{},
	}
	if tabProxyEnabled {
		fs.settings["feature_tab_proxy_enabled"] = "true"
	}
	if allowPublic {
		fs.settings["tab_proxy_allow_public_links"] = "true"
	}
	ff := services.NewFeatureFlags(fs)
	// A real (but connection-less) Registry rather than a nil one: serveHTTP
	// calls GRPCRegistry.SendRequestStreaming directly with no nil-guard (the
	// guard lives in getTabByShareToken/getTabByID's callers instead), so a
	// nil *nodegrpc.Registry would panic on the mutex inside GetConnection.
	// An empty Registry instead surfaces the same "node not connected" ->
	// 502 a real deployment would give for a node that dropped its gRPC
	// connection, which is exactly the "past the access gate" signal these
	// tests use.
	// B5: the standalone-share tests exercise the happy path where origin
	// isolation is active; the refusal tests below flip this to false.
	state := &AppState{Store: fs, FeatureFlags: ff, GRPCRegistry: nodegrpc.NewRegistry(), TabProxyIsolationActive: true}
	authHandler := NewAuthHandler(state, tabProxyTestSecret)
	return NewProxyHandler(state, authHandler), authHandler, mock, fs
}

// expectShareTokenQuery scripts the getTabByShareToken/getTabByShareToken row
// sqlmock will hand back for the next SELECT against server_tabs.
func expectShareTokenQuery(mock sqlmock.Sqlmock, row shareTokenRow) {
	rows := sqlmock.NewRows([]string{"id", "server_id", "uuid", "node_id", "mode",
		"target_port", "target_path", "surface", "visibility", "share_expires_at", "enabled"}).
		AddRow(row.id, row.serverID, row.serverUUID, row.nodeID, row.mode,
			row.targetPort, row.targetPath, row.surface, row.visibility, row.shareExpires, row.enabled)
	mock.ExpectQuery(`FROM server_tabs t JOIN servers s`).WillReturnRows(rows)
}

func publicRequest(token, rest string, cookie *http.Cookie) *http.Request {
	return publicRequestMethod("GET", token, rest, cookie)
}

func publicRequestMethod(method, token, rest string, cookie *http.Cookie) *http.Request {
	url := "/api/tabproxy/" + token
	if rest != "" {
		url += "/" + rest
	}
	r := httptest.NewRequest(method, url, nil)
	vars := map[string]string{"token": token}
	if rest != "" {
		vars["rest"] = rest
	}
	r = mux.SetURLVars(r, vars)
	if cookie != nil {
		r.AddCookie(cookie)
	}
	return r
}

func TestPublic_FeatureDisabled(t *testing.T) {
	h, _, _, _ := newTabProxyPublicTestHandler(t, false, true)
	rec := httptest.NewRecorder()

	h.Public(rec, publicRequest("tok", "", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d (feature disabled)", rec.Code, http.StatusNotFound)
	}
}

func TestPublic_UnknownToken(t *testing.T) {
	h, _, mock, _ := newTabProxyPublicTestHandler(t, true, true)
	mock.ExpectQuery(`FROM server_tabs t JOIN servers s`).WillReturnError(sql.ErrNoRows)
	rec := httptest.NewRecorder()

	h.Public(rec, publicRequest("nope", "", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d (unknown token)", rec.Code, http.StatusNotFound)
	}
}

func TestPublic_PublicVisibility_ServesAnonWhenAllowed(t *testing.T) {
	h, _, mock, _ := newTabProxyPublicTestHandler(t, true, true)
	expectShareTokenQuery(mock, shareTokenRow{
		id: 2, serverID: 1, serverUUID: "srv-1-uuid", nodeID: 7, mode: "proxied",
		targetPort: 8080, targetPath: "/", surface: "page", visibility: "public", enabled: true,
	})
	rec := httptest.NewRecorder()

	h.Public(rec, publicRequest("tok", "", nil))

	// No gRPC registry wired in this fake AppState, so serve() will fail past
	// the access gate - a 502 (not 404/401/403) is the signal that the
	// visibility gate let the anonymous request all the way through.
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d (past the visibility gate, anon): %s", rec.Code, http.StatusBadGateway, rec.Body.String())
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
}

func TestPublic_PublicVisibility_DisabledFlagHides(t *testing.T) {
	h, _, mock, _ := newTabProxyPublicTestHandler(t, true, false)
	expectShareTokenQuery(mock, shareTokenRow{
		id: 2, serverID: 1, serverUUID: "srv-1-uuid", nodeID: 7, mode: "proxied",
		targetPort: 8080, targetPath: "/", surface: "page", visibility: "public", enabled: true,
	})
	rec := httptest.NewRecorder()

	h.Public(rec, publicRequest("tok", "", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d (TabProxyAllowPublicLinks off hides a public link)", rec.Code, http.StatusNotFound)
	}
}

func TestPublic_PrivateVisibility_NoCookie401(t *testing.T) {
	h, _, mock, _ := newTabProxyPublicTestHandler(t, true, true)
	expectShareTokenQuery(mock, shareTokenRow{
		id: 2, serverID: 1, serverUUID: "srv-1-uuid", nodeID: 7, mode: "proxied",
		targetPort: 8080, targetPath: "/", surface: "page", visibility: "private", enabled: true,
	})
	rec := httptest.NewRecorder()

	h.Public(rec, publicRequest("tok", "", nil))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d (private, no cookie)", rec.Code, http.StatusUnauthorized)
	}
}

func TestPublic_PrivateVisibility_InvalidCookie401(t *testing.T) {
	h, _, mock, _ := newTabProxyPublicTestHandler(t, true, true)
	expectShareTokenQuery(mock, shareTokenRow{
		id: 2, serverID: 1, serverUUID: "srv-1-uuid", nodeID: 7, mode: "proxied",
		targetPort: 8080, targetPath: "/", surface: "page", visibility: "private", enabled: true,
	})
	cookie := &http.Cookie{Name: proxyCookieName, Value: "garbage"}
	rec := httptest.NewRecorder()

	h.Public(rec, publicRequest("tok", "", cookie))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d (private, invalid cookie)", rec.Code, http.StatusUnauthorized)
	}
}

func TestPublic_PrivateVisibility_WrongScopeCookie403(t *testing.T) {
	h, ah, mock, _ := newTabProxyPublicTestHandler(t, true, true)
	expectShareTokenQuery(mock, shareTokenRow{
		id: 2, serverID: 1, serverUUID: "srv-1-uuid", nodeID: 7, mode: "proxied",
		targetPort: 8080, targetPath: "/", surface: "page", visibility: "private", enabled: true,
	})
	// Valid ticket, but minted for a different tab - must not authorize this one.
	ticket, err := ah.IssueTabProxyTicket("owner", false, 1, 999, false)
	if err != nil {
		t.Fatalf("IssueTabProxyTicket: %v", err)
	}
	cookie := &http.Cookie{Name: proxyCookieName, Value: ticket}
	rec := httptest.NewRecorder()

	h.Public(rec, publicRequest("tok", "", cookie))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d (valid ticket, wrong tab)", rec.Code, http.StatusForbidden)
	}
}

func TestPublic_PrivateVisibility_ValidScopedCookieAllowed(t *testing.T) {
	h, ah, mock, _ := newTabProxyPublicTestHandler(t, true, true)
	expectShareTokenQuery(mock, shareTokenRow{
		id: 2, serverID: 1, serverUUID: "srv-1-uuid", nodeID: 7, mode: "proxied",
		targetPort: 8080, targetPath: "/", surface: "page", visibility: "private", enabled: true,
	})
	ticket, err := ah.IssueTabProxyTicket("owner", false, 1, 2, false)
	if err != nil {
		t.Fatalf("IssueTabProxyTicket: %v", err)
	}
	cookie := &http.Cookie{Name: proxyCookieName, Value: ticket}
	rec := httptest.NewRecorder()

	h.Public(rec, publicRequest("tok", "", cookie))

	// Same reasoning as the anon-public test: no gRPC registry wired, so a
	// 502 past the gate is the signal the ticket authorized the request.
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d (valid scoped ticket should pass the gate): %s", rec.Code, http.StatusBadGateway, rec.Body.String())
	}
}

// TestPublic_PrivateVisibility_ReadOnlyTicket_RejectsNonGET covers the WS5
// Task 9 fast-follow gap: a read-only (demo) ticket minted for a PRIVATE
// share link must not be able to issue a write method through the
// standalone proxy, mirroring InDashboard's own read-only method gate.
func TestPublic_PrivateVisibility_ReadOnlyTicket_RejectsNonGET(t *testing.T) {
	h, ah, mock, _ := newTabProxyPublicTestHandler(t, true, true)
	ticket, err := ah.IssueTabProxyTicket("demo", false, 1, 2, true)
	if err != nil {
		t.Fatalf("IssueTabProxyTicket: %v", err)
	}
	cookie := &http.Cookie{Name: proxyCookieName, Value: ticket}

	for _, method := range []string{"POST", "PUT", "DELETE", "PATCH"} {
		t.Run(method, func(t *testing.T) {
			expectShareTokenQuery(mock, shareTokenRow{
				id: 2, serverID: 1, serverUUID: "srv-1-uuid", nodeID: 7, mode: "proxied",
				targetPort: 8080, targetPath: "/", surface: "page", visibility: "private", enabled: true,
			})
			rec := httptest.NewRecorder()

			h.Public(rec, publicRequestMethod(method, "tok", "", cookie))

			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want %d (%s on read-only ticket)", rec.Code, http.StatusForbidden, method)
			}
		})
	}
}

// TestPublic_PrivateVisibility_ReadOnlyTicket_AllowsGETPastGate is the
// counterpart: the same read-only ticket must still be allowed through for
// GET/HEAD, reaching the serve() path (signaled by the 502 the fake
// AppState's connection-less gRPC registry produces past the access gate).
func TestPublic_PrivateVisibility_ReadOnlyTicket_AllowsGETPastGate(t *testing.T) {
	h, ah, mock, _ := newTabProxyPublicTestHandler(t, true, true)
	ticket, err := ah.IssueTabProxyTicket("demo", false, 1, 2, true)
	if err != nil {
		t.Fatalf("IssueTabProxyTicket: %v", err)
	}
	cookie := &http.Cookie{Name: proxyCookieName, Value: ticket}

	for _, method := range []string{"GET", "HEAD"} {
		t.Run(method, func(t *testing.T) {
			expectShareTokenQuery(mock, shareTokenRow{
				id: 2, serverID: 1, serverUUID: "srv-1-uuid", nodeID: 7, mode: "proxied",
				targetPort: 8080, targetPath: "/", surface: "page", visibility: "private", enabled: true,
			})
			rec := httptest.NewRecorder()

			h.Public(rec, publicRequestMethod(method, "tok", "", cookie))

			if rec.Code != http.StatusBadGateway {
				t.Fatalf("status = %d, want %d (%s on read-only ticket should pass the gate): %s", rec.Code, http.StatusBadGateway, method, rec.Body.String())
			}
		})
	}
}

func TestPublic_ExpiredLink410(t *testing.T) {
	h, _, mock, _ := newTabProxyPublicTestHandler(t, true, true)
	expectShareTokenQuery(mock, shareTokenRow{
		id: 2, serverID: 1, serverUUID: "srv-1-uuid", nodeID: 7, mode: "proxied",
		targetPort: 8080, targetPath: "/", surface: "page", visibility: "public", enabled: true,
		shareExpires: sql.NullTime{Valid: true, Time: time.Now().Add(-time.Hour)},
	})
	rec := httptest.NewRecorder()

	h.Public(rec, publicRequest("tok", "", nil))

	if rec.Code != http.StatusGone {
		t.Fatalf("status = %d, want %d (expired link)", rec.Code, http.StatusGone)
	}
}

func TestPublic_DisabledTab404(t *testing.T) {
	h, _, mock, _ := newTabProxyPublicTestHandler(t, true, true)
	expectShareTokenQuery(mock, shareTokenRow{
		id: 2, serverID: 1, serverUUID: "srv-1-uuid", nodeID: 7, mode: "proxied",
		targetPort: 8080, targetPath: "/", surface: "page", visibility: "public", enabled: false,
	})
	rec := httptest.NewRecorder()

	h.Public(rec, publicRequest("tok", "", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d (tab disabled)", rec.Code, http.StatusNotFound)
	}
}

func TestPublic_DirectModeTab404(t *testing.T) {
	h, _, mock, _ := newTabProxyPublicTestHandler(t, true, true)
	expectShareTokenQuery(mock, shareTokenRow{
		id: 2, serverID: 1, serverUUID: "srv-1-uuid", nodeID: 7, mode: "direct",
		targetPort: 8080, targetPath: "/", surface: "page", visibility: "public", enabled: true,
	})
	rec := httptest.NewRecorder()

	h.Public(rec, publicRequest("tok", "", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d (mode != proxied)", rec.Code, http.StatusNotFound)
	}
}

func TestPublic_TabSurfaceRejected(t *testing.T) {
	h, _, mock, _ := newTabProxyPublicTestHandler(t, true, true)
	expectShareTokenQuery(mock, shareTokenRow{
		id: 2, serverID: 1, serverUUID: "srv-1-uuid", nodeID: 7, mode: "proxied",
		targetPort: 8080, targetPath: "/", surface: "tab", visibility: "public", enabled: true,
	})
	rec := httptest.NewRecorder()

	h.Public(rec, publicRequest("tok", "", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d (surface=tab is dashboard-only, not the standalone page)", rec.Code, http.StatusNotFound)
	}
}

// --- MintPublicProxyAuth ---

func TestMintPublicProxyAuth_FeatureDisabled(t *testing.T) {
	h, _, _, _ := newTabProxyPublicTestHandler(t, false, true)
	req := withIdentity(httptest.NewRequest("GET", "/api/tabproxy/tok/auth", nil), "owner", false, "owner-id")
	req = mux.SetURLVars(req, map[string]string{"token": "tok"})
	rec := httptest.NewRecorder()

	h.MintPublicProxyAuth(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d (feature disabled)", rec.Code, http.StatusForbidden)
	}
	if got := rec.Result().Cookies(); len(got) != 0 {
		t.Fatalf("expected no cookie when feature disabled, got %+v", got)
	}
}

func TestMintPublicProxyAuth_UnknownToken404(t *testing.T) {
	h, _, mock, _ := newTabProxyPublicTestHandler(t, true, true)
	mock.ExpectQuery(`FROM server_tabs t JOIN servers s`).WillReturnError(sql.ErrNoRows)
	req := withIdentity(httptest.NewRequest("GET", "/api/tabproxy/tok/auth", nil), "owner", false, "owner-id")
	req = mux.SetURLVars(req, map[string]string{"token": "tok"})
	rec := httptest.NewRecorder()

	h.MintPublicProxyAuth(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d (unknown token)", rec.Code, http.StatusNotFound)
	}
}

func TestMintPublicProxyAuth_PublicVisibilityRejected(t *testing.T) {
	h, _, mock, _ := newTabProxyPublicTestHandler(t, true, true)
	expectShareTokenQuery(mock, shareTokenRow{
		id: 2, serverID: 1, serverUUID: "srv-1-uuid", nodeID: 7, mode: "proxied",
		targetPort: 8080, targetPath: "/", surface: "page", visibility: "public", enabled: true,
	})
	req := withIdentity(httptest.NewRequest("GET", "/api/tabproxy/tok/auth", nil), "owner", false, "owner-id")
	req = mux.SetURLVars(req, map[string]string{"token": "tok"})
	rec := httptest.NewRecorder()

	h.MintPublicProxyAuth(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d (public link doesn't need a ticket)", rec.Code, http.StatusBadRequest)
	}
}

func TestMintPublicProxyAuth_Forbidden_NoAccess(t *testing.T) {
	h, _, mock, _ := newTabProxyPublicTestHandler(t, true, true)
	expectShareTokenQuery(mock, shareTokenRow{
		id: 2, serverID: 1, serverUUID: "srv-1-uuid", nodeID: 7, mode: "proxied",
		targetPort: 8080, targetPath: "/", surface: "page", visibility: "private", enabled: true,
	})
	req := withIdentity(httptest.NewRequest("GET", "/api/tabproxy/tok/auth", nil), "stranger", false, "stranger-id")
	req = mux.SetURLVars(req, map[string]string{"token": "tok"})
	rec := httptest.NewRecorder()

	h.MintPublicProxyAuth(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d (no server access)", rec.Code, http.StatusForbidden)
	}
}

func TestMintPublicProxyAuth_Success_SetsScopedCookieAndTicketClaims(t *testing.T) {
	h, _, mock, _ := newTabProxyPublicTestHandler(t, true, true)
	expectShareTokenQuery(mock, shareTokenRow{
		id: 2, serverID: 1, serverUUID: "srv-1-uuid", nodeID: 7, mode: "proxied",
		targetPort: 8080, targetPath: "/", surface: "page", visibility: "private", enabled: true,
	})
	req := withIdentity(httptest.NewRequest("GET", "/api/tabproxy/tok/auth", nil), "owner", false, "owner-id")
	req = mux.SetURLVars(req, map[string]string{"token": "tok"})
	rec := httptest.NewRecorder()

	h.MintPublicProxyAuth(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusNoContent, rec.Body.String())
	}
	if rp := rec.Header().Get("Referrer-Policy"); rp != "no-referrer" {
		t.Errorf("Referrer-Policy = %q, want no-referrer", rp)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}

	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected exactly 1 cookie, got %d: %+v", len(cookies), cookies)
	}
	c := cookies[0]
	if c.Name != proxyCookieName {
		t.Errorf("cookie name = %q, want %q", c.Name, proxyCookieName)
	}
	if !c.HttpOnly {
		t.Error("cookie not HttpOnly")
	}
	if c.SameSite != http.SameSiteStrictMode {
		t.Errorf("cookie SameSite = %v, want Strict", c.SameSite)
	}
	wantPath := "/api/tabproxy/tok/"
	if c.Path != wantPath {
		t.Errorf("cookie Path = %q, want %q", c.Path, wantPath)
	}
	if c.MaxAge != 300 {
		t.Errorf("cookie MaxAge = %d, want 300 (5 minutes)", c.MaxAge)
	}

	claims, err := h.auth.ParseTabProxyTicket(c.Value)
	if err != nil {
		t.Fatalf("minted ticket did not parse: %v", err)
	}
	if claims.ServerID != 1 || claims.TabID != 2 {
		t.Errorf("claims server/tab = %d/%d, want 1/2", claims.ServerID, claims.TabID)
	}
	if claims.Username != "owner" {
		t.Errorf("claims username = %q, want owner", claims.Username)
	}
}

// TestPublic_RefusedWhenIsolationInactive covers the B5 C1 close: with origin
// isolation inactive, the standalone share data plane refuses BEFORE any DB
// lookup, so a same-origin public share can never be served.
func TestPublic_RefusedWhenIsolationInactive(t *testing.T) {
	h, _, _, _ := newTabProxyPublicTestHandler(t, true, true)
	h.state.TabProxyIsolationActive = false
	rec := httptest.NewRecorder()

	// No sqlmock query is scripted: the gate returns before getTabByShareToken.
	h.Public(rec, publicRequest("tok", "", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d (isolation inactive refuses the share data plane)", rec.Code, http.StatusNotFound)
	}
}

// TestMintPublicProxyAuth_RefusedWhenIsolationInactive is the mint-side twin:
// no ticket cookie is issued when isolation is inactive.
func TestMintPublicProxyAuth_RefusedWhenIsolationInactive(t *testing.T) {
	h, _, _, _ := newTabProxyPublicTestHandler(t, true, true)
	h.state.TabProxyIsolationActive = false
	req := withIdentity(httptest.NewRequest("GET", "/api/tabproxy/tok/auth", nil), "owner", false, "owner-id")
	req = mux.SetURLVars(req, map[string]string{"token": "tok"})
	rec := httptest.NewRecorder()

	h.MintPublicProxyAuth(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d (isolation inactive refuses mint)", rec.Code, http.StatusForbidden)
	}
	if got := rec.Result().Cookies(); len(got) != 0 {
		t.Fatalf("expected no cookie when isolation inactive, got %+v", got)
	}
}
