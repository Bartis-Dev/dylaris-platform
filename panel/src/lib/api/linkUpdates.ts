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

// RAISES on a failed request. The `|| {}` this replaces never once fired: a
// failure envelope is an object, so it was returned AS the state map, every
// node lookup missed, and the panel printed "No node is reporting its Link
// status yet. Nodes report on their heartbeat, and an older node image does not
// report at all."
//
// That sentence is the reason this is worth changing rather than an empty list
// would be. It is specific, plausible and wrong, and it sends an operator to
// look at node images when the answer is that we could not ask.
export async function getLinkUpdateStates(): Promise<Record<string, NodeLinkState>> {
    const res = await fetch(`${API_URL}/nodes/link-updates`, { headers: getAuthHeader() });
    const data = await handleResponse(res);
    if (data && (data as any).success === false) {
        throw new Error((data as any).message || 'Could not read the Link status of your nodes.');
    }
    // handleResponse spreads the body over its own { success, status }, so on a
    // bare map those two arrive as entries. No node token can collide with them,
    // which is why this was never visible - but the function claims to return
    // node states, so it should not hand back two that are not.
    const { success: _s, status: _st, ...states } = (data ?? {}) as Record<string, unknown>;
    return states as Record<string, NodeLinkState>;
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
