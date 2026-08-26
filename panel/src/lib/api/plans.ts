import { API_URL, getAuthHeader, handleResponse, handleError } from '@/lib/api/core';

// Per-user limit overrides: a tenant's caps, full stop. null on a field leaves
// it unset, which means unlimited.
//
// Admin-defined plan tiers used to sit under these as a baseline. They are gone:
// the hosted store never sold one (it pushes a node COUNT through this same
// call) and handing self-hosters a tariff editor was a product nobody wanted.
export interface LimitOverrides {
    maxNodes: number | null;
    maxLinks: number | null;
    trafficEdgeGb: number | null;
    trafficRelayGb: number | null;
    trafficCombinedGb: number | null;
}

export async function setUserLimitOverrides(userId: string, o: LimitOverrides): Promise<{ success: boolean; message?: string }> {
    try {
        const res = await fetch(`${API_URL}/admin/users/${userId}/limit-overrides`, {
            method: 'PATCH',
            headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
            body: JSON.stringify(o),
        });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}
