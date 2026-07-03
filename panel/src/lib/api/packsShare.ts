import { API_URL, getAuthHeader, handleResponse, handleError } from '@/lib/api/core';

export type ShareLinkKind = 'client-mrpack' | 'server-pack';

export interface ShareLink {
    id: number;
    buildId: number;
    kind: ShareLinkKind;
    token: string;
    expiresAt?: string;
    createdBy: string;
    createdAt: string;
    revoked: boolean;
}

export async function createShareLink(
    packId: number,
    buildId: number,
    kind: ShareLinkKind,
    expiresInDays?: number,
): Promise<{ success: boolean; link?: ShareLink; message?: string }> {
    try {
        const res = await fetch(`${API_URL}/packs/${packId}/builds/${buildId}/share-link`, {
            method: 'POST',
            headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
            body: JSON.stringify({ kind, expiresInDays: expiresInDays || 0 }),
        });
        return handleResponse(res) as any;
    } catch (err) { return handleError(err) as any; }
}

export async function listShareLinks(
    packId: number,
    buildId: number,
): Promise<{ success: boolean; links?: ShareLink[]; message?: string }> {
    try {
        const res = await fetch(`${API_URL}/packs/${packId}/builds/${buildId}/share-links`, {
            headers: getAuthHeader(),
        });
        return handleResponse(res) as any;
    } catch (err) { return handleError(err) as any; }
}

export async function revokeShareLink(
    packId: number,
    buildId: number,
    linkId: number,
): Promise<{ success: boolean; message?: string }> {
    try {
        const res = await fetch(`${API_URL}/packs/${packId}/builds/${buildId}/share-links/${linkId}`, {
            method: 'DELETE',
            headers: getAuthHeader(),
        });
        return handleResponse(res) as any;
    } catch (err) { return handleError(err) as any; }
}

// publicShareUrl builds the shareable download URL for a token. API_URL is the
// core's base (already ending in /api); when it is a relative path we prefix the
// current origin so the copied link is absolute and shareable.
export function publicShareUrl(token: string): string {
    const base = API_URL.startsWith('http')
        ? API_URL
        : (typeof window !== 'undefined' ? window.location.origin : '') + API_URL;
    return `${base}/share/${token}`;
}
