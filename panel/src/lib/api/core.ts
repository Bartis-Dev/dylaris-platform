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
    const data = await response.json();
    if (response.ok) return { success: true, ...data };
    return { success: false, message: data.message || 'Unknown error' };
};

export const handleError = (err: any) => {
    console.error("API Error:", err);
    return { success: false, message: 'Connection failed' };
};