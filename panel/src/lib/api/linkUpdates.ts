// Typed client for the Link sidecar update surface.
//
// The node replaces its own Link container; Core only carries the operator's
// preference and the manual trigger. The policy reaches DATACENTER nodes only -
// an external/BYON node always applies updates immediately and ignores it, since
// there is nobody on that machine to react to a notification.

import { API_URL, getAuthHeader, handleResponse, handleError } from '@/lib/api/core';

export type LinkUpdatePolicy = 'notify' | 'auto_idle' | 'auto';

export interface LinkUpdateSettings {
    policy: LinkUpdatePolicy;
    intervalMinutes: number;
}

// One node's Link image status. A node ABSENT from the map is not reporting -
// which is "unknown", not "up to date".
export interface NodeLinkState {
    // false when an operator deploys this node's Link. Core cannot replace that
    // container, so no update button may be offered for it.
    managed: boolean;
    running?: string;
    available?: string;
    updateAvailable: boolean;
}

export async function getLinkUpdateSettings(): Promise<LinkUpdateSettings | null> {
    try {
        const res = await fetch(`${API_URL}/admin/settings/link-updates`, { headers: getAuthHeader() });
        return (await handleResponse(res)) as LinkUpdateSettings;
    } catch (err) { handleError(err); return null; }
}

export async function saveLinkUpdateSettings(s: LinkUpdateSettings): Promise<LinkUpdateSettings | null> {
    try {
        const res = await fetch(`${API_URL}/admin/settings/link-updates`, {
            method: 'PUT',
            headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
            body: JSON.stringify(s),
        });
        return (await handleResponse(res)) as LinkUpdateSettings;
    } catch (err) { handleError(err); return null; }
}

export async function getLinkUpdateStates(): Promise<Record<string, NodeLinkState>> {
    try {
        const res = await fetch(`${API_URL}/nodes/link-updates`, { headers: getAuthHeader() });
        return ((await handleResponse(res)) as Record<string, NodeLinkState>) || {};
    } catch (err) { handleError(err); return {}; }
}

// Omit nodeId to update every node that has an update pending. Naming a node
// applies it there regardless of whether drift was detected.
export async function triggerLinkUpdate(nodeId?: string): Promise<{ queued: string[]; count: number } | null> {
    try {
        const res = await fetch(`${API_URL}/nodes/link-updates`, {
            method: 'POST',
            headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
            body: JSON.stringify(nodeId ? { nodeId } : {}),
        });
        return (await handleResponse(res)) as { queued: string[]; count: number };
    } catch (err) { handleError(err); return null; }
}
