import { API_URL, getAuthHeader, handleResponse, handleError } from '@/lib/api/core';

export interface PublishResult {
    success: boolean;
    modrinthProjectId?: string;
    modrinthVersionId?: string;
    message?: string;
    warnings?: string[];
}

export async function publishModrinth(
    packId: number,
    buildId: number,
    input: { channel: 'beta' | 'release'; ackNonModrinth?: boolean },
): Promise<PublishResult> {
    try {
        const res = await fetch(`${API_URL}/packs/${packId}/builds/${buildId}/publish`, {
            method: 'POST',
            headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
            body: JSON.stringify(input),
        });
        // 409 = non-Modrinth-content warning gate; surface it, not an error.
        const data = await res.json();
        return { success: res.ok, ...data } as PublishResult;
    } catch (err) { return handleError(err) as PublishResult; }
}

export async function replaceWithModrinth(
    packId: number,
    buildId: number,
    modversionId: number,
    versionId: string,
): Promise<{ success: boolean; message?: string }> {
    try {
        const res = await fetch(`${API_URL}/packs/${packId}/builds/${buildId}/content/${modversionId}/replace-modrinth`, {
            method: 'POST',
            headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
            body: JSON.stringify({ versionId }),
        });
        return handleResponse(res) as any;
    } catch (err) { return handleError(err) as any; }
}

export async function updateMods(
    packId: number,
    buildId: number,
    opts: { modversionId?: number; versionId?: string; all?: boolean },
): Promise<{
    success: boolean;
    upgraded?: number;
    results?: { modversionId: number; error?: string }[];
    message?: string;
}> {
    const res = await fetch(`${API_URL}/packs/${packId}/builds/${buildId}/update-mods`, {
        method: 'POST',
        headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
        body: JSON.stringify(opts),
    });
    return handleResponse(res) as any;
}

// mrpackDownloadUrl builds the authenticated export URL; the browser fetches it
// with the auth header via an anchor+fetch blob in the UI layer.
export function mrpackDownloadUrl(packId: number, buildId: number): string {
    return `${API_URL}/packs/${packId}/builds/${buildId}/export`;
}
