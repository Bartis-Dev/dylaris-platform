// typed client for /api/admin/settings/modpacks. GET omits the S3
// secret value (returns "") but exposes secretSet so the UI can render
// "(unchanged — leave empty)" vs "(not set yet)".

import { API_URL, getAuthHeader, handleResponse, handleError } from '@/lib/api/core';

export interface ModpackSettings {
    featureEnabled: boolean;
    provider: 'local' | 's3';
    paths: string[];
    s3Endpoint: string;
    s3Bucket: string;
    s3Region: string;
    s3AccessKey: string;
    s3SecretKey?: string;
    updateCheckIntervalHours: number;
    shareLinksEnabled: boolean;
}

export interface GetModpackSettingsResponse {
    success: boolean;
    settings?: ModpackSettings;
    secretSet?: boolean;
    message?: string;
}

export async function getModpackSettings(): Promise<GetModpackSettingsResponse> {
    try {
        const res = await fetch(`${API_URL}/admin/settings/modpacks`, { headers: getAuthHeader() });
        return (await handleResponse(res)) as any;
    } catch (err) { return handleError(err) as any; }
}

export async function setModpackSettings(s: ModpackSettings): Promise<{ success: boolean; message?: string }> {
    try {
        const res = await fetch(`${API_URL}/admin/settings/modpacks`, {
            method: 'PUT',
            headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
            body: JSON.stringify(s),
        });
        return (await handleResponse(res)) as any;
    } catch (err) { return handleError(err) as any; }
}

export async function setUserModpackFlag(userId: string, canCreate: boolean): Promise<{ success: boolean; message?: string }> {
    try {
        const res = await fetch(`${API_URL}/admin/users/${encodeURIComponent(userId)}/modpack-flag`, {
            method: 'PATCH',
            headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
            body: JSON.stringify({ canCreate }),
        });
        return (await handleResponse(res)) as any;
    } catch (err) { return handleError(err) as any; }
}
