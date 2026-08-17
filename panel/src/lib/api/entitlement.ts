// What the caller may USE (BYON / route-only), as opposed to how much of it
// (that is limits/usage). Resolved server-side from the tenant's plan and any
// admin-granted entitlement; see core/services/entitlement.go.

import { API_URL, getAuthHeader, handleResponse, handleError } from '@/lib/api/core';

export interface Entitlement {
    /** May enroll their own nodes and run servers on them. */
    byon: boolean;
    /** May create routes / link kits without owning a node. */
    routeOnly: boolean;
    /**
     * Why: "plan" | "grant" | "plan+grant" | "unlimited" | "none" | "suspended".
     * "unlimited" is a platform with no plans defined at all, which is every
     * self-host install - it is a real yes, not a fallback.
     */
    source: string;
    planKind?: string;
    /** Only present while a manual grant is ACTIVE; an expired one is not reported. */
    grantKind?: string;
    grantExpiresAt?: string;
}

export interface EntitlementResponse extends Partial<Entitlement> {
    success: boolean;
    message?: string;
}

/** The caller's own entitlement. Never 503s when BYON is off - it answers "no". */
export async function getMyEntitlement(): Promise<EntitlementResponse> {
    try {
        const res = await fetch(`${API_URL}/me/entitlement`, { headers: getAuthHeader() });
        return (await handleResponse(res)) as EntitlementResponse;
    } catch (err) {
        return handleError(err) as EntitlementResponse;
    }
}

export async function getUserEntitlement(userId: string): Promise<EntitlementResponse> {
    try {
        const res = await fetch(`${API_URL}/admin/users/${encodeURIComponent(userId)}/entitlement`, {
            headers: getAuthHeader(),
        });
        return (await handleResponse(res)) as EntitlementResponse;
    } catch (err) {
        return handleError(err) as EntitlementResponse;
    }
}

/** Grant BYON and/or route-only for `days` days. Additive to the tenant's plan. */
export async function grantEntitlement(userId: string, kind: 'byon' | 'route_only' | 'both', days: number): Promise<EntitlementResponse> {
    try {
        const res = await fetch(`${API_URL}/admin/users/${encodeURIComponent(userId)}/entitlement`, {
            method: 'POST',
            headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
            body: JSON.stringify({ kind, days }),
        });
        return (await handleResponse(res)) as EntitlementResponse;
    } catch (err) {
        return handleError(err) as EntitlementResponse;
    }
}

/** Take back the grant. Whatever the plan allows is untouched. */
export async function revokeEntitlement(userId: string): Promise<EntitlementResponse> {
    try {
        const res = await fetch(`${API_URL}/admin/users/${encodeURIComponent(userId)}/entitlement`, {
            method: 'DELETE',
            headers: getAuthHeader(),
        });
        return (await handleResponse(res)) as EntitlementResponse;
    } catch (err) {
        return handleError(err) as EntitlementResponse;
    }
}
