package handlers

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	pb "dylaris-proto/node"

	"dylaris-core/models"
	"dylaris-core/services"
	"dylaris-core/store"

	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/mux"
)

func TestInjectBaseHref(t *testing.T) {
	cases := []struct {
		name         string
		in           string
		base         string
		wantContains string
	}{
		{"after head", "<html><head><title>x</title></head></html>", "/p/", `<head><base href="/p/">`},
		{"head with attrs", `<head lang="en"><title>x</title>`, "/p/", `<head lang="en"><base href="/p/">`},
		{"no head prepends", "<html><body>x</body></html>", "/p/", `<base href="/p/"><html>`},
		{"uppercase HEAD", "<HTML><HEAD></HEAD>", "/p/", `<base href="/p/">`},
	}
	for _, c := range cases {
		got := string(injectBaseHref([]byte(c.in), c.base))
		if !strings.Contains(got, c.wantContains) {
			t.Errorf("%s: injectBaseHref = %q, want contains %q", c.name, got, c.wantContains)
		}
	}
}

func TestCoreStripHopByHop(t *testing.T) {
	in := []*pb.HttpHeader{
		{Key: "Content-Type", Value: "text/html"},
		{Key: "Connection", Value: "keep-alive"},
		{Key: "Transfer-Encoding", Value: "chunked"},
		{Key: "Upgrade", Value: "websocket"},
		{Key: "Set-Cookie", Value: "session=evil; Path=/"},
		{Key: "set-cookie2", Value: "session2=evil"},
		{Key: "Cache-Control", Value: "public, max-age=3600"},
		{Key: "Expires", Value: "Wed, 21 Oct 2099 07:28:00 GMT"},
		{Key: "Pragma", Value: "cache"},
		{Key: "X-Keep", Value: "yes"},
	}
	got := coreStripHopByHop(in)
	kept := map[string]bool{}
	for _, h := range got {
		kept[h.Key] = true
	}
	for _, drop := range []string{"Connection", "Transfer-Encoding", "Upgrade", "Set-Cookie", "set-cookie2", "Cache-Control", "Expires", "Pragma"} {
		if kept[drop] {
			t.Errorf("header %q not stripped", drop)
		}
	}
	for _, keep := range []string{"Content-Type", "X-Keep"} {
		if !kept[keep] {
			t.Errorf("header %q dropped", keep)
		}
	}
}

// TestWriteProxyHeaders_CacheControlStaysSingleNoStore covers the WS5/B5
// cache-correctness fix: serveHTTP/Public always stamp their own
// authoritative "Cache-Control: no-store" via Set BEFORE relaying the
// container's response headers via writeProxyHeaders (which uses Add). If a
// container-supplied Cache-Control (or Expires/Pragma) were not stripped
// first, the response would carry two Cache-Control values and a lenient
// shared cache on the panel origin could honor the container's permissive
// one for this per-user/per-ticket content.
func TestWriteProxyHeaders_CacheControlStaysSingleNoStore(t *testing.T) {
	rec := httptest.NewRecorder()
	// Mirrors the real call order in serveHTTP: no-store is Set first.
	rec.Header().Set("Cache-Control", "no-store")

	writeProxyHeaders(rec, []*pb.HttpHeader{
		{Key: "Content-Type", Value: "text/html"},
		{Key: "Cache-Control", Value: "public, max-age=3600"},
		{Key: "Expires", Value: "Wed, 21 Oct 2099 07:28:00 GMT"},
		{Key: "Pragma", Value: "cache"},
		{Key: "Set-Cookie", Value: "session=evil; Path=/"},
	}, false)

	if got := rec.Header().Values("Cache-Control"); len(got) != 1 || got[0] != "no-store" {
		t.Errorf("Cache-Control = %v, want exactly [\"no-store\"]", got)
	}
	if got := rec.Header().Get("Expires"); got != "" {
		t.Errorf("Expires = %q, want stripped", got)
	}
	if got := rec.Header().Get("Pragma"); got != "" {
		t.Errorf("Pragma = %q, want stripped", got)
	}
	if got := rec.Header().Values("Set-Cookie"); len(got) != 0 {
		t.Errorf("Set-Cookie = %v, want stripped", got)
	}
	if got := rec.Header().Get("Content-Type"); got != "text/html" {
		t.Errorf("Content-Type = %q, want text/html (unrelated header must pass through)", got)
	}
}

