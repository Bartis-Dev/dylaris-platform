// URL builders for the custom-tab reverse proxy.
//
// A proxied tab is served at the ROOT of its own host ("<label>.<suffix>"),
// which Core reports per tab as `proxyOrigin`. Two things follow, and both are
// the reason it works at all:
//
//   - The container's own absolute paths resolve. Under the path prefix this
//     replaced, a "/js/app.js" was resolved against the origin root and missed
//     the prefix entirely - which is what BlueMap and Dynmap emit, so the two
//     most deployed map plugins were the two that could not be shown.
//   - A different hostname is a different ORIGIN, so a tenant's JavaScript
//     cannot reach the panel session token in the panel origin's localStorage.
//
// Auth is cookie-only on the content host: the ticket is minted by a separate
// call to that host (mintTabProxyAuth), and no builder here ever puts a session
// token or a ticket in a URL.

// tabContentSrc is the iframe src for a proxied tab. It FAILS CLOSED: without a
// proxyOrigin there is no host that may serve this content, and falling back to
// a same-origin path would put a tenant's container on the panel origin - the
// exact vector the per-tab host exists to close. The caller renders an
// explanation instead.
export function tabContentSrc(proxyOrigin: string): string | null {
    if (!proxyOrigin) return null;
    return proxyOrigin + '/';
}

// shareLinkUrl builds the shareable standalone-page URL for a share token.
// Absolute (with the current origin) in the browser so it is copy/paste
// shareable; a bare path during SSR/tests where window is unavailable.
export function shareLinkUrl(token: string): string {
    if (typeof window === 'undefined') return `/c/${token}`;
    return `${window.location.origin}/c/${token}`;
}
