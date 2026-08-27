package handlers

import (
	"encoding/json"
	"fmt"
	"html"
	"net"
	"net/http"
	"strings"
	"time"

	"dylaris-core/authz"

	"github.com/gorilla/mux"
)

// One ORIGIN per proxied tab, instead of one path prefix per proxied tab.
//
// The path-prefix data plane this replaced had to rewrite a container's HTML
// with a <base href>, and that only ever fixed RELATIVE urls. A path-absolute
// "/js/app.js" is resolved against the origin ROOT and ignores the base path
// entirely - which is exactly what BlueMap ("/js/app.*.js", "/settings.json")
// and Dynmap ("config.js", "css/", "up.php") emit. The two most deployed map
// plugins were the two that could not work, and no amount of rewriting fixes
// that from the outside: both projects tell operators to use a subdomain.
//
// Serving each tab at the ROOT of "<label>.<suffix>" removes the rewriting and
// the breakage in one move, and it is also the stronger isolation. A different
// hostname is a different origin, so a container's JS cannot reach the panel
// session token in the panel origin's localStorage. The port-based isolation it
// replaced achieved that too, but left the path problem untouched.
//
// What a subdomain does NOT buy is cookie isolation: cookies are scoped by
// registrable domain, so a container here could set Domain=<parent> and have it
// reach the panel host. That is why coreResponseStrip still drops Set-Cookie,
// and why moving the content to a separate registrable domain was rejected -
// the ticket cookie would become cross-site, needing SameSite=None, which
// Safari and Firefox block by default.

const (
	// tabProxyReservedPrefix is the one path on a content host that Core answers
	// itself instead of proxying. It is deliberately ugly: everything else on
	// this host belongs to the container, and a collision would shadow one of
	// its real routes.
	tabProxyReservedPrefix = "/__dyl/"
	// tabProxyMintPath sets this host's ticket cookie. See HostMint.
	tabProxyMintPath = "/__dyl/mint"
)

// TabProxyHostLabel returns the per-tab label when host is "<label>.<suffix>",
// and "" for anything else.
//
// The label is validated here, before any database lookup, so a hostile Host
// header never reaches the query. isProxyHostLabel accepts exactly 20 lowercase
// alphanumerics, which also rejects a multi-level label ("a.b.<suffix>") for
// free: a dot is not in the alphabet.
func TabProxyHostLabel(host, suffix string) string {
	if host == "" || suffix == "" {
		return ""
	}
	h := strings.ToLower(strings.TrimSpace(host))
	// SplitHostPort also handles the bracketed IPv6 form; it errors when there
	// is no port at all, which is the common case and means "leave it alone".
	if bare, _, err := net.SplitHostPort(h); err == nil {
		h = bare
	}
	h = strings.TrimSuffix(h, ".")
	dotted := "." + suffix
	if !strings.HasSuffix(h, dotted) {
		return ""
	}
	label := strings.TrimSuffix(h, dotted)
	if !isProxyHostLabel(label) {
		return ""
	}
	return label
}