// TestWriteProxyHeaders_ForcesNosniffAndDropsSetCookie covers the WS5
// final-review hardening: writeProxyHeaders is the single chokepoint both
// InDashboard and Public relay a container's response headers through, so
// this exercises the http.ResponseWriter side directly (coreStripHopByHop
// above only covers the pure header-list filtering).
func TestWriteProxyHeaders_ForcesNosniffAndDropsSetCookie(t *testing.T) {
	rec := httptest.NewRecorder()
	writeProxyHeaders(rec, []*pb.HttpHeader{
		{Key: "Content-Type", Value: "text/html"},
		{Key: "Set-Cookie", Value: "session=evil; Path=/"},
		{Key: "X-Content-Type-Options", Value: "sniff-me-anyway"},
	}, false)

	if got := rec.Header().Values("Set-Cookie"); len(got) != 0 {
		t.Errorf("Set-Cookie = %v, want stripped", got)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := rec.Header().Get("Content-Type"); got != "text/html" {
		t.Errorf("Content-Type = %q, want text/html (unrelated header must pass through)", got)
	}
}

func TestIsWebSocketUpgrade(t *testing.T) {
	cases := []struct {
		name       string
		upgrade    string
		connection string
		want       bool
	}{
		{"ws upgrade", "websocket", "Upgrade", true},
		{"ws upgrade mixed case", "WebSocket", "keep-alive, Upgrade", true},
		{"no upgrade header", "", "", false},
		{"upgrade but not ws", "h2c", "Upgrade", false},
		{"ws but connection missing", "websocket", "", false},
	}
	for _, c := range cases {
		r, _ := http.NewRequest("GET", "/x", nil)
		if c.upgrade != "" {
			r.Header.Set("Upgrade", c.upgrade)
		}
		if c.connection != "" {
			r.Header.Set("Connection", c.connection)
		}
		if got := isWebSocketUpgrade(r); got != c.want {
			t.Errorf("%s: isWebSocketUpgrade = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestSingleJoin(t *testing.T) {
	cases := []struct {
		base, sub, want string
	}{
		{"/", "", "/"},
		{"/", "tiles/x.png", "/tiles/x.png"},
		{"/map", "tiles", "/map/tiles"},
		{"/map/", "/tiles", "/map/tiles"},
		{"/map", "/tiles", "/map/tiles"},
	}
	for _, c := range cases {
		if got := singleJoin(c.base, c.sub); got != c.want {
			t.Errorf("singleJoin(%q,%q) = %q, want %q", c.base, c.sub, got, c.want)
		}
	}
}

// TestSanitizeProxyPath covers security invariant #2 carried forward from the
// WS5 task reviews: the path Core forwards to the node in HttpProxyReq.Path
// MUST be origin-form starting with exactly one "/", never "//", so a value
// like "//evil.com/x" can't be mistaken for a scheme-relative URL by any
// consumer that re-parses it, and must never carry a raw CR/LF that could
// enable request-line/header injection when the node concatenates it into a
// raw URL string ("http://" + addr + path, see node/grpc_tabproxy.go).
func TestSanitizeProxyPath(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"already clean", "/a/b", "/a/b"},
		{"missing leading slash", "a/b", "/a/b"},
		{"empty", "", "/"},
		{"double leading slash", "//evil.com/x", "/evil.com/x"},
		{"triple leading slash", "///evil.com", "/evil.com"},
		{"scheme-relative attempt with query", "//evil.com/x?y=1", "/evil.com/x?y=1"},
		{"CR LF stripped", "/a\r\nEvil-Header: 1", "/aEvil-Header: 1"},
		{"embedded double slash mid path kept", "/a//b", "/a//b"},
		{"at-sign userinfo-style attempt", "/@evil.com/x", "/@evil.com/x"},
	}
	for _, c := range cases {
		if got := sanitizeProxyPath(c.in); got != c.want {
			t.Errorf("%s: sanitizeProxyPath(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

// --- Cookie-only auth (WS5 Task 8 fast-follow): MintProxyAuth + InDashboard ---

// tabProxyFakeStore embeds store.Store (nil) so it satisfies the full
// interface at compile time; only the methods MintProxyAuth/checkServerAccess
// touch are overridden. Any other call would panic - the tests never make one.
type tabProxyFakeStore struct {
	store.Store
	servers  map[int]*models.Server
	settings map[string]string
}

func (f *tabProxyFakeStore) GetServerByID(id int) (*models.Server, error) {
	if s, ok := f.servers[id]; ok {
		return s, nil
	}
	return nil, sql.ErrNoRows
}

// GetInvite: no invites modeled in these tests - a non-owner/non-admin caller
// is always denied, which is all the tab-proxy auth tests need.
func (f *tabProxyFakeStore) GetInvite(serverID int, userID string) (*models.ServerInvite, error) {
	return nil, sql.ErrNoRows
}

func (f *tabProxyFakeStore) GetSetting(key string) (string, error) {
	return f.settings[key], nil
}

const tabProxyTestSecret = "test-tab-proxy-secret"

// newTabProxyTestHandler builds a ProxyHandler wired to an in-memory fake
// store, with server id 1 owned by "owner". tabProxyEnabled controls the
// feature_tab_proxy_enabled setting; demoUserID, if non-empty, is stamped as
// the designated demo account (demo_account_uuid).
func newTabProxyTestHandler(t *testing.T, tabProxyEnabled bool, demoUserID string) (*ProxyHandler, *AuthHandler) {
	t.Helper()
	fs := &tabProxyFakeStore{
		servers: map[int]*models.Server{
			1: {ID: 1, UUID: "srv-1-uuid", OwnerName: "owner", OwnerID: "owner-id", NodeID: 7},
		},
		settings: map[string]string{},
	}
	if tabProxyEnabled {
		fs.settings["feature_tab_proxy_enabled"] = "true"
	}
	if demoUserID != "" {
		fs.settings["demo_account_uuid"] = demoUserID
	}
	ff := services.NewFeatureFlags(fs)
	// isDemoAccount (demo.go) short-circuits to false unless StoreEnabled - the
	// whole demo surface only exists on the hosted, store-linked build.
	state := &AppState{Store: fs, FeatureFlags: ff, StoreEnabled: demoUserID != ""}
	authHandler := NewAuthHandler(state, tabProxyTestSecret)
	return NewProxyHandler(state, authHandler), authHandler
}

func withIdentity(r *http.Request, username string, isAdmin bool, userID string) *http.Request {
	ctx := context.WithValue(r.Context(), "username", username)
	ctx = context.WithValue(ctx, "isAdmin", isAdmin)
	ctx = context.WithValue(ctx, "userID", userID)
	return r.WithContext(ctx)
}

func withVars(r *http.Request, serverID, tabID string) *http.Request {
	return mux.SetURLVars(r, map[string]string{"id": serverID, "tabId": tabID})
}

func TestMintProxyAuth_FeatureDisabled(t *testing.T) {
	h, _ := newTabProxyTestHandler(t, false, "")
	req := withVars(withIdentity(httptest.NewRequest("GET", "/api/servers/1/tabs/2/proxy-auth", nil), "owner", false, "owner-id"), "1", "2")
	rec := httptest.NewRecorder()

	h.MintProxyAuth(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d (feature disabled): %s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
	if got := rec.Result().Cookies(); len(got) != 0 {
		t.Fatalf("expected no cookie when feature disabled, got %+v", got)
	}
}

func TestMintProxyAuth_Forbidden_NoAccess(t *testing.T) {
	h, _ := newTabProxyTestHandler(t, true, "")
	req := withVars(withIdentity(httptest.NewRequest("GET", "/api/servers/1/tabs/2/proxy-auth", nil), "stranger", false, "stranger-id"), "1", "2")
	rec := httptest.NewRecorder()

	h.MintProxyAuth(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d (no server access): %s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
}

func TestMintProxyAuth_Forbidden_UnknownServer(t *testing.T) {
	h, _ := newTabProxyTestHandler(t, true, "")
	req := withVars(withIdentity(httptest.NewRequest("GET", "/api/servers/999/tabs/2/proxy-auth", nil), "owner", false, "owner-id"), "999", "2")
	rec := httptest.NewRecorder()

	h.MintProxyAuth(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d (unknown server): %s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
}

func TestMintProxyAuth_Success_SetsScopedCookieAndTicketClaims(t *testing.T) {
	h, ah := newTabProxyTestHandler(t, true, "")
	req := withVars(withIdentity(httptest.NewRequest("GET", "/api/servers/1/tabs/2/proxy-auth", nil), "owner", false, "owner-id"), "1", "2")
	rec := httptest.NewRecorder()

	before := time.Now()
	h.MintProxyAuth(rec, req)
	after := time.Now()

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
	if c.Secure {
		t.Error("cookie Secure = true on a plain-HTTP test request, want false (codebase convention: TLS/X-Forwarded-Proto gated)")
	}
	wantPath := "/api/servers/1/tabs/2/proxy/"
	if c.Path != wantPath {
		t.Errorf("cookie Path = %q, want %q", c.Path, wantPath)
	}
	if c.MaxAge != 300 {
		t.Errorf("cookie MaxAge = %d, want 300 (5 minutes)", c.MaxAge)
	}

	claims, err := ah.ParseTabProxyTicket(c.Value)
	if err != nil {
		t.Fatalf("minted ticket did not parse: %v", err)
	}
	if claims.Purpose != "tab_proxy" {
		t.Errorf("Purpose = %q, want tab_proxy", claims.Purpose)
	}
	if claims.Username != "owner" || claims.IsAdmin {
		t.Errorf("claims identity = %q/%v, want owner/false", claims.Username, claims.IsAdmin)
	}
	if claims.ServerID != 1 || claims.TabID != 2 {
		t.Errorf("claims server/tab = %d/%d, want 1/2", claims.ServerID, claims.TabID)
	}
	if claims.ReadOnly {
		t.Error("ReadOnly = true for a non-demo owner, want false")
	}
	if claims.ExpiresAt == nil {
		t.Fatal("ExpiresAt not set")
	}
	exp := claims.ExpiresAt.Time
	if exp.Before(before.Add(tabProxyTicketTTL-2*time.Second)) || exp.After(after.Add(tabProxyTicketTTL+2*time.Second)) {
		t.Errorf("ExpiresAt = %v, want ~%v from mint time", exp, tabProxyTicketTTL)
	}
}

func TestMintProxyAuth_DemoAccount_TicketIsReadOnly(t *testing.T) {
	// The demo account owns server 1 directly (plausible real setup: the
	// showcase servers are owned by the designated demo user), so
	// checkServerAccess passes via ownership while isDemoAccount still flags
	// the session read-only - same determination servers.go uses.
	h, ah := newTabProxyTestHandler(t, true, "demo-id")
	fs := h.state.Store.(*tabProxyFakeStore)
	fs.servers[1].OwnerName = "demo"
	fs.servers[1].OwnerID = "demo-id"

	req := withVars(withIdentity(httptest.NewRequest("GET", "/api/servers/1/tabs/2/proxy-auth", nil), "demo", false, "demo-id"), "1", "2")
	rec := httptest.NewRecorder()

	h.MintProxyAuth(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusNoContent, rec.Body.String())
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected exactly 1 cookie, got %d", len(cookies))
	}
	claims, err := ah.ParseTabProxyTicket(cookies[0].Value)
	if err != nil {
		t.Fatalf("minted ticket did not parse: %v", err)
	}
	if !claims.ReadOnly {
		t.Error("ReadOnly = false for the demo account, want true")
	}
}

// signRawClaims signs an arbitrary Claims value with the test handler's
// signing key, bypassing IssueTabProxyTicket/IssueToken - used to construct
// deliberately-invalid tickets (expired, wrong purpose) that the mint helpers
// would never produce.
func signRawClaims(t *testing.T, claims *Claims) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	s, err := tok.SignedString([]byte(tabProxyTestSecret))
	if err != nil {
		t.Fatalf("signing raw claims: %v", err)
	}
	return s
}

func newProxyRequest(method, serverID, tabID string, cookie *http.Cookie, rawQuery string) *http.Request {
	url := "/api/servers/" + serverID + "/tabs/" + tabID + "/proxy/"
	if rawQuery != "" {
		url += "?" + rawQuery
	}
	req := httptest.NewRequest(method, url, nil)
	req = withVars(req, serverID, tabID)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	return req
}

func TestInDashboard_RejectsMissingCookie(t *testing.T) {
	h, _ := newTabProxyTestHandler(t, true, "")
	req := newProxyRequest("GET", "1", "2", nil, "")
	rec := httptest.NewRecorder()

	h.InDashboard(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d (no cookie)", rec.Code, http.StatusUnauthorized)
	}
}

func TestInDashboard_RejectsExpiredTicket(t *testing.T) {
	h, _ := newTabProxyTestHandler(t, true, "")
	expired := &Claims{
		Username: "owner", Purpose: "tab_proxy", ServerID: 1, TabID: 2,
		RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(-time.Minute))},
	}
	cookie := &http.Cookie{Name: proxyCookieName, Value: signRawClaims(t, expired)}
	req := newProxyRequest("GET", "1", "2", cookie, "")
	rec := httptest.NewRecorder()

	h.InDashboard(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d (expired ticket)", rec.Code, http.StatusUnauthorized)
	}
}

func TestInDashboard_RejectsWrongPurposeToken(t *testing.T) {
	h, ah := newTabProxyTestHandler(t, true, "")
	// A normal, otherwise-valid 24h session token must NOT authenticate the
	// proxy - this is exactly the I1/I2 bypass the fix closes: no session JWT
	// (whether via ?token=, header, or now stuffed into the cookie by hand)
	// is accepted here anymore, only a Purpose == "tab_proxy" ticket.
	sessionTok, err := ah.IssueToken("owner", false)
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}
	cookie := &http.Cookie{Name: proxyCookieName, Value: sessionTok}
	req := newProxyRequest("GET", "1", "2", cookie, "")
	rec := httptest.NewRecorder()

	h.InDashboard(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d (session token in cookie, wrong purpose)", rec.Code, http.StatusUnauthorized)
	}
}

func TestInDashboard_RejectsWrongServerOrTabClaim(t *testing.T) {
	h, ah := newTabProxyTestHandler(t, true, "")

	tests := []struct {
		name                    string
		ticketServer, ticketTab int
		urlServer, urlTab       string
	}{
		{"wrong server", 1, 2, "5", "2"},
		{"wrong tab", 1, 2, "1", "9"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ticket, err := ah.IssueTabProxyTicket("owner", false, tc.ticketServer, tc.ticketTab, false)
			if err != nil {
				t.Fatalf("IssueTabProxyTicket: %v", err)
			}
			cookie := &http.Cookie{Name: proxyCookieName, Value: ticket}
			req := newProxyRequest("GET", tc.urlServer, tc.urlTab, cookie, "")
			rec := httptest.NewRecorder()

			h.InDashboard(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, http.StatusUnauthorized, tc.name)
			}
		})
	}
}

func TestInDashboard_ReadOnlyTicket_RejectsNonGET(t *testing.T) {
	h, ah := newTabProxyTestHandler(t, true, "")
	ticket, err := ah.IssueTabProxyTicket("demo", false, 1, 2, true)
	if err != nil {
		t.Fatalf("IssueTabProxyTicket: %v", err)
	}
	cookie := &http.Cookie{Name: proxyCookieName, Value: ticket}

	for _, method := range []string{"POST", "PUT", "DELETE", "PATCH"} {
		t.Run(method, func(t *testing.T) {
			req := newProxyRequest(method, "1", "2", cookie, "")
			rec := httptest.NewRecorder()

			h.InDashboard(rec, req)

			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want %d (%s on read-only ticket)", rec.Code, http.StatusForbidden, method)
			}
		})
	}
}

func TestInDashboard_ReadOnlyTicket_AllowsGETPastAuthGate(t *testing.T) {
	h, ah := newTabProxyTestHandler(t, true, "")
	ticket, err := ah.IssueTabProxyTicket("demo", false, 1, 2, true)
	if err != nil {
		t.Fatalf("IssueTabProxyTicket: %v", err)
	}
	cookie := &http.Cookie{Name: proxyCookieName, Value: ticket}

	for _, method := range []string{"GET", "HEAD"} {
		t.Run(method, func(t *testing.T) {
			req := newProxyRequest(method, "1", "2", cookie, "")
			rec := httptest.NewRecorder()

			h.InDashboard(rec, req)

			// No DB behind the fake store, so getTabByID always errors past
			// the auth gate - a 404 here (not 401/403) is the signal that
			// the cookie + read-only checks were both satisfied.
			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want %d (past auth gate, tab lookup 404s next)", rec.Code, http.StatusNotFound)
			}
		})
	}
}

