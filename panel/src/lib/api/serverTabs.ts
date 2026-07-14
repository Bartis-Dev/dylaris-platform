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
