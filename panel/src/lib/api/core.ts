import { handleUnauthorized } from "./session";

// Resolve the Core API base URL. Order of precedence:
//   1. window.__DYLARIS_CONFIG__.apiUrl - runtime shim from /config.js. It is
//      NOT baked into the build, so a self-hoster can point a prebuilt image at
//      their own API (or leave it empty for same-origin) without rebuilding.
//      NEXT_PUBLIC_* is inlined at build time and can't be changed afterwards.
//   2. NEXT_PUBLIC_API_URL - build-time env, for people who build the panel
//      themselves and for local dev (.env).
//   3. Same-origin /api - the production default: the API is reached on the
//      same host that serves the panel (the usual reverse-proxy layout where
//      /api is routed to Core). In development we instead fall back to
//      localhost:25500 since the panel dev server and Core run on different
//      ports.
function resolveApiUrl(): string {
    const trim = (u: string) => u.replace(/\/+$/, "");

    if (typeof window !== "undefined") {
        const cfg = (window as unknown as { __DYLARIS_CONFIG__?: { apiUrl?: string } }).__DYLARIS_CONFIG__;
        if (cfg?.apiUrl && cfg.apiUrl.trim() !== "") return trim(cfg.apiUrl.trim());
    }

    if (process.env.NEXT_PUBLIC_API_URL && process.env.NEXT_PUBLIC_API_URL.trim() !== "") {
        return trim(process.env.NEXT_PUBLIC_API_URL);
    }

    if (typeof window !== "undefined") {
        if (process.env.NODE_ENV !== "production") return "http://localhost:25500/api";
        return trim(`${window.location.origin}/api`);
    }

    // SSR/build only — real fetches happen client-side where window exists.
    return "/api";
}

export const API_URL = resolveApiUrl();

// fetch has NO default timeout. A host that accepts the connection and then
// never answers leaves the promise pending forever - not rejected, not
// resolved - so a catch written for "hard transport failure" never runs.
//
// This is deliberately NOT applied inside fetchAPI: 134 call sites go through
// it, including createServer and the S3 connection test, where a long wait is
// legitimate. It is for the small reads that gate whether the panel renders at
// all, where a pending promise means a permanently dead page.
export const GATE_TIMEOUT_MS = 10000;

// Returns a plain object so callers can spread it into header objects:
//   { ...getAuthHeader(), 'Content-Type': 'application/json' }
// A Headers instance does not iterate as own properties — spreading it
// silently drops Authorization, which broke 2FA verify and similar flows.
export const getAuthHeader = (): Record<string, string> => {
  if (typeof window === 'undefined') return {};
  const token = localStorage.getItem("authToken") || localStorage.getItem("token");
  return token ? { Authorization: `Bearer ${token}` } : {};
};

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

    if (response.ok) return { success: true, ...(data ?? {}) };
    if (parsed) return { success: false, message: data?.message || 'Unknown error' };
    return { success: false, message: `Request failed (${response.status})` };
};

export const handleError = (err: any) => {
    console.error("API Error:", err);
    return { success: false, message: 'Connection failed' };
};