func TestInDashboard_NoLongerAcceptsQueryToken(t *testing.T) {
	h, ah := newTabProxyTestHandler(t, true, "")
	sessionTok, err := ah.IssueToken("owner", false)
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}
	// Old bootstrap path: a valid 24h session JWT via ?token=. Must now be
	// completely ignored - the proxy accepts ONLY the dyl_tabproxy cookie.
	req := newProxyRequest("GET", "1", "2", nil, "token="+sessionTok)
	rec := httptest.NewRecorder()

	h.InDashboard(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d (?token= must no longer authenticate)", rec.Code, http.StatusUnauthorized)
	}
}

func TestInDashboard_FeatureDisabledRejectsBeforeTicketCheck(t *testing.T) {
	h, ah := newTabProxyTestHandler(t, false, "")
	ticket, err := ah.IssueTabProxyTicket("owner", false, 1, 2, false)
	if err != nil {
		t.Fatalf("IssueTabProxyTicket: %v", err)
	}
	cookie := &http.Cookie{Name: proxyCookieName, Value: ticket}
	req := newProxyRequest("GET", "1", "2", cookie, "")
	rec := httptest.NewRecorder()

	h.InDashboard(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d (feature disabled, even with a valid ticket)", rec.Code, http.StatusForbidden)
	}
}
