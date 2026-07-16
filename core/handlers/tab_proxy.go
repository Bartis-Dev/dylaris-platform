package handlers

// ProxyHandler serves the WS5 custom-tab reverse proxy: it streams a server
// container's HTTP/WebSocket through Core over the existing gRPC mesh so the
// browser only ever talks to Core on the panel origin. Two entry points:
// InDashboard (session-authed) and Public (share-token, Task 9). Both share
// serve().
//
// Auth is cookie-only (Task 8 fast-follow, closing two Important review
// findings): MintProxyAuth runs behind the normal /api subrouter's
// AuthMiddleware (inheriting 2FA-setup-lock + demo-read-only gating for
// free) and, after re-checking overview access + the feature gate, mints a
// short-lived (5min) tab-proxy-scoped ticket and stamps it as an HttpOnly,
// path-scoped dyl_tabproxy cookie. InDashboard then trusts ONLY that cookie -
// it never accepts a session JWT via header/query, so the 24h session token
// is never carried in this endpoint's URL.
//
// Public (Task 9) is the standalone share-link twin of the same idea: a
// public-visibility link is served anonymously (subject to the
// TabProxyAllowPublicLinks flag), while a private-visibility link requires
// the SAME dyl_tabproxy ticket cookie, minted by MintPublicProxyAuth
// (registered on the /api subrouter behind AuthMiddleware, mirroring
// MintProxyAuth) and Path-scoped to this share token's proxy prefix instead
// of the in-dashboard one. Public never mints or sets the cookie itself -
// it only validates it via the same ParseTabProxyTicket InDashboard uses,
// so there is exactly one place that ever parses a tab-proxy ticket's
// signature/claims.

import (
	"bytes"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	pb "dylaris-proto/node"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
)

const proxyCookieName = "dyl_tabproxy"
const proxyMaxRequestBody = 1 << 20 // 1 MB inline request-body cap
const proxyMaxHTMLBuffer = 10 << 20 // 10 MB cap on buffered text/html (base-href injection); see serveHTTP.

// coreHopByHop lists headers dropped in both directions (RFC 7230 6.1).
var coreHopByHop = map[string]bool{
	"connection":          true,
	"keep-alive":          true,
	"proxy-authenticate":  true,
	"proxy-authorization": true,
	"te":                  true,
	"trailer":             true,
	"transfer-encoding":   true,
	"upgrade":             true,
}

// coreResponseStrip lists headers dropped ONLY from the container's response
// on its way back to the browser (WS5 final-review hardening) - never
// applied to forwardRequestHeaders' outbound direction. The proxied response
// is served under the panel's own origin, so a container that emitted
// Set-Cookie/Set-Cookie2 could set or clobber a cookie in the visitor's
// browser on that origin (cookie-bomb/fixation surface, worse on the public
// share path - Public can serve a tab anonymously). The map/plugin web UIs
// this proxy serves (BlueMap/squaremap/Dynmap) use localStorage, not
// cookies, so nothing legitimate depends on this passing through.
//
// Cache-Control/Expires/Pragma are stripped for a second reason: serveHTTP
// and Public both stamp their own authoritative "Cache-Control: no-store" on
// this per-user/per-ticket response via Set, but relaying a container's own
// Cache-Control (e.g. "public, max-age=3600") through afterwards via Add
// would put TWO Cache-Control values on the wire - a lenient shared cache
// could honor the container's permissive one and cache per-user content on
// the shared panel origin. Stripping them here (the single chokepoint every
// container header passes through) guarantees "no-store" stays the sole,
// authoritative value.
var coreResponseStrip = map[string]bool{
	"set-cookie":    true,
	"set-cookie2":   true,
	"cache-control": true,
	"expires":       true,
	"pragma":        true,
}

type ProxyHandler struct {
	state *AppState
	auth  *AuthHandler
}

func NewProxyHandler(state *AppState, auth *AuthHandler) *ProxyHandler {
	return &ProxyHandler{state: state, auth: auth}
}

type proxyTab struct {
	ID           int
	ServerID     int
	ServerUUID   string
	NodeID       int
	Mode         string
	TargetPort   int
	TargetPath   string
	Surface      string
	Visibility   string
	ShareExpires sql.NullTime
	Enabled      bool
}

func (h *ProxyHandler) rawDB() *sql.DB {
	if h.state.Store == nil {
		return nil
	}
	provider, ok := h.state.Store.(sparkDBProvider)
	if !ok {
		return nil
	}
	return provider.RawDB()
}

