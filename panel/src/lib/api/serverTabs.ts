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
    // subServerName pins a proxied tab to one sub-server; "" is every
    // sub-server. The tab addresses a PORT, and the port belongs to whichever
    // sub-server is started, so an unpinned tab follows the container and a
    // pinned one stays hidden while a different world is running.
    subServerName: string;
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
    subServerName?: string;
    targetPort?: number;
    targetPath?: string;
    surface?: string;
    visibility?: string;
    // Three states, which is why it is optional AND accepts "": omitted keeps
    // whatever is stored, "" clears the expiry, an RFC3339 instant sets it.
    // Core cannot read the difference out of JSON null, so "" is the clear.
    shareExpiresAt?: string;
}

// RAISES on a failed request rather than returning no tabs.
//
// An empty list is a statement - "this server has no custom tabs" - and both
// callers act on it. The shell removes them from the navigation, and the tab
// page resolves the id against the list and renders "tab not found", which
// tells someone their tab is gone when a proxy answered 502 for one request.
export async function listServerTabs(serverId: number): Promise<ServerTab[]> {
    const res = await fetch(`${API_URL}/servers/${serverId}/tabs`, { headers: getAuthHeader() });
    const data = await handleResponse(res);
    if (!(data as any)?.success) {
        throw new Error((data as any)?.message || 'Could not load the tabs for this server.');
    }
    return (data as any).tabs || [];
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

// rotateShareLink issues a new link for a tab. With no slug the server picks an
// unguessable one; with a slug the user picks a readable one and trades that
// unguessability for it - which only matters for a PUBLIC link, since a private
// one is gated by the ticket and not by the slug.
export async function rotateShareLink(serverId: number, tabId: number, slug?: string): Promise<{ success: boolean; shareToken?: string; message?: string }> {
    try {
        const res = await fetch(`${API_URL}/servers/${serverId}/tabs/${tabId}/share-link`, {
            method: 'POST',
            headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
            body: JSON.stringify({ slug: slug || '' }),
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
// ticket cookie ON THAT TAB'S OWN HOST. Two requests, and the split is not
// incidental.
//
// The ticket has to be DECIDED where the session is, and stored where the
// content is, and those are different origins. The session is an HttpOnly,
// host-only cookie on the panel's host: a cross-origin fetch to a tab host
// carries neither it nor a header any script could build, so that end cannot
// identify a caller at all. The cookie, conversely, can only be set by the host
// that answered the request - the content host and nobody else.
//
// So: mint on our origin, present the result on theirs. The ticket is scoped to
// this one tab and expires in minutes; the response there is 204 with a
// Set-Cookie and no body, so no credential ever lands in an iframe src.
// credentials:'include' is what makes the Set-Cookie stick, and Core answers the
// matching Access-Control-Allow-Credentials for exactly the panel and wrapper
// origins.
export async function mintTabProxyAuth(tab: Pick<ServerTab, 'id' | 'serverId' | 'proxyOrigin'>): Promise<{ success: boolean; status?: number; message?: string }> {
    const proxyOrigin = tab.proxyOrigin;
    if (!proxyOrigin) {
        return { success: false, message: 'This tab has no proxy host configured.' };
    }
    let ticket = '';
    try {
        const res = await fetch(`${API_URL}/servers/${tab.serverId}/tabs/${tab.id}/proxy-ticket`, {
            method: 'POST',
            headers: getAuthHeader(),
        });
        const data = await res.json().catch(() => null);
        if (!res.ok || !data?.ticket) {
            return { success: false, status: res.status, message: data?.message || 'Not allowed to open this tab.' };
        }
        ticket = data.ticket;
    } catch (err) {
        console.error('tab proxy ticket failed:', err);
        return { success: false, message: 'Could not reach Core to authorize this tab.' };
    }
    try {
        const res = await fetch(`${proxyOrigin}/__dyl/mint`, {
            headers: { Authorization: `Bearer ${ticket}` },
            credentials: 'include',
        });
        if (res.status === 204) return { success: true, status: 204 };
        try {
            const data = await res.json();
            return { success: false, status: res.status, message: data?.message || 'Unknown error' };
        } catch {
            return { success: false, status: res.status, message: `Failed to authorize tab proxy (HTTP ${res.status})` };
        }
    } catch (err) {
        // A THROW here means the request never reached Core - and on this call
        // that has one overwhelmingly likely cause. The mint is the only
        // cross-origin fetch the panel makes, to a host the panel itself
        // computed, so the browser refuses it when the panel's CSP does not
        // list that host - which happens whenever the panel's
        // TAB_PROXY_HOST_SUFFIX disagrees with Core's.
        //
        // handleError's 'Connection failed' is true and useless here: it points
        // at Core, which is up and was never asked. Same trap handleResponse
        // documents for a non-JSON body. Name the host and both causes instead;
        // fetch cannot tell a CSP block from a DNS/TLS failure, so neither do we.
        console.error('tab proxy mint failed:', err);
        let host = proxyOrigin;
        try { host = new URL(proxyOrigin).host; } catch { /* keep the raw value */ }
        return {
            success: false,
            message: `The browser could not reach ${host}. Either the panel's TAB_PROXY_HOST_SUFFIX does not match Core's, or DNS and TLS for that host are not set up yet.`,
        };
    }
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
