package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
)

// beamPanelCSP is the Content-Security-Policy beam sets on proxied Panel
// HTML. The Panel ships no CSP of its own, so this is the only policy in
// force. It is pragmatic, not nonce-strict: 'unsafe-inline' on script-src
// is a deliberate concession so Next.js framework inline scripts (RSC
// flight + hydration) run without a per-request nonce, which would break
// statically-prerendered Panel pages. The RCE class is closed natively in
// the vendored Wails dispatcher (BC3 Part A), so this CSP is
// defense-in-depth: the high-value directives (connect-src, object-src,
// base-uri, form-action, frame-ancestors) contain the residual XSS blast
// radius and need no nonce. Non-obvious allowances: cdnjs.cloudflare.com
// (jszip, loaded at runtime for client-side upload zipping); cravatar.eu +
// cdn.modrinth.com (player avatars, mod icons); frame-src stays broad so
// operator custom-tab / module iframes (already sandboxed) keep loading;
// frame-ancestors 'self' replaces the removed X-Frame-Options. connect-src
// includes https://api.dylaris.com to cover a Panel build that targets an
// absolute API origin; same-origin /api is already covered by 'self'.
const beamPanelCSP = "default-src 'self'; " +
	"script-src 'self' 'unsafe-inline' https://cdnjs.cloudflare.com; " +
	"style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data: blob: https://cravatar.eu https://cdn.modrinth.com; " +
	"font-src 'self'; " +
	"connect-src 'self' https://api.dylaris.com; " +
	"object-src 'none'; " +
	"base-uri 'none'; " +
	"form-action 'self'; " +
	"frame-src https: http:; " +
	"frame-ancestors 'self'"

// wailsRuntimeInjection is spliced into proxied Panel HTML so the
// window.go / window.runtime native bridge exists on the reverse-proxied
// Panel. Wails' own AssetServer only auto-injects the runtime into HTML it
// serves directly; it does NOT inject through httputil.ReverseProxy, so
// the proxied Panel needs these two tags added manually. They are plain
// classic scripts served same-origin from /wails/*, allowed by the beam
// CSP's script-src 'self'.
//
// The former inline BrowserOpenURL scheme-wrapper (and the
// Object.defineProperty freezes of window.runtime / window) were removed
// in BC3: they were a same-origin JS mitigation any page script could
// bypass via window.WailsInvoke("BO:..."). The real boundary now lives
// natively in the vendored Wails dispatcher (processBrowserMessage), which
// no page JS can reach around.
const wailsRuntimeInjection = `<script src="/wails/runtime.js"></script>` +
	`<script src="/wails/ipc.js"></script>`

// injectWailsRuntime splices the runtime scripts into an HTML document,
// preferring just before </head>, then before <body>, else prepended.
func injectWailsRuntime(html []byte) []byte {
	s := string(html)
	for _, anchor := range []string{"</head>", "<body"} {
		if i := strings.Index(strings.ToLower(s), anchor); i >= 0 {
			return []byte(s[:i] + wailsRuntimeInjection + s[i:])
		}
	}
	return append([]byte(wailsRuntimeInjection), html...)
}

// beamSettingsRoute is the reserved path that serves the embedded
// Panel-URL settings page. Everything NOT under this prefix (and not
// under /wails/) is reverse-proxied to the configured Panel.
const beamSettingsRoute = "/__beam/"

// resolvePanelTarget parses the configured Panel URL into a proxy
// target. Read straight from the persisted settings on every call -
// it's a tiny local file, and keeping no cached state means a URL
// change via the Settings page takes effect on the very next request
// with zero locking.
func (a *App) resolvePanelTarget() *url.URL {
	u, err := url.Parse(a.GetPanelURL())
	if err != nil || u.Scheme == "" || u.Host == "" {
		u, _ = url.Parse("https://panel.dylaris.com")
	}
	return u
}

