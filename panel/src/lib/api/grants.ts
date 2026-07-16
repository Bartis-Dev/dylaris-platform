// panel client for per-friend delegation grants (account-wide or
// per-server), used by the owner "Access" tab.

import { API_URL, getAuthHeader, handleResponse, handleError } from '@/lib/api/core';

export interface Grant {
    username: string;
    serverId: number | null;
    serverName: string;
    serverRoleId: number | null;
    serverRoleName: string;
    grantCaps: string[];
    denyCaps: string[];
    inherit: boolean;
    accountWide: boolean;
}

export interface AssignGrantInput {
    username: string;
    serverId?: number | null;
    serverRoleId?: number | null;
    grantCaps: string[];
    denyCaps: string[];
    inherit: boolean;
}

export async function listGrants(): Promise<{ success: boolean; grants?: Grant[]; message?: string }> {
    try {
        const res = await fetch(`${API_URL}/grants`, { headers: getAuthHeader() });
        return handleResponse(res) as any;
    } catch (err) { return handleError(err) as any; }
}

export async function assignGrant(input: AssignGrantInput): Promise<{ success: boolean; message?: string }> {
    try {
        const res = await fetch(`${API_URL}/grants`, {
            method: 'POST',
            headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
            body: JSON.stringify(input),
        });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}

export async function revokeGrant(username: string, serverId: number | null): Promise<{ success: boolean; message?: string }> {
    try {
        const res = await fetch(`${API_URL}/grants`, {
            method: 'DELETE',
            headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
            body: JSON.stringify({ username, serverId }),
        });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}
