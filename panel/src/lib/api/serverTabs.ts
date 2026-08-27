// per-server custom Tabs. CRUD client + types.

import { API_URL, getAuthHeader, handleResponse, handleError } from '@/lib/api/core';

export interface ServerTab {
    id: number;
    serverId: number;
    name: string;
    icon: string;
    url: string;
    position: number;
    enabled: boolean;
    openInPanel: boolean;
    mode: string;          // "direct" | "proxied"
    targetPort: number;    // proxied only
    targetPath: string;    // proxied only
    surface: string;       // "tab" | "page" | "both"
    visibility: string;    // "private" | "public"
    shareToken: string;    // "" when none
    shareExpiresAt: string | null;
    // proxyOrigin is the browser-facing origin this tab is served on, built by
    // Core from the tab's host label. "" for a direct tab and for any tab while
    // the feature is unconfigured - which is exactly when no iframe should be
    // rendered at all.
    proxyOrigin: string;
}

export interface ServerTabInput {
    name: string;
    icon?: string;
    url?: string;
    position?: number;
    enabled?: boolean;
    openInPanel?: boolean;
    mode?: string;
    targetPort?: number;
    targetPath?: string;
    surface?: string;
    visibility?: string;
    // Three states, which is why it is optional AND accepts "": omitted keeps
    // whatever is stored, "" clears the expiry, an RFC3339 instant sets it.
    // Core cannot read the difference out of JSON null, so "" is the clear.
    shareExpiresAt?: string;
}

export async function listServerTabs(serverId: number): Promise<ServerTab[]> {
    try {
        const res = await fetch(`${API_URL}/servers/${serverId}/tabs`, { headers: getAuthHeader() });
        const data = await handleResponse(res);
        return (data as any).tabs || [];
    } catch (err) { handleError(err); return []; }
}

export async function createServerTab(serverId: number, input: ServerTabInput): Promise<{ success: boolean; id?: number; message?: string }> {
    try {
        const res = await fetch(`${API_URL}/servers/${serverId}/tabs`, {
            method: 'POST',
            headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
            body: JSON.stringify(input),
        });
        return handleResponse(res) as any;
    } catch (err) { return handleError(err) as any; }
}

export async function updateServerTab(serverId: number, tabId: number, input: Partial<ServerTabInput>): Promise<{ success: boolean; message?: string }> {
    try {
        const res = await fetch(`${API_URL}/servers/${serverId}/tabs/${tabId}`, {
            method: 'PATCH',
            headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
            body: JSON.stringify(input),
        });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}

export async function deleteServerTab(serverId: number, tabId: number): Promise<{ success: boolean; message?: string }> {
    try {
        const res = await fetch(`${API_URL}/servers/${serverId}/tabs/${tabId}`, {
            method: 'DELETE',
            headers: getAuthHeader(),
        });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}

export async function rotateShareLink(serverId: number, tabId: number): Promise<{ success: boolean; shareToken?: string; message?: string }> {
    try {
        const res = await fetch(`${API_URL}/servers/${serverId}/tabs/${tabId}/share-link`, {
            method: 'POST',
            headers: getAuthHeader(),
        });
        return handleResponse(res) as any;
    } catch (err) { return handleError(err) as any; }
}

export async function revokeShareLink(serverId: number, tabId: number): Promise<{ success: boolean; message?: string }> {
    try {
        const res = await fetch(`${API_URL}/servers/${serverId}/tabs/${tabId}/share-link`, {
            method: 'DELETE',
            headers: getAuthHeader(),
        });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}

// mintTabProxyAuth authorizes one tab's iframe by setting the dyl_tabproxy
// ticket cookie ON THAT TAB'S OWN HOST.
//
// The mint has to run there and nowhere else: a cookie can only be set for the
// host that answered the request, and the content is served from
// "<label>.<suffix>", not from the panel or the API host.
//
// It is reached cross-origin with the session as a Bearer HEADER, which is only
// possible because the panel token lives in localStorage rather than in a
// cookie - the content origin cannot READ it, but the panel can SEND it. The
// response is 204 with a Set-Cookie and no body, so no credential ever lands in
// an iframe src. credentials:'include' is what makes the Set-Cookie stick;
// Core answers the matching Access-Control-Allow-Credentials for exactly the
// panel and wrapper origins.
export async function mintTabProxyAuth(proxyOrigin: string): Promise<{ success: boolean; status?: number; message?: string }> {
    if (!proxyOrigin) {
        return { success: false, message: 'This tab has no proxy host configured.' };
    }
    try {
        const res = await fetch(`${proxyOrigin}/__dyl/mint`, {
            headers: getAuthHeader(),
            credentials: 'include',
        });
        if (res.status === 204) return { success: true, status: 204 };
        try {
            const data = await res.json();
            return { success: false, status: res.status, message: data?.message || 'Unknown error' };
        } catch {
            return { success: false, status: res.status, message: `Failed to authorize tab proxy (HTTP ${res.status})` };
        }
    } catch (err) { return handleError(err); }
}

export interface ShareResolution {
    contentOrigin: string;
    requiresAuth: boolean;
}

// resolveShareLink turns a share token into the host that serves it. Anonymous:
// the token is the credential for a public link, and for a private one this
// says only "you will be asked to sign in", never a byte of the tab.
export async function resolveShareLink(token: string): Promise<{ success: boolean; status: number; data?: ShareResolution }> {
    try {
        const res = await fetch(`${API_URL}/tabproxy/${encodeURIComponent(token)}/resolve`);
        if (!res.ok) return { success: false, status: res.status };
        return { success: true, status: res.status, data: await res.json() };
    } catch {
        return { success: false, status: 0 };
    }
}
