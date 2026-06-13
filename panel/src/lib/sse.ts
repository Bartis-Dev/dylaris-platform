// Shared EventSource factory that keeps the long-lived session JWT out of
// URLs. EventSource cannot set an Authorization header, so the only way to
// authenticate an SSE stream is via the URL — and putting the session JWT
// there leaks it into access logs / the Referer header.
//
// Instead we mint a short-lived, single-purpose ticket (POST /api/sse-ticket,
// authenticated with the normal Bearer header) and carry that in ?ticket=.
// The ticket expires in 5 minutes server-side with a sliding window, so
// native EventSource auto-reconnects within the window reuse the same URL.

import { API_URL, getAuthHeader } from '@/lib/api/core';

/**
 * Mint an SSE ticket and open an EventSource for `path` (e.g.
 * "/system/events" or "/servers/1/console/stream?sub_server=foo").
 * Throws if the ticket mint fails so the caller's existing error path runs.
 */
export async function createEventSource(path: string): Promise<EventSource> {
    const res = await fetch(`${API_URL}/sse-ticket`, {
        method: 'POST',
        headers: getAuthHeader(),
    });
    if (!res.ok) {
        throw new Error(`sse-ticket mint failed: ${res.status}`);
    }
    const data = await res.json();
    const ticket: string = data.ticket;
    if (!ticket) {
        throw new Error('sse-ticket mint returned no ticket');
    }
    const sep = path.includes('?') ? '&' : '?';
    return new EventSource(`${API_URL}${path}${sep}ticket=${encodeURIComponent(ticket)}`);
}
