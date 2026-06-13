// publish + collaborators client.

import { API_URL, getAuthHeader, handleResponse, handleError } from '@/lib/api/core';

export interface PublishResult {
    success: boolean;
    modrinthProjectId?: string;
    modrinthVersionId?: string;
    message?: string;
}

export interface Collaborator {
    user?: { id?: string; username?: string };
    role?: string;
    accepted?: boolean;
}

export async function publishModpackVersion(
    modpackId: number,
    versionId: number,
    payload: { promoteTo?: 'beta' | 'release' } = {},
): Promise<PublishResult> {
    try {
        const res = await fetch(`${API_URL}/modpacks/${modpackId}/versions/${versionId}/publish`, {
            method: 'POST',
            headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
            body: JSON.stringify(payload),
        });
        return handleResponse(res) as any;
    } catch (err) { return handleError(err) as any; }
}

export async function listCollaborators(modpackId: number): Promise<Collaborator[]> {
    try {
        const res = await fetch(`${API_URL}/modpacks/${modpackId}/collaborators`, { headers: getAuthHeader() });
        const data = await handleResponse(res);
        return (data as any).collaborators || [];
    } catch (err) { handleError(err); return []; }
}

export async function addCollaborator(modpackId: number, username: string): Promise<{ success: boolean; message?: string }> {
    try {
        const res = await fetch(`${API_URL}/modpacks/${modpackId}/collaborators`, {
            method: 'POST',
            headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
            body: JSON.stringify({ username }),
        });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}

export async function removeCollaborator(modpackId: number, modrinthUserId: string): Promise<{ success: boolean; message?: string }> {
    try {
        const res = await fetch(`${API_URL}/modpacks/${modpackId}/collaborators/${encodeURIComponent(modrinthUserId)}`, {
            method: 'DELETE',
            headers: getAuthHeader(),
        });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}
