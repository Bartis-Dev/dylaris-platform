// Modrinth PAT management client. Plaintext is sent on Set but
// never returned; reads only expose connection state + username.

import { API_URL, getAuthHeader, handleResponse, handleError } from '@/lib/api/core';

export interface ModrinthPATStatus {
    success: boolean;
    connected: boolean;
    modrinthUsername?: string;
    lastValidatedAt?: string;
    message?: string;
}

export async function getModrinthPATStatus(): Promise<ModrinthPATStatus> {
    try {
        const res = await fetch(`${API_URL}/me/modrinth-pat`, { headers: getAuthHeader() });
        return handleResponse(res) as any;
    } catch (err) { return { ...(handleError(err) as any), connected: false }; }
}

export async function setModrinthPAT(token: string): Promise<ModrinthPATStatus> {
    try {
        const res = await fetch(`${API_URL}/me/modrinth-pat`, {
            method: 'PUT',
            headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
            body: JSON.stringify({ token }),
        });
        return handleResponse(res) as any;
    } catch (err) { return { ...(handleError(err) as any), connected: false }; }
}

export async function clearModrinthPAT(): Promise<{ success: boolean; message?: string }> {
    try {
        const res = await fetch(`${API_URL}/me/modrinth-pat`, {
            method: 'DELETE', headers: getAuthHeader(),
        });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}
