// panel client for owner-defined server roles (per-server capability
// bundles), used by the owner "Access" tab.

import { API_URL, getAuthHeader, handleResponse, handleError } from '@/lib/api/core';

export interface ServerRole {
    id: number;
    name: string;
    capabilities: string[];
}

export async function listServerRoles(): Promise<{ success: boolean; roles?: ServerRole[]; message?: string }> {
    try {
        const res = await fetch(`${API_URL}/server-roles`, { headers: getAuthHeader() });
        return handleResponse(res) as any;
    } catch (err) { return handleError(err) as any; }
}

export async function createServerRole(name: string, capabilities: string[]): Promise<{ success: boolean; role?: ServerRole; message?: string }> {
    try {
        const res = await fetch(`${API_URL}/server-roles`, {
            method: 'POST',
            headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
            body: JSON.stringify({ name, capabilities }),
        });
        return handleResponse(res) as any;
    } catch (err) { return handleError(err) as any; }
}

export async function updateServerRole(id: number, name: string, capabilities: string[]): Promise<{ success: boolean; role?: ServerRole; message?: string }> {
    try {
        const res = await fetch(`${API_URL}/server-roles/${id}`, {
            method: 'PATCH',
            headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
            body: JSON.stringify({ name, capabilities }),
        });
        return handleResponse(res) as any;
    } catch (err) { return handleError(err) as any; }
}

export async function deleteServerRole(id: number): Promise<{ success: boolean; message?: string }> {
    try {
        const res = await fetch(`${API_URL}/server-roles/${id}`, {
            method: 'DELETE',
            headers: getAuthHeader(),
        });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}
