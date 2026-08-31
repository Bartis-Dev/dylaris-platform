import { handleUnauthorized } from "./session";

// Resolve the Core API base URL. Two answers, and the second is almost always
// the one:
//   1. window.__DYLARIS_CONFIG__.apiUrl - injected by Core into each page it
//      serves, from PANEL_API_URL. Empty unless an operator deliberately put the
//      API on a second hostname, which Core warns about at boot because a
//      host-only session cookie cannot follow it.
//   2. Same-origin /api - the shape of the software: Core serves the panel and
//      the API together, so there is one origin and nothing to configure. True
//      in development too, where `next dev` proxies /api to Core rather than
//      sending the browser to another port.
//
// There used to be a build-time NEXT_PUBLIC_API_URL between them. It is gone,
// and its removal is the point: baked into the bundle, it could not be corrected
// at runtime, it outranked the same-origin default, and every value it could
// hold other than the panel's own origin produces a panel that loads and then
// 401s. A stale one in a developer's .env is exactly how that gets discovered.
function resolveApiUrl(): string {
    const trim = (u: string) => u.replace(/\/+$/, "");

    if (typeof window !== "undefined") {
        const cfg = (window as unknown as { __DYLARIS_CONFIG__?: { apiUrl?: string } }).__DYLARIS_CONFIG__;
        if (cfg?.apiUrl && cfg.apiUrl.trim() !== "") return trim(cfg.apiUrl.trim());
    }

    if (typeof window !== "undefined") {
        // Same origin, in development too. `next dev` proxies /api to Core (see
        // next.config.ts) precisely so this stays true: the session is a
        // host-only cookie, and a dev panel pointed at localhost:25500 would be
        // a different origin, where the browser neither stores it nor sends it.
        return trim(`${window.location.origin}/api`);
    }

    // SSR/build only — real fetches happen client-side where window exists.
    return "/api";
}

export const API_URL = resolveApiUrl();

/**
 * Core's public origin, WITHOUT the trailing /api.
 *
 * This is what warp's ENROLL_URL and the link's CORE_URL want: both append
 * /api/warp/... themselves.
 *
 * It follows the API base rather than window.location.origin. Those are the same
 * host now - Core serves the panel - but they were not always, and a deploy kit
 * that enrolls against a host with no /api/warp/enroll on it is a failure the
 * operator debugs on the wrong machine. Following the API base is right in both
 * worlds; following the page's origin was only ever right in one.
 *
 * A relative base (the same-origin default, and what dev now uses) has no origin
 * to strip, so the page's own is the answer there - and correct, because it is
 * where /api was just fetched from.
 */
export function coreOrigin(): string {
    const base = resolveApiUrl().replace(/\/api\/?$/, "");
    if (base === "" || base.startsWith("/")) {
        return typeof window !== "undefined" ? window.location.origin : "";
    }
    return base;
}

// fetch has NO default timeout. A host that accepts the connection and then
// never answers leaves the promise pending forever - not rejected, not
// resolved - so a catch written for "hard transport failure" never runs.
//
// This is deliberately NOT applied inside fetchAPI: 134 call sites go through
// it, including createServer and the S3 connection test, where a long wait is
// legitimate. It is for the small reads that gate whether the panel renders at
// all, where a pending promise means a permanently dead page.
export const GATE_TIMEOUT_MS = 10000;

// Returns an EMPTY object, and keeps existing because ~300 call sites spread it
// into their headers.
//
// The session is an HttpOnly cookie now. fetch sends cookies on same-origin
// requests by default, and since Core serves the panel there is no other kind -
// so authentication happens without any call site doing anything, and the panel
// never holds the credential it used to read out of localStorage.
//
// Kept rather than removed for two reasons. Deleting it would be a ~300-file
// diff whose only content is removing a spread, and every one of those files
// would then need re-reviewing for a change that does nothing. And it is the
// hook if a future caller genuinely needs an explicit credential - one place to
// put it, rather than a new pattern invented at whichever call site needs it
// first.
//
// A Headers instance would not work here: it does not iterate as own
// properties, so spreading it silently drops everything. That cost a broken 2FA
// verify once, and the shape is kept for it.
export const getAuthHeader = (): Record<string, string> => ({});

// Error Handler Helper
export const handleResponse = async (response: Response) => {
    // Check for an expired session before touching the body: a 401 can carry a
    // non-JSON body (Go http.Error is text/plain), which would throw here.
    if (handleUnauthorized(response)) return { success: false, message: 'Session expired' };

    // A non-JSON body is not exclusive to 401. Go's http.Error writes
    // text/plain, an unmatched route gives the default text 404, and a proxy in
    // front of Core answers 502/504 with HTML - none of which parse. This used
    // to throw, the caller's catch turned it into handleError, and the operator
    // was told "Connection failed" about a server that had in fact answered.
    // That is the same trap as merging "the call failed" into "no results".
    let data: any = null;
    let parsed = true;
    try {
        data = await response.json();
    } catch {
        // Body was not JSON. Keep the status - it is the only thing left that
        // distinguishes a 404 from a 502.
        parsed = false;
    }

    // The status rides along on every path, not just the non-JSON one. Callers
    // that only look at `success` are unaffected, but a caller that has to tell
    // "you may not see this" (403) apart from "there is nothing here" (404)
    // cannot do it from the message alone - and rendering the first as the
    // second is how the Players tabs came to show an empty ban list to someone
    // who simply lacked files.read.
    if (response.ok) return { success: true, status: response.status, ...(data ?? {}) };
    if (parsed) return { success: false, status: response.status, message: data?.message || 'Unknown error' };
    return { success: false, status: response.status, message: `Request failed (${response.status})` };
};

export const handleError = (err: any) => {
    console.error("API Error:", err);
    return { success: false, message: 'Connection failed' };
};