// newPanelMiddleware builds the AssetServer middleware that turns the
// Beam window into a transparent shell around the remote Panel.
//
// Why a proxy instead of just navigating the webview to the Panel URL:
// Wails only injects its runtime (the /wails/ipc.js + /wails/runtime.js
// scripts that create window.go / window.runtime) into HTML that the
// asset server itself serves. A cross-origin navigation to the Panel
// leaves that origin behind, so window.go is undefined there and the
// whole native bridge - gRPC file ops, native chunked upload, native
// download dialogs - silently falls back to plain HTTP. Proxying the
// Panel THROUGH the asset server keeps the webview on the wails://
// origin, so the runtime gets injected and window.go works.
//
// Routing:
//
//	/wails/*    → next (Wails runtime - must never be proxied)
//	/__beam/*   → embedded settings page (served from frontend/dist via
//	              serveEmbedded; Wails' own asset server auto-injects the
//	              runtime for the "/index.html" entry point)
//	everything  → reverse-proxied to the resolved Panel URL
func newPanelMiddleware(app *App, next http.Handler) http.Handler {
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			target := app.resolvePanelTarget()
			pr.SetURL(target)
			// SetURL leaves Out.Host as the inbound (wails.localhost)
			// host; the Panel's edge (NPM / Cloudflare) routes on Host,
			// so it has to be the real one.
			pr.Out.Host = target.Host
			// Force an uncompressed response. Wails injects its runtime
			// <script> tags into text/html bodies - it cannot do that
			// through a gzip stream. Dropping Accept-Encoding lets the
			// Go transport negotiate + transparently undo compression,
			// so the body reaching the injector is always plain HTML.
			pr.Out.Header.Del("Accept-Encoding")
		},
		ModifyResponse: func(resp *http.Response) error {
			// The Panel ships no security headers of its own, so beam is
			// the only place a CSP / framing policy is applied. Drop the
			// report-only variant and X-Frame-Options (the latter is
			// superseded by the CSP's frame-ancestors); the enforced
			// Content-Security-Policy is SET on proxied HTML below.
			resp.Header.Del("Content-Security-Policy-Report-Only")
			resp.Header.Del("X-Frame-Options")
			// A redirect whose Location points back at the Panel host
			// would navigate the webview off the wails:// origin and
			// kill the runtime. Rewrite it to an origin-relative path
			// so the redirect stays inside the proxy.
			if loc := resp.Header.Get("Location"); loc != "" {
				if u, err := url.Parse(loc); err == nil && u.Host != "" {
					if t := app.resolvePanelTarget(); u.Host == t.Host {
						u.Scheme, u.Host = "", ""
						resp.Header.Set("Location", u.String())
					}
				}
			}
			// Splice the Wails runtime into HTML documents so window.go
			// exists on the Panel. Accept-Encoding was stripped on the
			// way out, so the body here is always plain text.
			if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
				resp.Header.Set("Content-Security-Policy", beamPanelCSP)
				body, err := io.ReadAll(resp.Body)
				resp.Body.Close()
				if err != nil {
					return err
				}
				body = injectWailsRuntime(body)
				resp.Body = io.NopCloser(bytes.NewReader(body))
				resp.ContentLength = int64(len(body))
				resp.Header.Set("Content-Length", strconv.Itoa(len(body)))
				resp.Header.Del("Content-Encoding")
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusBadGateway)
			page := strings.ReplaceAll(panelUnreachableHTML, "{{ERROR}}", err.Error())
			_, _ = w.Write([]byte(page))
		},
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/wails/"):
			// Wails runtime + IPC bridge - always served locally.
			next.ServeHTTP(w, r)
		case r.URL.Path == "/__beam":
			// Normalize to the trailing-slash form so the settings
			// page's relative ./assets/* references resolve correctly.
			http.Redirect(w, r, beamSettingsRoute, http.StatusFound)
		case r.URL.Path == beamSettingsRoute:
			// The settings-page HTML entry point. Served through the Wails
			// asset server (which auto-injects /wails/runtime.js +
			// /wails/ipc.js for any path ending in "/index.html"), then
			// post-processed to splice the per-run shell-capability token
			// and mark the page unframeable so a compromised same-origin
			// Panel cannot iframe it and read the token. Wails' raw
			// runtime is safe now that the native dispatcher enforces the
			// URL-open boundary (BC3 Part A).
			serveBeamIndex(app, next, w, r)
		case strings.HasPrefix(r.URL.Path, beamSettingsRoute+"assets/"):
			// Static JS/CSS assets - never HTML documents, nothing to
			// inject, and Wails' own auto-injection only fires for
			// paths ending in "/" or "/index.html" so it doesn't touch
			// these either. Plain passthrough.
			serveEmbedded(next, w, r, strings.TrimPrefix(r.URL.Path, "/__beam"))
		default:
			if app.gateIsBlocked() {
				// Force-update gate active: keep the webview on the app-shell
				// mandatory screen. Redirect every Panel-bound request to
				// /__beam/ so a reload or deep-link can't slip past it.
				// /wails/ and /__beam/* are handled in the cases above, so this
				// never loops.
				http.Redirect(w, r, beamSettingsRoute, http.StatusFound)
				return
			}
			proxy.ServeHTTP(w, r)
		}
	})
}

