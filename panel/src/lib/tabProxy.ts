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

export function tabDashboardProxySrc(serverId: number, tabId: number): string {
    return `/api/servers/${serverId}/tabs/${tabId}/proxy/`;
}

export function tabProxyPageSrc(token: string): string {
    return `/api/tabproxy/${encodeURIComponent(token)}/`;
}

// shareLinkUrl builds the shareable standalone-page URL for a share token.
// Absolute (with the current origin) in the browser so it is copy/paste
// shareable; a bare path during SSR/tests where window is unavailable.
export function shareLinkUrl(token: string): string {
    if (typeof window === 'undefined') return `/c/${token}`;
    return `${window.location.origin}/c/${token}`;
}
