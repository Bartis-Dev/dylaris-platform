package panelfs

import (
	"net/url"
	"strings"
)

// The panel's Content-Security-Policy, ported from panel/src/lib/csp.ts when the
// panel stopped having a server of its own to write it.
//
// script-src is nonce-strict ('nonce-<N>' + 'strict-dynamic', no
// 'unsafe-inline'); style-src stays pragmatic because Next and Tailwind emit
// inline <style> the browser would otherwise block, and script - not style - is
// the high-value injection target. cdnjs is kept as a belt-and-braces host
// source for jszip, which trusted code injects at runtime: CSP3 browsers ignore
// it under 'strict-dynamic', older ones honour it.
//
// Moving it here fixed a duplication rather than creating one. TAB_PROXY_HOST_SUFFIX
// had to be set on the panel container as well as on Core, purely because the
// panel wrote this header - and nothing compared the two values. When they
// disagreed, connect-src was missing the tab hosts, every proxied tab failed to
// authorize, and nothing anywhere said why. There is now one value.

type cspConfig struct {
	// apiOrigin is set only when an operator still serves the API on a separate
	// hostname (PANEL_API_URL). The normal deployment is same-origin, where
	// 'self' already covers it and this stays empty.
	apiOrigin string
	tabSuffix string
}

func (c cspConfig) build(nonce string) string {
	// No 'unsafe-eval' branch, and no dev mode at all. The panel's dev server
	// serves its own pages and never reaches this handler, so a Core is always
	// serving a production build - a dev switch here could only ever be wrong in
	// the direction that loosens the policy in production.
	script := []string{"'self'", "'nonce-" + nonce + "'", "'strict-dynamic'", "https://cdnjs.cloudflare.com"}

	connect := []string{"'self'"}
	if c.apiOrigin != "" {
		connect = append(connect, c.apiOrigin)
	}
	if c.tabSuffix != "" {
		// Written WITHOUT a scheme on purpose: a bare host-source takes the
		// document's own scheme (a secure upgrade is still allowed), so one
		// entry is correct for the https panel in production and the http one
		// in development. Hardcoding https would break local development
		// silently.
		connect = append(connect, "*."+c.tabSuffix)
	}

	// img-src needs the API origin for the same reason connect-src does: the
	// server-icon preview is an <img> pointing at Core's /files/download, so on
	// a split-origin deployment the browser refuses it and the icon simply never
	// appears. The vendor hosts are fixed third parties, not operator config.
	img := []string{"'self'", "data:", "blob:"}
	if c.apiOrigin != "" {
		img = append(img, c.apiOrigin)
	}
	img = append(img, "https://cravatar.eu", "https://cdn.modrinth.com")

	return strings.Join([]string{
		"default-src 'self'",
		"script-src " + strings.Join(script, " "),
		"style-src 'self' 'unsafe-inline'",
		"img-src " + strings.Join(img, " "),
		"font-src 'self'",
		"connect-src " + strings.Join(connect, " "),
		"object-src 'none'",
		"base-uri 'none'",
		"form-action 'self'",
		// Operator custom-tab and module iframes are already sandboxed; keeping
		// this broad is what lets them load at all.
		"frame-src https: http:",
		"frame-ancestors 'none'",
	}, "; ")
}

// originOf reduces a configured API URL to a bare origin, so that a value like
// "https://api.example.com/api" becomes "https://api.example.com". Empty or
// unparseable yields empty, and connect-src stays on 'self'.
func originOf(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// normalizeTabSuffix cleans TAB_PROXY_HOST_SUFFIX into the bare DNS suffix, the
// same way Core's config does and for the same reason: an operator copying the
// value out of their reverse-proxy config brings a scheme, a port or a trailing
// dot along, and any of those would make the CSP source match nothing while
// reading correct in the compose file.
//
// A single label is refused rather than repaired: "*.localhost" as a connect-src
// would be a wide, silent allowance.
func normalizeTabSuffix(raw string) string {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" {
		return ""
	}
	if i := strings.Index(v, "://"); i >= 0 {
		v = v[i+3:]
	}
	if i := strings.IndexByte(v, '/'); i >= 0 {
		v = v[:i]
	}
	if i := strings.IndexByte(v, ':'); i >= 0 {
		v = v[:i]
	}
	v = strings.Trim(v, ".")
	if !strings.Contains(v, ".") {
		return ""
	}
	return v
}

// renderConfigJS is the runtime API-URL shim the panel bundle reads before it
// resolves anything. A shell entrypoint used to write it into the image on
// container start; Core holds the value already, so it is rendered here.
func renderConfigJS(apiURL string) []byte {
	return []byte("window.__DYLARIS_CONFIG__ = {\n  apiUrl: " + jsString(apiURL) + "\n};\n")
}

// escByte is the reverse solidus, written as a number.
//
// Every character this file has to emit escaped is emitted as \uXXXX through
// writeJSEscape, so the file itself contains no escape sequence at all. That is
// deliberate: an escaping routine whose own source can be mangled by a tool
// that rewrites escape sequences is a routine that fails silently, and the
// failure mode here is a config shim the browser refuses to parse.
const escByte = 0x5c

func writeJSEscape(b *strings.Builder, r rune) {
	const hexDigits = "0123456789abcdef"
	b.WriteByte(escByte)
	b.WriteByte('u')
	b.WriteByte(hexDigits[(r>>12)&0xf])
	b.WriteByte(hexDigits[(r>>8)&0xf])
	b.WriteByte(hexDigits[(r>>4)&0xf])
	b.WriteByte(hexDigits[r&0xf])
}

// jsString escapes an operator-supplied value for a double-quoted JS literal.
//
// Not json.Marshal, and the difference is not pedantry: JSON permits U+2028 and
// U+2029 raw inside a string where JavaScript does not, so a marshalled value
// carrying either yields a syntax error in the shim - and a shim that fails to
// parse leaves window.__DYLARIS_CONFIG__ unset, which the panel reads as
// "same-origin" rather than as an error.
//
// "<" is escaped too, so a "</script>" inside the value cannot close the tag if
// this is ever inlined rather than served as its own file.
func jsString(s string) string {
	var b strings.Builder
	b.WriteByte(0x22) // "
	for _, r := range s {
		switch r {
		case 0x22, escByte, 0x0a, 0x0d, 0x3c, 0x2028, 0x2029:
			writeJSEscape(&b, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte(0x22)
	return b.String()
}
