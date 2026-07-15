// URL builders for the WS5 custom-tab reverse proxy. Same-origin paths (Core
// is reached on the panel origin as /api in production).
//
// Auth for both proxy surfaces is cookie-only (see
// core/handlers/tab_proxy.go): the in-dashboard proxy trusts ONLY the
// dyl_tabproxy ticket cookie minted by a separate GET .../proxy-auth call,
// and the standalone share page trusts the same cookie minted by
// GET /api/tabproxy/{token}/auth for a private link (a public link needs no
// cookie at all). Neither builder below ever puts a session token or ticket
// in the URL - the 24h session JWT must never be carried by an iframe src.

// When origin-isolation is active (spec B5), Core serves the proxy data plane on
// a dedicated same-host, different-PORT origin so a proxied container's JS runs
// there and can never read the panel token from the panel origin's localStorage.
// The panel then builds the iframe src as an ABSOLUTE URL on that origin
// (`origin` is Core's normalized scheme://host[:port], no trailing slash).
//
// The IN-DASHBOARD builder keeps a relative same-origin fallback by design: that
// surface is admin-only / self-host and is a documented best-effort limitation
// (a single operator viewing their own containers). The PUBLIC builder does NOT
// fall back - see tabProxyPageSrc. The mint / preflight fetches stay same-origin
// to the panel and are unaffected.
export function tabDashboardProxySrc(serverId: number, tabId: number, origin?: string): string {
    const path = `/api/servers/${serverId}/tabs/${tabId}/proxy/`;
    return origin ? origin + path : path;
}

// PUBLIC share builder. Origin isolation is MANDATORY for public shares (spec
// B5): a public /c/<token> page is served on the panel origin, so embedding the
// proxied iframe on a relative (same-origin) src would let the container's JS
// read the panel token from localStorage - the exact vector B5 closes. This
// builder therefore FAILS CLOSED: with no isolated `origin` it returns null so
// the caller refuses to render the iframe instead of silently falling back to
// same-origin. It never emits a relative path.
export function tabProxyPageSrc(token: string, origin?: string): string | null {
    if (!origin) return null;
    return origin + `/api/tabproxy/${encodeURIComponent(token)}/`;
}

// shareLinkUrl builds the shareable standalone-page URL for a share token.
// Absolute (with the current origin) in the browser so it is copy/paste
// shareable; a bare path during SSR/tests where window is unavailable.
export function shareLinkUrl(token: string): string {
    if (typeof window === 'undefined') return `/c/${token}`;
    return `${window.location.origin}/c/${token}`;
}
