import { API_URL, getAuthHeader, handleResponse, handleError } from '@/lib/api/core';

// Effective limits the usage is measured against (0 = unlimited). GB.
export interface UsageLimits {
    maxNodes: number;
    r2QuotaGb: number;
    trafficEdgeGb: number;
    trafficRelayGb: number;
    trafficCombinedGb: number;
}

// Per-channel over-limit warn flags (traffic is warn-only — not blocked).
export interface UsageOver {
    edge: boolean;
    relay: boolean;
    combined: boolean;
    r2: boolean;
}

// One tenant's metered usage for a billing month. edgeBytes is the billable
// player traffic; relayBytes (filebrowser) and backupBytes (R2 storage) are
// observability + future overage. limits/over are the effective plan/override
// caps and the over-limit warn flags.
export interface TrafficUsage {
    userId: string;
    username?: string;
    period: string;
    edgeBytes: number;
    relayBytes: number;
    backupBytes: number;
    updatedAt: string;
    limits?: UsageLimits;
    over?: UsageOver;
}

// EntitlementState is present only while the caller holds MORE than they bought,
// which normally means they downgraded. Absent is the normal case, so the banner
// has nothing to render and no shape to special-case.
export interface EntitlementState {
    overLimit: boolean;
    overLimitSince: string;
    // When everything is disconnected if they are still over. Sent by Core rather
    // than computed here so the panel cannot promise a different deadline than
    // the sweep enforces.
    cutoffAt: string;
}

// getMyUsage returns the caller's own usage for the current (or ?period) month.
export async function getMyUsage(period?: string): Promise<{ success: boolean; usage?: TrafficUsage; entitlementState?: EntitlementState; message?: string }> {
    try {
        const q = period ? `?period=${encodeURIComponent(period)}` : '';
        const res = await fetch(`${API_URL}/me/usage${q}`, { headers: getAuthHeader() });
        return handleResponse(res) as any;
    } catch (err) { return handleError(err) as any; }
}

// getAllUsage returns every tenant's usage for a month (admin only).
export async function getAllUsage(period?: string): Promise<{ success: boolean; period?: string; usage?: TrafficUsage[]; message?: string }> {
    try {
        const q = period ? `?period=${encodeURIComponent(period)}` : '';
        const res = await fetch(`${API_URL}/admin/usage${q}`, { headers: getAuthHeader() });
        return handleResponse(res) as any;
    } catch (err) { return handleError(err) as any; }
}

// formatBytes renders a byte count as a human GiB/MiB/etc string.
export function formatBytes(bytes: number): string {
    if (!bytes || bytes <= 0) return '0 B';
    const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB'];
    const i = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1);
    const value = bytes / Math.pow(1024, i);
    return `${value.toFixed(i === 0 ? 0 : 2)} ${units[i]}`;
}
