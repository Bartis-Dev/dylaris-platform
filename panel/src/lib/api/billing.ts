import { API_URL, getAuthHeader, handleResponse, handleError } from '@/lib/api/core';

export type BillingStatus = 'active' | 'past_due' | 'suspended';

export interface BillingSettings {
    gracePeriod: string;
    r2Retention: string;
    nodeRetention: string;
    r2QuotaGb: string;
    presignTtlNodeMin: string;
    presignTtlByonMin: string;
    paymentUrl: string;
}

// Per-user retention + limit overrides. Empty string on a spec / null on a numeric
// field means "use the plan / platform default". A 0 means unlimited for this user.
export interface UserBillingOverrides {
    gracePeriod: string;
    r2Retention: string;
    nodeRetention: string;
    r2QuotaGb: number | null;
    maxNodes: number | null;
    maxLinks: number | null;
    trafficEdgeGb: number | null;
    trafficRelayGb: number | null;
    trafficCombinedGb: number | null;
}

// Admin read of a single tenant's billing state plus the platform defaults the
// override fields fall back to (shown as placeholders in the UI).
export interface UserBillingAdmin {
    success: boolean;
    status: BillingStatus;
    graceUntil?: string | null;
    suspendedAt?: string | null;
    planId?: number | null;
    overrides: UserBillingOverrides;
    defaults: { gracePeriod: string; r2Retention: string; nodeRetention: string; r2QuotaGb: string };
    message?: string;
}

// getMyBilling returns the caller's lifecycle state for the banner.
export async function getMyBilling(): Promise<{ success: boolean; status?: BillingStatus; graceUntil?: string | null; paymentUrl?: string }> {
    try {
        const res = await fetch(`${API_URL}/me/billing`, { headers: getAuthHeader() });
        return handleResponse(res) as any;
    } catch (err) { return handleError(err) as any; }
}

export async function getBillingSettings(): Promise<{ success: boolean } & Partial<BillingSettings> & { message?: string }> {
    try {
        const res = await fetch(`${API_URL}/admin/settings/billing`, { headers: getAuthHeader() });
        return handleResponse(res) as any;
    } catch (err) { return handleError(err) as any; }
}

export async function setBillingSettings(s: BillingSettings): Promise<{ success: boolean; message?: string }> {
    try {
        const res = await fetch(`${API_URL}/admin/settings/billing`, {
            method: 'PUT',
            headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
            body: JSON.stringify(s),
        });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}

// Admin: set a tenant's lifecycle status (active | past_due | suspended).
export async function setUserBillingStatus(userId: string, status: BillingStatus): Promise<{ success: boolean; message?: string }> {
    try {
        const res = await fetch(`${API_URL}/admin/users/${userId}/billing`, {
            method: 'PATCH',
            headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
            body: JSON.stringify({ status }),
        });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}

// Admin: read a tenant's full billing state + platform defaults for the modal.
export async function getUserBilling(userId: string): Promise<UserBillingAdmin> {
    try {
        const res = await fetch(`${API_URL}/admin/users/${userId}/billing`, { headers: getAuthHeader() });
        return handleResponse(res) as any;
    } catch (err) { return handleError(err) as any; }
}

// RetentionOverridesInput is the subset the /billing-overrides endpoint accepts
// (the limit overrides go to /limit-overrides via setUserLimitOverrides).
export interface RetentionOverridesInput {
    gracePeriod: string;
    r2Retention: string;
    nodeRetention: string;
    r2QuotaGb: number | null;
}

// Admin: set a tenant's per-user retention overrides (empty string / null clears one).
export async function setUserBillingOverrides(userId: string, o: RetentionOverridesInput): Promise<{ success: boolean; message?: string }> {
    try {
        const res = await fetch(`${API_URL}/admin/users/${userId}/billing-overrides`, {
            method: 'PATCH',
            headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
            body: JSON.stringify(o),
        });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}
