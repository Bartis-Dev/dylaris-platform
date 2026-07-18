import { API_URL, getAuthHeader, handleResponse, handleError } from './core';

// One changelog entry from an append-only service feed.
export interface UpdateEntry {
    date: string;
    service: string;
    type: 'feature' | 'fix' | 'change' | 'security' | string;
    summary: string;
}

// One service's slice of the update response (platform or gateway).
export interface UpdateServiceBlock {
    installedCount: number;
    latestCount: number;
    updateAvailable: boolean;
    seenCount: number;
    unseen: number;
    newEntries: UpdateEntry[] | null;
}

export interface UpdatesResponse {
    success: boolean;
    unseen: number;
    platform: UpdateServiceBlock;
    gateway?: UpdateServiceBlock; // present only when gateway routing is enabled
}

// getUpdates - ADMIN ONLY (server enforces IsAdmin; a non-admin gets 403). Returns
// the platform update-feed delta (always) and gateway delta (only when gateway
// routing is enabled and a gateway feed is configured), plus the caller's unseen
// count for the navbar badge. Fails soft on the server: an unreachable/empty feed
// simply reports no updates.
export async function getUpdates(): Promise<{ success: boolean; message?: string } & Partial<UpdatesResponse>> {
    try {
        const res = await fetch(`${API_URL}/updates`, { headers: getAuthHeader() });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}

// markUpdatesSeen - acknowledge the current feeds so the caller's badge clears.
// Server-computed (no body needed); own data.
export async function markUpdatesSeen() {
    try {
        const res = await fetch(`${API_URL}/me/updates-seen`, {
            method: 'PUT',
            headers: getAuthHeader(),
        });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}
