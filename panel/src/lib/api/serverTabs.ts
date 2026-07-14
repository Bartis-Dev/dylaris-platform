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

// mintTabProxyAuth authorizes the in-dashboard proxy iframe for one tab: it
// calls Core's session-authed proxy-auth endpoint (core/handlers/tab_proxy.go
// MintProxyAuth), which on success stamps a short-lived, path-scoped
// dyl_tabproxy HttpOnly cookie (204 No Content, no body) instead of returning
// a token - the proxy iframe src therefore never carries a session
// credential. Unlike the rest of this file, this call needs credentials:
// 'include' so the Set-Cookie actually sticks on the panel origin; that only
// works same-origin (the production /api reverse-proxy layout), since Core's
// CORS config grants no Access-Control-Allow-Credentials for a cross-origin
// dev split (see main.go's allowedOrigin/corsObj - deliberately Bearer-only).
export async function mintTabProxyAuth(serverId: number, tabId: number): Promise<{ success: boolean; message?: string }> {
    try {
        const res = await fetch(`${API_URL}/servers/${serverId}/tabs/${tabId}/proxy-auth`, {
            headers: getAuthHeader(),
            credentials: 'include',
        });
        if (res.status === 204) return { success: true };
        // Every non-success response from this endpoint (feature off, no
        // access, expired/invalid session) carries a JSON {message} body -
        // only the 204 success path above has none.
        try {
            const data = await res.json();
            return { success: false, message: data?.message || 'Unknown error' };
        } catch {
            return { success: false, message: `Failed to authorize tab proxy (HTTP ${res.status})` };
        }
    } catch (err) { return handleError(err); }
}