// getTabByHostLabel resolves a content host back to its tab.
func (h *ProxyHandler) getTabByHostLabel(label string) (*proxyTab, error) {
	db := h.rawDB()
	if db == nil {
		return nil, fmt.Errorf("db unavailable")
	}
	var t proxyTab
	err := db.QueryRow(`SELECT t.id, t.server_id, s.uuid, s.node_id, t.mode,
		COALESCE(t.target_port,0), t.target_path, t.surface, t.visibility, t.share_expires_at,
		t.enabled, t.share_token
		FROM server_tabs t JOIN servers s ON s.id = t.server_id
		WHERE t.proxy_host_label=$1`, label).Scan(
		&t.ID, &t.ServerID, &t.ServerUUID, &t.NodeID, &t.Mode,
		&t.TargetPort, &t.TargetPath, &t.Surface, &t.Visibility, &t.ShareExpires,
		&t.Enabled, &t.ShareToken)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// hostVisibility collapses "may an anonymous visitor see this" into the value
// decideProxyAccess already understands.
//
// Every proxied tab now has a content host, including one that was never
// published. Passing tab.Visibility straight through would therefore make a
// public-but-unpublished tab anonymously reachable the moment it exists, which
// is not what "public" meant when the only way to reach a tab was a share link
// somebody had to hand out. So anonymity requires BOTH: marked public, and
// actually carrying a share token.
func hostVisibility(tab *proxyTab) string {
	if tab.Visibility == "public" && tab.ShareToken.Valid && tab.ShareToken.String != "" {
		return "public"
	}
	return "private"
}

// HostContent serves a request that arrived on a tab-content host.
//
// Auth is cookie-only, exactly as the path-mode data plane was: this handler
// never accepts a session JWT by header or query. The ticket is minted by
// HostMint, which does run behind AuthMiddleware.
func (h *ProxyHandler) HostContent(w http.ResponseWriter, r *http.Request) {
	label := TabProxyHostLabel(r.Host, h.state.TabProxyHostSuffix)
	if label == "" {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}
	// Master gate first, before the database and before the cookie: a disabled
	// feature rejects every request here regardless of ticket validity.
	if !h.state.FeatureFlags.IsTabProxyEnabled(r.Context()) {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}
	tab, err := h.getTabByHostLabel(label)
	if err != nil || tab == nil {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}
	if !h.allowHostRequest(w, r, tab) {
		return
	}
	// The request path IS the path inside the container, because this host
	// serves nothing else. That is the whole point: no prefix to strip, and
	// nothing to rewrite in the response.
	h.serve(w, r, tab, strings.TrimPrefix(r.URL.Path, "/"))
}

// allowHostRequest is every gate between a request and a container, split out
// so each one can be asserted without a database or a node behind it. It writes
// the refusal itself and reports false; true means serve.
//
// Order is load-bearing and mirrors the data plane this replaced: what the tab
// IS (enabled, proxied, unexpired) before who the CALLER is, so a disabled tab
// answers the same to everyone.
func (h *ProxyHandler) allowHostRequest(w http.ResponseWriter, r *http.Request, tab *proxyTab) bool {
	if !tab.Enabled || tab.Mode != "proxied" {
		http.Error(w, "Not found", http.StatusNotFound)
		return false
	}
	// A pinned tab addresses a port that only exists while ITS sub-server is the
	// one running. Serving it against another would hand the viewer a different
	// world's map under the name of theirs - or, if that port is something else
	// entirely there, a different application. Refused before the ticket check,
	// because this is about the tab, not the caller.
	if tab.SubServerName != "" && tab.SubServerName != tab.ActiveSubServer {
		h.writeHostGatePage(w, http.StatusConflict, "This tab is not running",
			"It belongs to a different sub-server than the one currently started.", "")
		return false
	}
	if shareTokenExpired(tab.ShareExpires, time.Now()) {
		h.writeHostGatePage(w, http.StatusGone, "This link has expired",
			"Ask whoever shared it for a new one.", "")
		return false
	}

	authed, hasAccess, readOnly := h.resolvePublicTicket(r, tab)
	allow, status := decideProxyAccess(
		hostVisibility(tab),
		h.state.FeatureFlags.TabProxyAllowPublicLinks(r.Context()),
		authed, hasAccess,
	)
	if !allow {
		switch status {
		case http.StatusUnauthorized:
			// This page cannot tell "not signed in" from "signed in, but this
			// browser holds no ticket for this tab yet" - it can read neither
			// the panel's localStorage nor a cookie that was never set. So it
			// does not guess: it sends the visitor to the panel, which knows
			// both.
			h.writeHostGatePage(w, status, "This page is private",
				"Open it from the panel to view it.", h.state.FrontendURL)
		case http.StatusForbidden:
			h.writeHostGatePage(w, status, "No permission",
				"Your account cannot view this tab.", "")
		default:
			http.Error(w, "Not found", http.StatusNotFound)
		}
		return false
	}

	// A demo account's ticket carries ReadOnly. A WebSocket is a write channel
	// we cannot inspect once upgraded, so it is refused outright rather than
	// allowed and hoped about.
	if readOnly && (isWebSocketUpgrade(r) || (r.Method != http.MethodGet && r.Method != http.MethodHead)) {
		http.Error(w, "Read-only", http.StatusForbidden)
		return false
	}
	return true
}

// HostMint sets this host's ticket cookie. Registered behind AuthMiddleware, so
// the full session chain (signature, expiry, denylist, 2FA-setup lock, demo
// read-only) is inherited rather than re-implemented here.
//
// The panel reaches this cross-origin and sends its session as a Bearer HEADER.
// That is only possible because the panel token lives in localStorage, not in a
// cookie: the other origin cannot READ it, but the panel can SEND it. No
// credential is ever put in a URL.
//
// The cookie is HOST-ONLY on purpose - no Domain attribute. Domain=<suffix>
// would work too and would be worse: it would send tab A's ticket to tab B's
// host, leaving only the claim check between them. Host-only makes cross-tab
// reuse impossible by scope, and the claim check below stays anyway.
func (h *ProxyHandler) HostMint(w http.ResponseWriter, r *http.Request) {
	label := TabProxyHostLabel(r.Host, h.state.TabProxyHostSuffix)
	if label == "" {
		sendJSONError(w, "Not found", http.StatusNotFound)
		return
	}
	if !h.state.FeatureFlags.IsTabProxyEnabled(r.Context()) {
		sendJSONError(w, "Tab proxy disabled", http.StatusForbidden)
		return
	}
	tab, err := h.getTabByHostLabel(label)
	if err != nil || tab == nil {
		sendJSONError(w, "Not found", http.StatusNotFound)
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
	res, rerr := h.state.Authz.Resolve(authz.Identity{UserID: userID, Username: username, IsAdmin: isAdmin}, srv.ID)
	if rerr != nil || !res.HasCap(tabsReadCap) {
		sendJSONError(w, "Forbidden", http.StatusForbidden)
		return
	}

	readOnly := !isAdmin && isDemoAccount(h.state, userID)
	ticket, terr := h.auth.IssueTabProxyTicket(username, isAdmin, tab.ServerID, tab.ID, readOnly)
	if terr != nil {
		sendJSONError(w, "Failed to mint proxy ticket", http.StatusInternalServerError)
		return
	}

	secure := r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
	http.SetCookie(w, &http.Cookie{
		Name:     proxyCookieName,
		Value:    ticket,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		// Strict still works: the panel host and this content host share a
		// registrable domain, so the iframe request is same-SITE even though it
		// is cross-ORIGIN. Same-site is not same-origin.
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(tabProxyTicketTTL.Seconds()),
	})
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

// writeHostGatePage renders a refusal INSIDE the iframe.
//
// The alternative was letting the wrapper page preflight the content host and
// render the card itself, which needs CORS on the content root - and a
// content root that answers with Access-Control-Allow-Origin would let
// panel-origin script read a tenant's container response. Rendering here keeps
// the content host CORS-free except for the mint endpoint.
//
// target="_top" because this is displayed in an iframe: without it the panel
// would load inside the frame it was supposed to replace.
func (h *ProxyHandler) writeHostGatePage(w http.ResponseWriter, status int, title, detail, linkURL string) {
	var link string
	if linkURL != "" {
		link = `<p><a target="_top" rel="noopener" href="` + html.EscapeString(linkURL) + `">Open the panel</a></p>`
	}
	body := `<!doctype html><meta charset="utf-8">` +
		`<meta name="viewport" content="width=device-width,initial-scale=1">` +
		`<title>` + html.EscapeString(title) + `</title>` +
		`<style>html{color-scheme:dark light}body{margin:0;min-height:100vh;display:flex;` +
		`align-items:center;justify-content:center;font:15px/1.5 system-ui,sans-serif;text-align:center}` +
		`div{padding:24px;max-width:34rem}h1{font-size:1.1rem;margin:0 0 .4rem}` +
		`p{margin:.4rem 0;opacity:.75}a{color:inherit}</style>` +
		`<div><h1>` + html.EscapeString(title) + `</h1><p>` + html.EscapeString(detail) + `</p>` + link + `</div>`

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

// TabProxyHostMux routes by Host: a tab-content host reaches ONLY the two
// handlers above, everything else reaches next.
//
// The dispatch is on the WHOLE request, not an added route, and that is the
// security-relevant part. "<label>.<suffix>/api/servers" must not reach the API
// and no panel route may answer there, because a tenant's container is one
// fetch away from anything this origin serves.
//
// Returns next unchanged when no suffix is configured, so a deployment without
// one carries no extra layer at all.
func TabProxyHostMux(state *AppState, ph *ProxyHandler, auth *AuthHandler, next http.Handler) http.Handler {
	suffix := state.TabProxyHostSuffix
	if suffix == "" || ph == nil {
		return next
	}
	mint := auth.AuthMiddleware(ph.HostMint)
	allowed := tabProxyMintOrigins(state.FrontendURL, suffix)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if TabProxyHostLabel(r.Host, suffix) == "" {
			next.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, tabProxyReservedPrefix) {
			if r.URL.Path != tabProxyMintPath {
				http.Error(w, "Not found", http.StatusNotFound)
				return
			}
			writeMintCORS(w, r, allowed)
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			mint(w, r)
			return
		}
		ph.HostContent(w, r)
	})
}

// tabProxyMintOrigins is the exact set of origins allowed to mint: the panel,
// and the wrapper page that serves share links. Both are ours.
func tabProxyMintOrigins(frontendURL, suffix string) []string {
	out := []string{}
	fe := strings.TrimRight(strings.TrimSpace(frontendURL), "/")
	if fe != "" {
		out = append(out, fe)
	}
	if wrapper := TabProxyWrapperOrigin(frontendURL, suffix); wrapper != "" && wrapper != fe {
		out = append(out, wrapper)
	}
	return out
}

// writeMintCORS echoes only an origin from the allow-list.
//
// Never a wildcard and never a reflected origin: this response sets a
// credentialed cookie, so Access-Control-Allow-Credentials with a reflected
// Origin would let any site on the internet mint a ticket in a visitor's
// browser.
func writeMintCORS(w http.ResponseWriter, r *http.Request, allowed []string) {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return
	}
	for _, a := range allowed {
		if a != "" && strings.EqualFold(a, origin) {
			w.Header().Set("Access-Control-Allow-Origin", a)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization")
			w.Header().Set("Vary", "Origin")
			return
		}
	}
}

// TabProxyContentOrigin is the browser-facing origin a tab is served on. Empty
// when the feature is not configured, which is what the panel keys "can this be
// shown at all" off.
func TabProxyContentOrigin(frontendURL, suffix, label string) string {
	if suffix == "" || label == "" {
		return ""
	}
	return tabProxyScheme(frontendURL) + "://" + label + "." + suffix
}

// TabProxyWrapperOrigin is the origin the share-wrapper page is served on: the
// bare suffix, same scheme as the panel. Empty when the feature is unconfigured.
//
// The wrapper is the panel bundle reached at "share.example.com" rather than
// "panel.example.com". It is deliberately NOT a content host - it holds no
// container output, so it may talk to Core normally, and it must be a different
// origin from the content it frames or the container could reach into it and
// rewrite the navbar it renders.
func TabProxyWrapperOrigin(frontendURL, suffix string) string {
	if suffix == "" {
		return ""
	}
	return tabProxyScheme(frontendURL) + "://" + suffix
}

// tabProxyScheme mirrors the panel's scheme, because a mixed-scheme pair cannot
// share a cookie the way this design needs.
func tabProxyScheme(frontendURL string) string {
	if strings.HasPrefix(strings.TrimSpace(frontendURL), "http://") {
		return "http"
	}
	return "https"
}

// shareResolveResponse is what the wrapper needs before it can render: where the
// content lives, and whether this visitor will be asked to sign in.
type shareResolveResponse struct {
	ContentOrigin string `json:"contentOrigin"`
	RequiresAuth  bool   `json:"requiresAuth"`
}

// ResolveShare turns a share token into the content host that serves it.
//
// Unauthenticated on purpose: the token IS the credential for a public link,
// and for a private one this answers only "you will need to sign in", never any
// of the tab's content. It is rate limited at the route because a token is
// guessable in principle - 16 base62 characters is not, in practice, but the
// limiter costs nothing and the endpoint is anonymous.
func (h *ProxyHandler) ResolveShare(w http.ResponseWriter, r *http.Request) {
	token := mux.Vars(r)["token"]
	if !h.state.FeatureFlags.IsTabProxyEnabled(r.Context()) || h.state.TabProxyHostSuffix == "" {
		sendJSONError(w, "Not found", http.StatusNotFound)
		return
	}
	tab, err := h.getTabByShareToken(token)
	if err != nil || tab == nil || !tab.Enabled || tab.Mode != "proxied" {
		sendJSONError(w, "Not found", http.StatusNotFound)
		return
	}
	if tab.Surface != "page" && tab.Surface != "both" {
		// Not published as a page: the tab exists, but no standalone link does.
		sendJSONError(w, "Not found", http.StatusNotFound)
		return
	}
	if !tab.ProxyHostLabel.Valid || tab.ProxyHostLabel.String == "" {
		sendJSONError(w, "Not found", http.StatusNotFound)
		return
	}
	if shareTokenExpired(tab.ShareExpires, time.Now()) {
		sendJSONError(w, "This link has expired", http.StatusGone)
		return
	}

	origin := TabProxyContentOrigin(h.state.FrontendURL, h.state.TabProxyHostSuffix, tab.ProxyHostLabel.String)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(shareResolveResponse{
		ContentOrigin: origin,
		RequiresAuth:  hostVisibility(tab) != "public" || !h.state.FeatureFlags.TabProxyAllowPublicLinks(r.Context()),
	})
}
