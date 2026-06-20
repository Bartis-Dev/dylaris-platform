import { API_URL, getAuthHeader, handleResponse, handleError } from './core';

// Store-linking client. The connect-store surface only exists when the hosted
// Core has the store ENV set (features.store); these calls 404 otherwise.

export interface StoreStatus {
    success: boolean;
    enabled: boolean;
    linked: boolean;
    email?: string;
    storeUrl?: string;
    message?: string;
}

// GET /store/status — linked state, read on demand from dylaris.com.
export async function getStoreStatus(): Promise<StoreStatus> {
    try {
        const res = await fetch(`${API_URL}/store/status`, { headers: getAuthHeader() });
        return (await handleResponse(res)) as StoreStatus;
    } catch (err) {
        return handleError(err) as StoreStatus;
    }
}

// POST /store/link/start — mints a single-use token and returns the storefront
// redirect URL (dylaris.com/connect?token=...).
export async function startStoreLink(): Promise<{ success: boolean; redirectUrl?: string; message?: string }> {
    try {
        const res = await fetch(`${API_URL}/store/link/start`, {
            method: 'POST',
            headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
        });
        return (await handleResponse(res)) as { success: boolean; redirectUrl?: string; message?: string };
    } catch (err) {
        return handleError(err) as { success: boolean; redirectUrl?: string; message?: string };
    }
}
