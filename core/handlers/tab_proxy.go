package handlers

// ProxyHandler serves the WS5 custom-tab reverse proxy: it streams a server
// container's HTTP/WebSocket through Core over the existing gRPC mesh so the
// browser only ever talks to Core on the panel origin. Two entry points:
// InDashboard (session-authed) and Public (share-token, Task 9). Both share
// serve().

import (
	"bytes"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	pb "dylaris-proto/node"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
)

const proxyCookieName = "dyl_tabproxy"
const proxyMaxRequestBody = 1 << 20 // 1 MB inline request-body cap

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

// resolveIdentity authenticates a proxy request from the Authorization header,
// the ?token= query, or the scoped dyl_tabproxy cookie. Returns ok=false when
// no valid session token is present. This is the ONLY auth gate for this
// endpoint (security invariant #3): InDashboard is registered on the root
// router, bypassing the /api subrouter's AuthMiddleware/setup-lock/maintenance
// chain entirely, so every check here must be self-contained.
func (h *ProxyHandler) resolveIdentity(r *http.Request) (username string, isAdmin bool, userID string, ok bool) {
	tokenStr := ""
	if a := r.Header.Get("Authorization"); strings.HasPrefix(a, "Bearer ") {
		tokenStr = strings.TrimPrefix(a, "Bearer ")
	}
	if tokenStr == "" {
		tokenStr = r.URL.Query().Get("token")
	}
	if tokenStr == "" {
		if c, err := r.Cookie(proxyCookieName); err == nil {
			tokenStr = c.Value
		}
	}
	if tokenStr == "" {
		return "", false, "", false
	}
	claims, err := h.auth.ParseSessionToken(tokenStr)
	if err != nil {
		return "", false, "", false
	}
	uid := ""
	if h.state.Store != nil {
		if u, uerr := h.state.Store.GetUserByUsername(claims.Username); uerr == nil && u != nil {
			uid = u.ID
		}
	}
	return claims.Username, claims.IsAdmin, uid, true
}

// setProxyCookie stamps a short-lived, path-scoped HttpOnly cookie carrying the
// JWT so the iframe's relative sub-requests authenticate. Only when the request
// authenticated via header/query (not from the cookie itself). Security
// invariant #4: HttpOnly (unreadable to the proxied page's JS), SameSite=Strict
// (never sent on a cross-site navigation/subrequest), and Path scoped to THIS
// tab's proxy prefix only — never site-wide, so it cannot leak into any other
// tab's, server's, or the panel's own requests.
func (h *ProxyHandler) setProxyCookie(w http.ResponseWriter, r *http.Request, basePath string) {
	tokenStr := ""
	if a := r.Header.Get("Authorization"); strings.HasPrefix(a, "Bearer ") {
		tokenStr = strings.TrimPrefix(a, "Bearer ")
	}
	if tokenStr == "" {
		tokenStr = r.URL.Query().Get("token")
	}
	if tokenStr == "" {
		return
	}
	secure := r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
	http.SetCookie(w, &http.Cookie{
		Name:     proxyCookieName,
		Value:    tokenStr,
		Path:     basePath,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   3600,
	})
}

// InDashboard: ANY /api/servers/{id}/tabs/{tabId}/proxy/{rest...} - session +
// overview access to the server (same gate as tab read). Order matters: the
// feature gate is checked before touching the DB or trusting any identity, so
// a disabled feature never exposes even the existence of a tab/server.
func (h *ProxyHandler) InDashboard(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	serverID, _ := strconv.Atoi(vars["id"])
	tabID, _ := strconv.Atoi(vars["tabId"])
	subPath := vars["rest"]

	// Security invariant #5: master feature gate. A disabled feature must
	// reject every request here regardless of session validity.
	if !h.state.FeatureFlags.IsTabProxyEnabled(r.Context()) {
		http.Error(w, "Tab proxy disabled", http.StatusForbidden)
		return
	}
	// Security invariant #3: in-handler session auth (no middleware to lean on).
	username, isAdmin, userID, ok := h.resolveIdentity(r)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	// Security invariant #5 (cont.): only a tab explicitly in "proxied" mode
	// may be proxied — a "direct" (plain external URL) tab must never route
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
	srv, serr := h.state.Store.GetServerByID(serverID)
	if serr != nil || srv == nil {
		http.Error(w, "Server not found", http.StatusNotFound)
		return
	}
	// Security invariant #3 (cont.): explicit permission check, same class as
	// the read-only tab list ("overview"), enforced in-handler.
	if !checkServerAccess(h.state.Store, srv, username, isAdmin, userID, "overview") {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	basePath := fmt.Sprintf("/api/servers/%d/tabs/%d/proxy/", serverID, tabID)
	h.setProxyCookie(w, r, basePath)
	h.serve(w, r, tab, basePath, subPath)
}

// serve branches to the WS bridge (Task 10) or the HTTP path.
func (h *ProxyHandler) serve(w http.ResponseWriter, r *http.Request, tab *proxyTab, basePath, subPath string) {
	if isWebSocketUpgrade(r) {
		// Replaced by h.serveWS in Task 10.
		http.Error(w, "WebSocket proxying not available", http.StatusNotImplemented)
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
	// client-controlled {rest:.*} route capture — sanitize the JOINED result
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
			// DB-stored value fetched via getTabByID — never anything derived
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
			if !isHTML {
				writeProxyHeaders(w, hd.Headers, false)
				w.WriteHeader(int(hd.StatusCode))
				headerWritten = true
			}
			continue
		}
		if chunk := resp.GetChunk(); chunk != nil {
			if isHTML {
				htmlBuf.Write(chunk.Data)
			} else {
				if !headerWritten {
					w.WriteHeader(http.StatusOK)
					headerWritten = true
				}
				w.Write(chunk.Data)
				if canFlush {
					flusher.Flush()
				}
			}
		}
	}

	if isHTML && head != nil {
		out := injectBaseHref(htmlBuf.Bytes(), basePath)
		writeProxyHeaders(w, head.Headers, true) // drop upstream Content-Length; body changed
		w.Header().Set("Content-Length", strconv.Itoa(len(out)))
		w.WriteHeader(int(head.StatusCode))
		w.Write(out)
	}
}

// forwardRequestHeaders converts the browser request headers into the wire
// slice, dropping hop-by-hop, the panel session cookie/Authorization (never
// forwarded to the container — security invariant #6), and Accept-Encoding
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

func coreStripHopByHop(hs []*pb.HttpHeader) []*pb.HttpHeader {
	out := make([]*pb.HttpHeader, 0, len(hs))
	for _, h := range hs {
		if coreHopByHop[strings.ToLower(h.Key)] {
			continue
		}
		out = append(out, h)
	}
	return out
}

func writeProxyHeaders(w http.ResponseWriter, hs []*pb.HttpHeader, dropContentLength bool) {
	for _, h := range coreStripHopByHop(hs) {
		if dropContentLength && strings.EqualFold(h.Key, "Content-Length") {
			continue
		}
		w.Header().Add(h.Key, h.Value)
	}
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
// which already keeps the host fixed regardless of what's in path — but this
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

// keep the time import used even before Task 9 references it (share expiry).
var _ = time.Time{}