func (h *ProxyHandler) getTabByID(serverID, tabID int) (*proxyTab, error) {
	db := h.rawDB()
	if db == nil {
		return nil, fmt.Errorf("db unavailable")
	}
	var t proxyTab
	err := db.QueryRow(`SELECT t.id, t.server_id, s.uuid, s.node_id, t.mode,
		COALESCE(t.target_port,0), t.target_path, t.surface, t.visibility, t.share_expires_at, t.enabled
		FROM server_tabs t JOIN servers s ON s.id = t.server_id
		WHERE t.id=$1 AND t.server_id=$2`, tabID, serverID).Scan(
		&t.ID, &t.ServerID, &t.ServerUUID, &t.NodeID, &t.Mode,
		&t.TargetPort, &t.TargetPath, &t.Surface, &t.Visibility, &t.ShareExpires, &t.Enabled)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// proxyBasePath builds the proxy prefix a given server/tab is served (and
// cookie-scoped) under. Shared by MintProxyAuth (cookie Path) and InDashboard
// (base-href injection) so the two can never drift apart.
func proxyBasePath(serverID, tabID int) string {
	return fmt.Sprintf("/api/servers/%d/tabs/%d/proxy/", serverID, tabID)
}

// MintProxyAuth: GET /api/servers/{id}/tabs/{tabId}/proxy-auth, registered on
// the NORMAL /api subrouter behind AuthMiddleware and the router's
// overview.read RequireCap - this inherits the full session gating
// (2FA-setup-lock, demo read-only, signature/expiry) plus server-level access
// enforcement for free instead of re-implementing it here. After the feature
// gate, it mints a short-lived tab-proxy-scoped ticket and stamps it as an
// HttpOnly cookie scoped to exactly this server/tab's proxy prefix, so
// InDashboard never needs the 24h session JWT in a URL.
//
// This does NOT re-check the tab's own state (enabled/mode/surface) - that
// stays the DB-backed getTabByID gate InDashboard runs on every actual proxy
// request regardless of the ticket, so a ticket minted here for a bad tabId
// simply 404s the moment it is used.
func (h *ProxyHandler) MintProxyAuth(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	serverID, _ := strconv.Atoi(vars["id"])
	tabID, _ := strconv.Atoi(vars["tabId"])

	if !h.state.FeatureFlags.IsTabProxyEnabled(r.Context()) {
		sendJSONError(w, "Tab proxy disabled", http.StatusForbidden)
		return
	}

	username, _ := r.Context().Value("username").(string)
	isAdmin, _ := r.Context().Value("isAdmin").(bool)
	userID, _ := r.Context().Value("userID").(string)

	// Existence check only: the route's overview.read RequireCap already
	// enforced access before this handler ran (formerly a checkServerAccess
	// call here).
	if _, serr := h.state.Store.GetServerByID(serverID); serr != nil {
		sendJSONError(w, "Server not found", http.StatusNotFound)
		return
	}

	// The demo account is forced read-only by AuthMiddleware for every other
	// endpoint; carry that same restriction into the ticket so it survives
	// through the proxy, which has no AuthMiddleware of its own to re-derive
	// it from.
	readOnly := !isAdmin && isDemoAccount(h.state, userID)

	ticket, err := h.auth.IssueTabProxyTicket(username, isAdmin, serverID, tabID, readOnly)
	if err != nil {
		sendJSONError(w, "Failed to mint proxy ticket", http.StatusInternalServerError)
		return
	}

	secure := r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
	http.SetCookie(w, &http.Cookie{
		Name:     proxyCookieName,
		Value:    ticket,
		Path:     proxyBasePath(serverID, tabID),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(tabProxyTicketTTL.Seconds()),
	})
	// Defense in depth: this response carries no token in the body, but the
	// codebase's convention for any auth-adjacent endpoint is to keep it out
	// of caches/Referer regardless.
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

// resolveProxyTicket validates the dyl_tabproxy cookie and confirms its
// server-id + tab-id claims match this request's URL. This is the ONLY auth
// gate for this endpoint (security invariant #3): InDashboard is registered
// on the root router, bypassing the /api subrouter's
// AuthMiddleware/setup-lock/maintenance chain entirely. Unlike the old
// design, this handler does NOT re-derive identity from a bearer/query
// session token - the ticket's claims are trusted as-is because they were
// only ever minted by MintProxyAuth, which already ran the full
// AuthMiddleware + overview.read RequireCap + feature-gate chain.
func (h *ProxyHandler) resolveProxyTicket(r *http.Request, serverID, tabID int) (username string, isAdmin, readOnly, ok bool) {
	c, err := r.Cookie(proxyCookieName)
	if err != nil || c.Value == "" {
		return "", false, false, false
	}
	claims, err := h.auth.ParseTabProxyTicket(c.Value)
	if err != nil {
		return "", false, false, false
	}
	if claims.ServerID != serverID || claims.TabID != tabID {
		return "", false, false, false
	}
	return claims.Username, claims.IsAdmin, claims.ReadOnly, true
}

// InDashboard: ANY /api/servers/{id}/tabs/{tabId}/proxy/{rest...} - cookie-only
// ticket auth (see MintProxyAuth). Order matters: the feature gate is checked
// before touching the ticket or the DB, so a disabled feature never exposes
// even the existence of a tab/server.
func (h *ProxyHandler) InDashboard(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	serverID, _ := strconv.Atoi(vars["id"])
	tabID, _ := strconv.Atoi(vars["tabId"])
	subPath := vars["rest"]

	// Security invariant #5: master feature gate. A disabled feature must
	// reject every request here regardless of ticket validity.
	if !h.state.FeatureFlags.IsTabProxyEnabled(r.Context()) {
		http.Error(w, "Tab proxy disabled", http.StatusForbidden)
		return
	}
	// Security invariant #3: cookie-only ticket auth. Missing, expired,
	// wrong-signature, wrong-purpose or wrong-server/tab all collapse to the
	// same 401 so the panel knows to re-mint via MintProxyAuth.
	_, _, readOnly, ok := h.resolveProxyTicket(r, serverID, tabID)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	// Security invariant #7: a ticket minted for a demo/read-only session may
	// only ever GET/HEAD through the proxy, mirroring AuthMiddleware's
	// demo-read-only gate on every other endpoint.
	if readOnly && r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "Read-only session", http.StatusForbidden)
		return
	}
	// Security invariant (WS): a bidirectional WebSocket cannot be constrained
	// by the GET/HEAD method gate above (the handshake itself is a GET, but
	// once upgraded either side can send write-frames), so a read-only/demo
	// ticket must never be allowed to open one at all.
	if readOnly && isWebSocketUpgrade(r) {
		http.Error(w, "Read-only session", http.StatusForbidden)
		return
	}
	// Security invariant #5 (cont.): only a tab explicitly in "proxied" mode
	// may be proxied - a "direct" (plain external URL) tab must never route
	// through the mesh.
	tab, err := h.getTabByID(serverID, tabID)
	if err != nil || tab == nil || !tab.Enabled || tab.Mode != "proxied" {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}
	if tab.Surface != "tab" && tab.Surface != "both" {
		http.Error(w, "Not a dashboard tab", http.StatusNotFound)
		return
	}
	h.serve(w, r, tab, proxyBasePath(serverID, tabID), subPath)
}

