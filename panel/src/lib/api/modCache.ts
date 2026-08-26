import { API_URL, getAuthHeader, handleError } from '@/lib/api/core';

// Where Modrinth metadata is cached. An empty address means the Redis this
// panel already runs on, which is the default and needs no configuration.

export interface ModCacheStatus {
    dedicated: boolean;
    addr?: string;
    healthy: boolean;
    error?: string;
}

export interface ModCacheSettings {
    addr: string;
    username: string;
    db: number;
    tls: boolean;
    /** True when a password is stored. The password itself never comes back. */
    passwordSet: boolean;
    /** Write-only. Blank on save keeps the stored one. */
    password?: string;
    status: ModCacheStatus;
}

export async function getModCacheSettings(): Promise<ModCacheSettings | null> {
    try {
        const res = await fetch(`${API_URL}/admin/settings/mod-cache`, { headers: getAuthHeader() });
        if (!res.ok) return null;
        const data = await res.json();
        return data.settings ?? null;
    } catch { return null; }
}

export async function saveModCacheSettings(
    input: { addr: string; username: string; db: number; tls: boolean; password: string },
): Promise<{ success: boolean; message?: string; status?: ModCacheStatus }> {
    try {
        const res = await fetch(`${API_URL}/admin/settings/mod-cache`, {
            method: 'PUT',
            headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
            body: JSON.stringify(input),
        });
        const data = await res.json();
        return { success: res.ok, ...data };
    } catch (err) { return handleError(err) as { success: boolean; message?: string }; }
}
