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
    /** The LATER of the two deadlines below - when the tenant stops being granted anything. */
    grantExpiresAt?: string;
    /**
     * Per-kind deadlines. The two grants are independent: one may run for a week
     * and the other for a year, and ending one leaves the other alone.
     */
    grantByonExpiresAt?: string;
    grantRouteExpiresAt?: string;
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

/**
 * Grant BYON and/or route-only for `days` days. Additive to the tenant's plan.
 *
 * `amount` is how many of that kind they may hold, written as the matching limit
 * override. Omit it to leave the limit alone - but note that an absent limit is
 * NO limit, so a grant without one lets the tenant enroll without bound until a
 * purchase pushes a real cap, at which point they are retroactively over it.
 */
export async function grantEntitlement(
    userId: string,
    kind: 'byon' | 'route_only' | 'both',
    days: number,
    amount?: number | null,
): Promise<EntitlementResponse> {
    try {
        const res = await fetch(`${API_URL}/admin/users/${encodeURIComponent(userId)}/entitlement`, {
            method: 'POST',
            headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
            body: JSON.stringify(amount === undefined ? { kind, days } : { kind, days, amount }),
        });
        return (await handleResponse(res)) as EntitlementResponse;
    } catch (err) {
        return handleError(err) as EntitlementResponse;
    }
}

/**
 * Take back a grant. Whatever the plan allows is untouched.
 *
 * `kind` ends ONE of the two and leaves the other running; omitting it ends both,
 * which is what every caller meant before the two could be held separately.
 */
export async function revokeEntitlement(userId: string, kind?: 'byon' | 'route_only'): Promise<EntitlementResponse> {
    try {
        const query = kind ? `?kind=${kind}` : '';
        const res = await fetch(`${API_URL}/admin/users/${encodeURIComponent(userId)}/entitlement${query}`, {
            method: 'DELETE',
            headers: getAuthHeader(),
        });
        return (await handleResponse(res)) as EntitlementResponse;
    } catch (err) {
        return handleError(err) as EntitlementResponse;
    }
}
