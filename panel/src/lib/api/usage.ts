import { API_URL, getAuthHeader, handleResponse, handleError } from '@/lib/api/core';

// One tenant's metered usage for a billing month. edgeBytes is the billable
// player traffic; relayBytes (filebrowser) and backupBytes (R2 storage) are
// observability + future overage.
export interface TrafficUsage {
    userId: string;
    username?: string;
    period: string;
    edgeBytes: number;
    relayBytes: number;
    backupBytes: number;
    updatedAt: string;
}

// getMyUsage returns the caller's own usage for the current (or ?period) month.
export async function getMyUsage(period?: string): Promise<{ success: boolean; usage?: TrafficUsage; message?: string }> {
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
