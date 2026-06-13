import { API_URL, getAuthHeader, handleResponse, handleError } from '@/lib/api/core';

export interface AccountPolicy {
    allowNameChange: boolean;
    nameChangeCooldownDays: number;
}

export interface UsernameHistoryEntry {
    id: number;
    userId: string;
    oldUsername: string;
    newUsername: string;
    changedAt: string;
    changedBy?: string;
    byAdmin: boolean;
}

export async function getAccountPolicy(): Promise<{ success: boolean; policy?: AccountPolicy; message?: string }> {
    try {
        const res = await fetch(`${API_URL}/admin/settings/users`, { headers: getAuthHeader() });
        return handleResponse(res) as any;
    } catch (err) { return handleError(err) as any; }
}

export async function setAccountPolicy(p: AccountPolicy): Promise<{ success: boolean; message?: string }> {
    try {
        const res = await fetch(`${API_URL}/admin/settings/users`, {
            method: 'PUT',
            headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
            body: JSON.stringify(p),
        });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}

export async function getMyUsernameHistory(): Promise<UsernameHistoryEntry[]> {
    try {
        const res = await fetch(`${API_URL}/me/username-history`, { headers: getAuthHeader() });
        const data = await handleResponse(res);
        return (data as any).history || [];
    } catch (err) { handleError(err); return []; }
}

export async function getUsernameHistory(userId: string): Promise<UsernameHistoryEntry[]> {
    try {
        const res = await fetch(`${API_URL}/admin/users/${userId}/username-history`, { headers: getAuthHeader() });
        const data = await handleResponse(res);
        return (data as any).history || [];
    } catch (err) { handleError(err); return []; }
}
