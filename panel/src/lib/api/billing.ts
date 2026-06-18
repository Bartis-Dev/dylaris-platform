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

// Admin: set a tenant's per-user retention overrides (empty string clears one).
export async function setUserBillingOverrides(userId: string, o: { gracePeriod: string; r2Retention: string; nodeRetention: string }): Promise<{ success: boolean; message?: string }> {
    try {
        const res = await fetch(`${API_URL}/admin/users/${userId}/billing-overrides`, {
            method: 'PATCH',
            headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
            body: JSON.stringify(o),
        });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}
