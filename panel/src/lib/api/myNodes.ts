// The tenant's own machine: reading what removing it would destroy, and
// removing it. Both answer only for a node the caller owns; see
// core/handlers/node_self_service.go for why this is a separate surface from
// the capability-gated admin routes rather than a relaxed gate on them.

import { API_URL, getAuthHeader, handleResponse, handleError } from '@/lib/api/core';

/** One server that would go with the machine. */
export interface MyNodeServer {
    id: number;
    name: string;
    uuid: string;
    /** Every install on it, by name. Read from the database, so it is still
     *  correct while the machine is already offline. */
    subServers: string[];
    /** The one currently booted, when there is one. */
    activeSubServer?: string;
}

export interface MyNodeContents {
    success: boolean;
    message?: string;
    node?: { id: number; name: string; status: string };
    servers?: MyNodeServer[];
}

export async function getMyNodeContents(nodeId: number): Promise<MyNodeContents> {
    try {
        const res = await fetch(`${API_URL}/me/nodes/${nodeId}/contents`, { headers: getAuthHeader() });
        return (await handleResponse(res)) as MyNodeContents;
    } catch (err) {
        return handleError(err) as MyNodeContents;
    }
}

/**
 * Remove the caller's own machine.
 *
 * withServers is explicit rather than inferred: the two reasons to remove a
 * machine are "I am moving it" and "I am done with it", and only the second
 * wants the worlds gone. Core refuses while servers remain unless it is set.
 */
export async function deleteMyNode(nodeId: number, withServers: boolean): Promise<{ success: boolean; message?: string }> {
    try {
        const q = withServers ? '?servers=delete' : '';
        const res = await fetch(`${API_URL}/me/nodes/${nodeId}${q}`, {
            method: 'DELETE',
            headers: getAuthHeader(),
        });
        return (await handleResponse(res)) as { success: boolean; message?: string };
    } catch (err) {
        return handleError(err) as { success: boolean; message?: string };
    }
}
