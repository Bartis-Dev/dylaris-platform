import { API_URL, getAuthHeader, handleResponse, handleError } from './core';

// One changelog entry from an append-only service feed.
export interface UpdateEntry {
    date: string;
    service: string;
    type: 'feature' | 'fix' | 'change' | 'security' | string;
    summary: string;
}

// One COMPONENT's standing, each measured against its own installed build.
// baselineKnown is false when the component never reported one and Core's
// baseline stood in - "up to date" and "nobody asked" must not look identical.
export interface PerServiceBlock {
    service: string;
    installedCount: number;
    baselineKnown: boolean;
    behind: number;
    newEntries: UpdateEntry[] | null;
}

// One FEED's slice of the update response (platform or gateway).
export interface UpdateServiceBlock {
    installedCount: number;
    latestCount: number;
    updateAvailable: boolean;
    seenCount: number;
    unseen: number;
    newEntries: UpdateEntry[] | null;
    perService?: PerServiceBlock[] | null;
}

// The update-feed position THIS panel bundle was built at, stamped in by CI.
// Core cannot see it any other way: the panel is a static bundle running in
// someone's browser. Sent so Core can report whether the panel specifically is
// behind, instead of assuming it moved whenever Core did.
const PANEL_FEED_BASELINE = process.env.NEXT_PUBLIC_FEED_BASELINE || '';

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
        const qs = PANEL_FEED_BASELINE ? `?panelBaseline=${encodeURIComponent(PANEL_FEED_BASELINE)}` : '';
        const res = await fetch(`${API_URL}/updates${qs}`, { headers: getAuthHeader() });
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
