// In-panel changelog API client.
//
// Hits /api/changelog (GET, returns released + coming-soon entries + the
// per-user last-seen cursor for the unread badge) and
// /api/changelog/mark-seen (POST, advances the cursor — clamped upward on
// the server so this is safe to call with any date).

import { API_URL, getAuthHeader, handleResponse, handleError } from '@/lib/api/core';

export type ChangelogType = 'feature' | 'fix' | 'breaking' | 'improvement' | 'security';
export type ChangelogAudience = 'everyone' | 'admin';
export type ChangelogChannel = 'released' | 'coming_soon';

export interface ChangelogEntry {
    date: string;       // ISO datetime ("2026-06-09T00:00:00Z") from the server
    dateStr: string;    // "2026-06-09" — stable key + display
    slug: string;
    type: ChangelogType;
    audience: ChangelogAudience;
    title: string;
    body: string;       // markdown
    channel: ChangelogChannel;
}

export interface ChangelogResponse {
    released: ChangelogEntry[];
    comingSoon: ChangelogEntry[];
    unreadCount: number;
    lastSeen: string | null;
}

interface GetChangelogResult {
    success: boolean;
    data?: ChangelogResponse;
    message?: string;
}

export async function getChangelog(): Promise<GetChangelogResult> {
    try {
        const res = await fetch(`${API_URL}/changelog`, {
            headers: { ...getAuthHeader() },
        });
        const parsed = await handleResponse(res);
        if (!parsed.success) {
            return { success: false, message: parsed.message };
        }
        return {
            success: true,
            data: {
                released: parsed.released || [],
                comingSoon: parsed.comingSoon || [],
                unreadCount: parsed.unreadCount || 0,
                lastSeen: parsed.lastSeen ?? null,
            },
        };
    } catch (err) {
        return handleError(err);
    }
}

interface MarkSeenResult {
    success: boolean;
    message?: string;
}

export async function markChangelogSeen(latestDate: string): Promise<MarkSeenResult> {
    try {
        const res = await fetch(`${API_URL}/changelog/mark-seen`, {
            method: 'POST',
            headers: {
                ...getAuthHeader(),
                'Content-Type': 'application/json',
            },
            body: JSON.stringify({ latestDate }),
        });
        return await handleResponse(res);
    } catch (err) {
        return handleError(err);
    }
}