// serve branches to the WS bridge (Task 10) or the HTTP path.
func (h *ProxyHandler) serve(w http.ResponseWriter, r *http.Request, tab *proxyTab, basePath, subPath string) {
	if isWebSocketUpgrade(r) {
		h.serveWS(w, r, tab)
		return
	}
	h.serveHTTP(w, r, tab, basePath, subPath)
}

// serveHTTP dispatches one HTTP request over the mesh and streams the response.
// text/html is buffered so a <base href> can be injected; everything else is
// streamed straight through.
func (h *ProxyHandler) serveHTTP(w http.ResponseWriter, r *http.Request, tab *proxyTab, basePath, subPath string) {
	target := singleJoin(tab.TargetPath, subPath)
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	// Security invariant #2: normalize to a safe origin-form path before it
	// crosses the mesh boundary. tab.TargetPath is DB-validated at tab
	// creation/update (must start with "/"), but subPath is the raw,
	// client-controlled {rest:.*} route capture - sanitize the JOINED result
	// so a request like .../proxy/@evil.com/x or .../proxy//evil.com/x can
	// never be turned into something that re-targets the host on the node
	// side, however the node ends up building/parsing the outbound URL.
	target = sanitizeProxyPath(target)
	var body []byte
	if r.Body != nil {
		b, _ := io.ReadAll(io.LimitReader(r.Body, proxyMaxRequestBody+1))
		if len(b) > proxyMaxRequestBody {
			http.Error(w, "Request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		body = b
	}
	reqID := uuid.NewString()
	msg := &pb.NodeMessage{
		RequestId:  reqID,
		ServerUuid: tab.ServerUUID,
		Payload: &pb.NodeMessage_HttpProxyReq{HttpProxyReq: &pb.HttpProxyReq{
			// Security invariant #1: the port sent to the node is ALWAYS the
			// DB-stored value fetched via getTabByID - never anything derived
			// from the request or the client. This is what closes the SSRF
			// containment: the node dials whatever port Core asks for, so
			// Core must never let a client choose it.
			TargetPort: int32(tab.TargetPort),
			Method:     r.Method,
			Path:       target,
			Headers:    h.forwardRequestHeaders(r),
			Body:       body,
		}},
	}
	ch, err := h.state.GRPCRegistry.SendRequestStreaming(tab.NodeID, msg)
	if err != nil {
		http.Error(w, "Node communication error: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer h.state.GRPCRegistry.CleanupRequest(tab.NodeID, reqID)

	var head *pb.HttpProxyRespHead
	isHTML := false
	// htmlOverflowed flips once the buffered text/html body exceeds
	// proxyMaxHTMLBuffer; see the cap comment below.
	htmlOverflowed := false
	var htmlBuf bytes.Buffer
	headerWritten := false
	flusher, canFlush := w.(http.Flusher)

	for resp := range ch {
		if e := resp.GetError(); e != nil {
			if !headerWritten {
				http.Error(w, e.Message, int(e.Code))
			}
			return
		}
		if hd := resp.GetHttpProxyRespHead(); hd != nil {
			head = hd
			isHTML = strings.HasPrefix(strings.ToLower(headerValue(hd.Headers, "Content-Type")), "text/html")
			// The proxied page is per-user/per-ticket - never let a shared
			// cache in front of Core (or the browser's disk cache) keep it.
			// This stays the SOLE Cache-Control value: coreResponseStrip
			// drops any container-supplied Cache-Control/Expires/Pragma
			// before writeProxyHeaders relays the rest, so a later Add can
			// never append a second, more permissive value.
			w.Header().Set("Cache-Control", "no-store")
			if !isHTML {
				writeProxyHeaders(w, hd.Headers, false)
				w.WriteHeader(int(hd.StatusCode))
				headerWritten = true
			}
			continue
		}
		chunk := resp.GetChunk()
		if chunk == nil {
			continue
		}
		if !isHTML {
			if !headerWritten {
				w.WriteHeader(http.StatusOK)
				headerWritten = true
			}
			w.Write(chunk.Data)
			if canFlush {
				flusher.Flush()
			}
			continue
		}
		// text/html is buffered (up to proxyMaxHTMLBuffer) so a <base href>
		// can be injected once the full body is known. Buffering it
		// unconditionally would let a single large text/html response grow
		// this handler's memory without bound - a per-request memory-DoS
		// (WS5 Task 8 follow-up fix). Past the cap, buffering stops and the
		// rest of the body streams straight through with no base-href
		// injection: a 502 would be simpler but would break an otherwise
		// perfectly servable large page for no real security benefit, so
		// graceful degradation is the better tradeoff here.
		if !htmlOverflowed && htmlBuf.Len()+len(chunk.Data) > proxyMaxHTMLBuffer {
			htmlOverflowed = true
			writeProxyHeaders(w, head.Headers, true) // drop upstream Content-Length; body no longer matches
			w.WriteHeader(int(head.StatusCode))
			headerWritten = true
			w.Write(htmlBuf.Bytes())
			htmlBuf.Reset()
		}
		if htmlOverflowed {
			w.Write(chunk.Data)
			if canFlush {
				flusher.Flush()
			}
			continue
		}
		htmlBuf.Write(chunk.Data)
	}

	if isHTML && !htmlOverflowed && head != nil {
		out := injectBaseHref(htmlBuf.Bytes(), basePath)
		writeProxyHeaders(w, head.Headers, true) // drop upstream Content-Length; body changed
		w.Header().Set("Content-Length", strconv.Itoa(len(out)))
		w.WriteHeader(int(head.StatusCode))
		w.Write(out)
	}
}

// forwardRequestHeaders converts the browser request headers into the wire
// slice, dropping hop-by-hop, the panel session cookie/Authorization (never
// forwarded to the container - security invariant #6), and Accept-Encoding
// (so text/html arrives uncompressed for <base> injection).
func (h *ProxyHandler) forwardRequestHeaders(r *http.Request) []*pb.HttpHeader {
	out := []*pb.HttpHeader{}
	for k, vals := range r.Header {
		lk := strings.ToLower(k)
		if coreHopByHop[lk] || lk == "authorization" || lk == "cookie" || lk == "accept-encoding" {
			continue
		}
		for _, v := range vals {
			out = append(out, &pb.HttpHeader{Key: k, Value: v})
		}
	}
	return out
}

// --- pure helpers ---

// coreStripHopByHop drops both the true hop-by-hop headers (coreHopByHop) and
// the response-only strip set (coreResponseStrip, e.g. Set-Cookie) - the
// single chokepoint writeProxyHeaders relies on before anything from a
// container response reaches the browser on the panel origin.
func coreStripHopByHop(hs []*pb.HttpHeader) []*pb.HttpHeader {
	out := make([]*pb.HttpHeader, 0, len(hs))
	for _, h := range hs {
		lk := strings.ToLower(h.Key)
		if coreHopByHop[lk] || coreResponseStrip[lk] {
			continue
		}
		out = append(out, h)
	}
	return out
}

// writeProxyHeaders is the single chokepoint both InDashboard and Public
// relay a container's response headers through (via serve -> serveHTTP), so
// every response-side header rule lives here exactly once.
func writeProxyHeaders(w http.ResponseWriter, hs []*pb.HttpHeader, dropContentLength bool) {
	for _, h := range coreStripHopByHop(hs) {
		if dropContentLength && strings.EqualFold(h.Key, "Content-Length") {
			continue
		}
		w.Header().Add(h.Key, h.Value)
	}
	// WS5 final-review hardening: Set (not Add) so this always wins over any
	// container-supplied value - defense-in-depth against MIME-sniffing of
	// content relayed onto the panel origin.
	w.Header().Set("X-Content-Type-Options", "nosniff")
}

func headerValue(hs []*pb.HttpHeader, key string) string {
	for _, h := range hs {
		if strings.EqualFold(h.Key, key) {
			return h.Value
		}
	}
	return ""
}

// injectBaseHref inserts <base href="base"> right after the opening <head> tag
// (case-insensitive) so a proxied app's relative asset URLs resolve under the
// proxy prefix. When there is no <head> the tag is prepended.
func injectBaseHref(html []byte, base string) []byte {
	tag := []byte(`<base href="` + base + `">`)
	lower := bytes.ToLower(html)
	idx := bytes.Index(lower, []byte("<head"))
	if idx == -1 {
		return append(append([]byte{}, tag...), html...)
	}
	gt := bytes.IndexByte(lower[idx:], '>')
	if gt == -1 {
		return append(append([]byte{}, tag...), html...)
	}
	insertAt := idx + gt + 1
	out := make([]byte, 0, len(html)+len(tag))
	out = append(out, html[:insertAt]...)
	out = append(out, tag...)
	out = append(out, html[insertAt:]...)
	return out
}

// isWebSocketUpgrade reports whether the request is a WebSocket handshake.
func isWebSocketUpgrade(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket") &&
		strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade")
}

// singleJoin joins a base path and a forwarded sub-path without doubling or
// dropping the separating slash.
func singleJoin(base, sub string) string {
	if sub == "" {
		return base
	}
	if strings.HasSuffix(base, "/") && strings.HasPrefix(sub, "/") {
		return base + sub[1:]
	}
	if !strings.HasSuffix(base, "/") && !strings.HasPrefix(sub, "/") {
		return base + "/" + sub
	}
	return base + sub
}

// sanitizeProxyPath hardens the origin-form path (+ query) Core forwards to
// the node in HttpProxyReq.Path (security invariant #2, carried forward from
// earlier task reviews). The node currently builds the outbound URL by plain
// string concatenation ("http://"+addr+path, see node/grpc_tabproxy.go),
// which already keeps the host fixed regardless of what's in path - but this
// is the last point Core controls before the value crosses the mesh
// boundary, so it is normalized defensively rather than trusted to stay safe
// under some future node-side refactor:
//   - strips raw control characters (CR/LF, etc.) that could otherwise enable
//     request-line/header injection when concatenated into a raw URL string.
//   - collapses a leading run of slashes to exactly one, so a value like
//     "//evil.com/x" (scheme-relative in some URL parsers/consumers) can
//     never be mistaken for a different authority.
//   - guarantees the result starts with "/" (origin-form).
func sanitizeProxyPath(p string) string {
	var b strings.Builder
	b.Grow(len(p))
	for _, r := range p {
		if r < 0x20 || r == 0x7f {
			continue
		}
		b.WriteRune(r)
	}
	p = b.String()
	if p == "" || p[0] != '/' {
		p = "/" + p
	}
	for len(p) > 1 && p[1] == '/' {
		p = p[1:]
	}
	return p
}

// --- Task 9: public share-token endpoint ---

// getTabByShareToken resolves a share-token slug to its proxied tab + owning
// server, mirroring getTabByID's query shape but keyed on the public slug
// the standalone page has instead of a server+tab id pair.
func (h *ProxyHandler) getTabByShareToken(token string) (*proxyTab, error) {
	db := h.rawDB()
	if db == nil {
		return nil, fmt.Errorf("db unavailable")
	}
	var t proxyTab
	err := db.QueryRow(`SELECT t.id, t.server_id, s.uuid, s.node_id, t.mode,
		COALESCE(t.target_port,0), t.target_path, t.surface, t.visibility, t.share_expires_at, t.enabled
		FROM server_tabs t JOIN servers s ON s.id = t.server_id
		WHERE t.share_token=$1`, token).Scan(
		&t.ID, &t.ServerID, &t.ServerUUID, &t.NodeID, &t.Mode,
		&t.TargetPort, &t.TargetPath, &t.Surface, &t.Visibility, &t.ShareExpires, &t.Enabled)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// decideProxyAccess resolves the visibility gate for a share-token request.
// public + allowed serves anyone (even anon); public + disabled hides (404);
// private requires a valid tab-proxy ticket (401 if none/invalid) scoped to
// this exact tab (403 if the ticket is valid but minted for a different tab).
func decideProxyAccess(visibility string, allowPublic, authed, hasAccess bool) (allow bool, status int) {
	if visibility == "public" {
		if allowPublic {
			return true, http.StatusOK
		}
		return false, http.StatusNotFound
	}
	if !authed {
		return false, http.StatusUnauthorized
	}
	if !hasAccess {
		return false, http.StatusForbidden
	}
	return true, http.StatusOK
}

// shareTokenExpired reports whether an optional expiry is in the past (an
// expiry is "used up" the instant it's reached, not the instant after -
// hence !now.Before(expires.Time) rather than a strict After).
func shareTokenExpired(expires sql.NullTime, now time.Time) bool {
	return expires.Valid && !now.Before(expires.Time)
}

// publicProxyBasePath builds the proxy prefix (and cookie Path) for a
// share-token tab, mirroring proxyBasePath for the in-dashboard path so
// MintPublicProxyAuth and Public can never drift apart.
func publicProxyBasePath(token string) string {
	return "/api/tabproxy/" + token + "/"
}

// resolvePublicTicket validates the dyl_tabproxy cookie against an
// already-resolved share-token tab, mirroring resolveProxyTicket for
// InDashboard. authed reports whether a valid (signature + Purpose ==
// "tab_proxy" + unexpired) ticket was present at all; hasAccess additionally
// requires the ticket's server-id + tab-id claims to match THIS tab - a
// ticket that is valid but was minted for a different tab is
// authed-but-no-access, which decideProxyAccess turns into 403 rather than
// the 401 a missing/invalid cookie gets. readOnly carries the ticket's own
// ReadOnly claim (set by MintPublicProxyAuth from the demo-account check) so
// Public can apply the same read-only method gate InDashboard does - it is
// meaningless (and always false) when authed is false.
func (h *ProxyHandler) resolvePublicTicket(r *http.Request, tab *proxyTab) (authed, hasAccess, readOnly bool) {
	c, err := r.Cookie(proxyCookieName)
	if err != nil || c.Value == "" {
		return false, false, false
	}
	claims, err := h.auth.ParseTabProxyTicket(c.Value)
	if err != nil {
		return false, false, false
	}
	return true, claims.ServerID == tab.ServerID && claims.TabID == tab.ID, claims.ReadOnly
}

// MintPublicProxyAuth: GET /api/tabproxy/{token}/auth, registered on the
// NORMAL /api subrouter behind AuthMiddleware - like MintProxyAuth, this
// inherits the full session gating (2FA-setup-lock, demo read-only,
// signature/expiry) for free instead of re-implementing it. Only a
// visibility=="private" link needs a ticket at all (a public link is served
// anonymously by Public with no cookie involved), so any other visibility is
// rejected outright. After re-checking overview access on the tab's server,
// it mints a short-lived tab-proxy-scoped ticket and stamps it as an
// HttpOnly cookie scoped to exactly this share link's proxy prefix, so
// Public never needs the 24h session JWT either.
//
// Like MintProxyAuth, this does NOT re-check the tab's enabled/mode/surface/
// expiry state - that stays Public's DB-backed gate on every actual proxy
// request regardless of the ticket, so a ticket minted here for a
// since-revoked or expired link simply 410/404s the moment it is used.
func (h *ProxyHandler) MintPublicProxyAuth(w http.ResponseWriter, r *http.Request) {
	token := mux.Vars(r)["token"]

	if !h.state.FeatureFlags.IsTabProxyEnabled(r.Context()) {
		sendJSONError(w, "Tab proxy disabled", http.StatusForbidden)
		return
	}
	// Origin-isolation gate (spec B5): minting a share ticket only makes sense
	// once the standalone share will actually be served, i.e. from the isolated
	// origin. Refuse with a clear error otherwise, mirroring Public's gate, so
	// the /c page's authorize() flow gets a clean early refusal.
	if !h.state.TabProxyIsolationActive {
		sendJSONError(w, "Public tab sharing requires origin isolation (set TAB_PROXY_ORIGIN)", http.StatusForbidden)
		return
	}

	tab, err := h.getTabByShareToken(token)
	if err != nil || tab == nil {
		sendJSONError(w, "Not found", http.StatusNotFound)
		return
	}
	if tab.Visibility != "private" {
		// A public link is served anonymously by Public with no cookie at
		// all - minting a ticket for it would be pointless.
		sendJSONError(w, "Link does not require authentication", http.StatusBadRequest)
		return
	}

	username, _ := r.Context().Value("username").(string)
	isAdmin, _ := r.Context().Value("isAdmin").(bool)
	userID, _ := r.Context().Value("userID").(string)

	srv, serr := h.state.Store.GetServerByID(tab.ServerID)
	if serr != nil || srv == nil {
		sendJSONError(w, "Server not found", http.StatusNotFound)
		return
	}
	if !checkServerAccess(h.state.Store, srv, username, isAdmin, userID, "overview") {
		sendJSONError(w, "Forbidden", http.StatusForbidden)
		return
	}

	// Same read-only determination as MintProxyAuth: the demo account is
	// forced read-only everywhere else by AuthMiddleware, so carry that
	// forward into the ticket for a path that has no AuthMiddleware of its
	// own to re-derive it from.
	readOnly := !isAdmin && isDemoAccount(h.state, userID)

	ticket, err := h.auth.IssueTabProxyTicket(username, isAdmin, tab.ServerID, tab.ID, readOnly)
	if err != nil {
		sendJSONError(w, "Failed to mint proxy ticket", http.StatusInternalServerError)
		return
	}

	secure := r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
	http.SetCookie(w, &http.Cookie{
		Name:     proxyCookieName,
		Value:    ticket,
		Path:     publicProxyBasePath(token),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(tabProxyTicketTTL.Seconds()),
	})
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

// Public: ANY /api/tabproxy/{token} and /api/tabproxy/{token}/{rest...} -
// resolves the share token, applies the visibility gate, and dispatches to
// the same mesh serve() path InDashboard uses. Registered on the ROOT
// router (like /api/share) so it bypasses the /api subrouter's setup-lock +
// maintenance middleware; auth on the private path is cookie-only via the
// SAME ParseTabProxyTicket InDashboard trusts - there is no hand-rolled
// session parsing here, and this handler never sets the cookie itself
// (MintPublicProxyAuth does that, behind AuthMiddleware).
func (h *ProxyHandler) Public(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	token := vars["token"]
	subPath := vars["rest"]

	// Security invariant: master feature gate, checked before touching the
	// DB or the cookie - a disabled feature must reject every request here
	// regardless of ticket validity, mirroring InDashboard.
	if !h.state.FeatureFlags.IsTabProxyEnabled(r.Context()) {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}
	// Origin-isolation gate (spec B5): the standalone /c/<token> share data plane
	// is the cross-tenant C1 vector - a proxied container served on the panel's
	// own origin could read a viewer's panel token from localStorage. It is only
	// safe once served from a dedicated isolated origin, so refuse the entire
	// share data plane unless origin-isolation is active. A uniform 404 (not a
	// distinct error) avoids leaking share existence to anonymous visitors,
	// matching this handler's other not-available responses. InDashboard is
	// intentionally NOT gated: it keeps working in the same-origin fallback.
	if !h.state.TabProxyIsolationActive {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}
	tab, err := h.getTabByShareToken(token)
	if err != nil || tab == nil || !tab.Enabled || tab.Mode != "proxied" {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}
	if tab.Surface != "page" && tab.Surface != "both" {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}
	if shareTokenExpired(tab.ShareExpires, time.Now()) {
		http.Error(w, "This link has expired", http.StatusGone)
		return
	}

	allowPublic := h.state.FeatureFlags.TabProxyAllowPublicLinks(r.Context())
	// A public-visibility link serves anyone with no cookie involved at all;
	// only a private one needs the ticket-cookie check.
	var authed, hasAccess, readOnly bool
	if tab.Visibility != "public" {
		authed, hasAccess, readOnly = h.resolvePublicTicket(r, tab)
	}
	allow, status := decideProxyAccess(tab.Visibility, allowPublic, authed, hasAccess)
	if !allow {
		http.Error(w, http.StatusText(status), status)
		return
	}
	// Security invariant (mirrors InDashboard's own gate): a ticket minted for
	// a demo/read-only session may only ever GET/HEAD through the proxy. This
	// only ever fires on the PRIVATE ticket path - readOnly stays false above
	// for a public-visibility link, which has no ticket and is intentionally
	// left unrestricted by the admin's allowPublic choice.
	if readOnly && r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "Read-only session", http.StatusForbidden)
		return
	}
	// Security invariant (WS), mirroring InDashboard: a read-only/demo ticket
	// must never be allowed to open a WebSocket, since a bidirectional stream
	// cannot be constrained by the GET/HEAD gate above once upgraded. This
	// only ever fires on the private ticket path - readOnly stays false for a
	// public-visibility link, which is intentionally left unrestricted.
	if readOnly && isWebSocketUpgrade(r) {
		http.Error(w, "Read-only session", http.StatusForbidden)
		return
	}

	// The proxied page is per-share-link and, for a private link, per-ticket -
	// never let a shared cache or the browser's disk cache keep it.
	w.Header().Set("Cache-Control", "no-store")
	h.serve(w, r, tab, publicProxyBasePath(token), subPath)
}

// --- Task 10: WebSocket upgrade bridge ---

const coreWSFragmentSize = 60 * 1024

// maxWSMessageBytes caps one reassembled container->browser application
// message (see the container->browser loop in serveWS below). Without it, a
// container/node pair streaming endless Fin=false fragments (or one huge
// message) grows rxBuf here until Core OOMs, which takes the mesh-facing
// side down for EVERY tab-proxy session, not just this one - the same DoS
// shape the node side already closes for its own Core->container
// reassembly. This value MUST mirror the node's own maxWSMessageBytes
// (node/grpc_tabproxy_ws.go) since both sides are bounding the same
// conceptual message size, just in opposite directions.
const maxWSMessageBytes = 8 * 1024 * 1024 // 8 MiB

// wsReassemblyExceeded reports whether appending add bytes to a reassembly
// buffer that already holds cur bytes would push it past max. Pulled out as
// a pure helper (mirroring the shape of the node's wsBridge.appendInbound
// check) so the cap logic is table-testable without standing up a full
// WS/gRPC bridge.
func wsReassemblyExceeded(cur, add, max int) bool {
	return cur+add > max
}

// splitProxyBytes splits data into <=max fragments (reassembled on the far
// side); an empty message yields one empty fragment. Keeps a WsFrame under
// Core's 128KB gRPC receive cap.
func splitProxyBytes(data []byte, max int) [][]byte {
	if max <= 0 {
		max = coreWSFragmentSize
	}
	if len(data) == 0 {
		return [][]byte{{}}
	}
	var out [][]byte
	for off := 0; off < len(data); off += max {
		end := off + max
		if end > len(data) {
			end = len(data)
		}
		out = append(out, data[off:end])
	}
	return out
}

// serveWS upgrades the browser connection and bridges it to the container WS
// over the mesh: WsOpen, then WsFrame/WsClose in both directions. Application
// messages are fragmented at coreWSFragmentSize and reassembled on the far side
// via the WsFrame.fin flag. V1 does not append the forwarded sub-path (the
// live-WS endpoint sits at the app base path in target_path).
//
// Concurrency/teardown mirrors the node-side bridge (node/grpc_tabproxy_ws.go):
// exactly one reader goroutine (browser->container, below) and exactly one
// writer (the container->browser loop, which is this function's own
// goroutine - the http.Handler call itself), so gorilla's single-reader/
// single-writer requirement on bconn is never violated. Teardown is
// exactly-once via closeOnce/done regardless of which side triggers it: the
// browser closing/erroring, the container sending WsClose/an error, or the
// node connection dying (registry.Unregister closes every pending channel,
// so ch is closed and the `!ok` branch fires here). CleanupRequest is
// deferred so the registry's pending-request map never leaks an entry past
// this handler's return, matching the SendRequestStreaming contract used by
// every other mesh caller (see serveHTTP above).
func (h *ProxyHandler) serveWS(w http.ResponseWriter, r *http.Request, tab *proxyTab) {
	bconn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer bconn.Close()

	target := tab.TargetPath
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	// Security invariant #2 (see serveHTTP): normalize to a safe origin-form
	// path/query the same way before it crosses the mesh boundary, so the WS
	// path is never less sanitized than the HTTP one.
	target = sanitizeProxyPath(target)
	// A fresh, unique request_id per WS open (never reused) - the registry
	// keys the node-side bridge and pending-response channel by this id, so a
	// collision with any other in-flight request (a concurrent WS, or an
	// HTTP proxy call) would let one overwrite the other's bridge/channel.
	reqID := uuid.NewString()
	openMsg := &pb.NodeMessage{
		RequestId:  reqID,
		ServerUuid: tab.ServerUUID,
		Payload: &pb.NodeMessage_WsOpen{WsOpen: &pb.WsOpen{
			TargetPort: int32(tab.TargetPort),
			Path:       target,
			Headers:    h.forwardRequestHeaders(r),
		}},
	}
	ch, err := h.state.GRPCRegistry.SendRequestStreaming(tab.NodeID, openMsg)
	if err != nil {
		return
	}
	defer h.state.GRPCRegistry.CleanupRequest(tab.NodeID, reqID)

	done := make(chan struct{})
	var closeOnce sync.Once
	closeAll := func() {
		closeOnce.Do(func() {
			// Best-effort: if the node/connection is already gone this send
			// simply fails and is ignored - there is nothing left to notify.
			h.state.GRPCRegistry.SendOnStream(tab.NodeID, &pb.NodeMessage{
				RequestId:  reqID,
				ServerUuid: tab.ServerUUID,
				Payload:    &pb.NodeMessage_WsClose{WsClose: &pb.WsClose{Code: 1000}},
			})
			close(done)
			bconn.Close()
		})
	}

	// browser -> container (sole reader of bconn)
	go func() {
		defer closeAll()
		for {
			mt, data, rerr := bconn.ReadMessage()
			if rerr != nil {
				return
			}
			frags := splitProxyBytes(data, coreWSFragmentSize)
			for i, f := range frags {
				if serr := h.state.GRPCRegistry.SendOnStream(tab.NodeID, &pb.NodeMessage{
					RequestId:  reqID,
					ServerUuid: tab.ServerUUID,
					Payload: &pb.NodeMessage_WsFrame{WsFrame: &pb.WsFrame{
						Opcode: int32(mt), Data: f, Fin: i == len(frags)-1,
					}},
				}); serr != nil {
					return
				}
			}
		}
	}()

	// container -> browser (sole writer of bconn)
	var rxBuf []byte
	rxOpcode := websocket.TextMessage
	for {
		select {
		case <-done:
			return
		case resp, ok := <-ch:
			if !ok {
				// Node/connection died: registry.Unregister closed ch.
				closeAll()
				return
			}
			if resp.GetWsClose() != nil || resp.GetError() != nil {
				closeAll()
				return
			}
			fr := resp.GetWsFrame()
			if fr == nil {
				continue
			}
			// DoS-mirror fix: cap the reassembled message the same way the
			// node side caps its own Core->container reassembly. Past the
			// cap, stop reassembling and tear the bridge down via the
			// existing teardown path instead of growing rxBuf unboundedly.
			if wsReassemblyExceeded(len(rxBuf), len(fr.Data), maxWSMessageBytes) {
				closeAll()
				return
			}
			rxBuf = append(rxBuf, fr.Data...)
			if fr.Opcode != 0 {
				rxOpcode = int(fr.Opcode)
			}
			if !fr.Fin {
				continue
			}
			if werr := bconn.WriteMessage(rxOpcode, rxBuf); werr != nil {
				closeAll()
				return
			}
			rxBuf = nil
		}
	}
}