// serveEmbedded rewrites the request path and hands it to the default
// asset handler so the embedded frontend/dist build is served.
func serveEmbedded(next http.Handler, w http.ResponseWriter, r *http.Request, path string) {
	r2 := r.Clone(r.Context())
	r2.URL.Path = path
	next.ServeHTTP(w, r2)
}

// captureResponse is a buffering http.ResponseWriter used to post-process the
// Wails asset server's /__beam/ index.html (splice the shell token, add framing
// headers) before writing it to the real client. It records status, headers,
// and body without touching the underlying connection.
type captureResponse struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func newCaptureResponse() *captureResponse {
	return &captureResponse{header: http.Header{}, status: http.StatusOK}
}

func (c *captureResponse) Header() http.Header         { return c.header }
func (c *captureResponse) WriteHeader(status int)      { c.status = status }
func (c *captureResponse) Write(b []byte) (int, error) { return c.body.Write(b) }

// injectShellToken splices a <script> that publishes the per-run shell token as
// window.__beamShellToken just before </head> (same anchor strategy as
// injectWailsRuntime). The token is emitted with %q so it is always a safe,
// Go-quoted string literal in the HTML; the token is hex from crypto/rand, so
// %q never needs to escape anything, but %q keeps it robust regardless.
func injectShellToken(html []byte, token string) []byte {
	tag := fmt.Sprintf(`<script>window.__beamShellToken=%q</script>`, token)
	s := string(html)
	for _, anchor := range []string{"</head>", "<body"} {
		if i := strings.Index(strings.ToLower(s), anchor); i >= 0 {
			return []byte(s[:i] + tag + s[i:])
		}
	}
	return append([]byte(tag), html...)
}

// serveBeamIndex serves the app-shell index.html through the Wails asset server
// (which auto-injects /wails/*), then splices the per-run shell-capability token
// into the document and marks it unframeable. The token reaches ONLY this
// first-party page - never the proxied Panel - so a compromised Panel cannot
// read it, and X-Frame-Options: DENY + CSP frame-ancestors 'none' stop a
// same-origin iframe from stealing it via frames[0].__beamShellToken.
func serveBeamIndex(app *App, next http.Handler, w http.ResponseWriter, r *http.Request) {
	cap := newCaptureResponse()
	serveEmbedded(next, cap, r, "/index.html")

	body := cap.body.Bytes()
	if strings.HasPrefix(cap.header.Get("Content-Type"), "text/html") {
		body = injectShellToken(body, app.shellToken)
	}

	// Copy the asset server's headers through, then override framing + length.
	for k, vals := range cap.header {
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy", "frame-ancestors 'none'")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.Header().Del("Content-Encoding")

	status := cap.status
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// panelUnreachableHTML is shown when the Panel can't be reached (DNS
// failure, connection refused, TLS error, …). It carries a single
// affordance: jump to the Settings page to fix the URL. The {{ERROR}}
// token is replaced with the underlying transport error.
const panelUnreachableHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<title>Dylaris Beam - Panel unreachable</title>
<style>
  html,body{height:100%;margin:0}
  body{display:flex;align-items:center;justify-content:center;
       background:#080909;color:#D8DDE6;
       font-family:system-ui,-apple-system,sans-serif}
  .box{max-width:460px;text-align:center;padding:32px}
  h1{font-size:18px;font-weight:600;margin:0 0 8px}
  p{font-size:13px;color:#8A95A8;line-height:1.6;margin:0 0 20px}
  code{font-family:ui-monospace,monospace;font-size:11px;color:#5A6272;
       word-break:break-all}
  a{display:inline-block;background:#7048C8;color:#fff;text-decoration:none;
    font-size:13px;font-weight:500;padding:9px 16px;border-radius:8px}
</style>
</head>
<body>
  <div class="box">
    <h1>Can't reach the Panel</h1>
    <p>The Beam app couldn't connect to the configured Panel.<br>
       <code>{{ERROR}}</code></p>
    <a href="/__beam/">Change Panel URL</a>
  </div>
</body>
</html>`
