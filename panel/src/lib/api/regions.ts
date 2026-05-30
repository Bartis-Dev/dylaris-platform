import { API_URL, getAuthHeader, handleResponse, handleError } from './core';

export interface Region {
    id: string;
    displayName: string;
    enabled: boolean;
    color?: string;
    createdAt?: string;
}

export interface UserRegions {
    allRegions: boolean;
    regions: string[];
}

// listRegions GET /api/regions — enabled regions, for any authenticated user.
export async function listRegions() {
    try {
        const res = await fetch(`${API_URL}/regions`, { headers: getAuthHeader() });
        return handleResponse(res);
    } catch (err) {
        return handleError(err);
    }
}

// adminListRegions GET /api/admin/regions — admin-only, includes disabled.
export async function adminListRegions() {
    try {
        const res = await fetch(`${API_URL}/admin/regions`, { headers: getAuthHeader() });
        return handleResponse(res);
    } catch (err) {
        return handleError(err);
    }
}

export async function createRegion(input: { id: string; displayName: string; enabled?: boolean; color?: string }) {
    try {
        const res = await fetch(`${API_URL}/admin/regions`, {
            method: 'POST',
            headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
            body: JSON.stringify(input),
        });
        return handleResponse(res);
    } catch (err) {
        return handleError(err);
    }
}

export async function updateRegion(id: string, input: { displayName?: string; enabled?: boolean; color?: string }) {
    try {
        const res = await fetch(`${API_URL}/admin/regions/${encodeURIComponent(id)}`, {
            method: 'PATCH',
            headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
            body: JSON.stringify(input),
        });
        return handleResponse(res);
    } catch (err) {
        return handleError(err);
    }
}

export async function deleteRegion(id: string) {
    try {
        const res = await fetch(`${API_URL}/admin/regions/${encodeURIComponent(id)}`, {
            method: 'DELETE',
            headers: getAuthHeader(),
        });
        return handleResponse(res);
    } catch (err) {
        return handleError(err);
    }
}

// getUserRegions GET /api/admin/users/{id}/regions
export async function getUserRegions(userId: string) {
    try {
        const res = await fetch(`${API_URL}/admin/users/${userId}/regions`, { headers: getAuthHeader() });
        return handleResponse(res);
    } catch (err) {
        return handleError(err);
    }
}

// setUserRegions PUT /api/admin/users/{id}/regions
export async function setUserRegions(userId: string, payload: UserRegions) {
    try {
        const res = await fetch(`${API_URL}/admin/users/${userId}/regions`, {
            method: 'PUT',
            headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
            body: JSON.stringify(payload),
        });
        return handleResponse(res);
    } catch (err) {
        return handleError(err);
    }
}

// getMyRegions GET /api/me/regions — current user's own assignment.
export async function getMyRegions() {
    try {
        const res = await fetch(`${API_URL}/me/regions`, { headers: getAuthHeader() });
        return handleResponse(res);
    } catch (err) {
        return handleError(err);
    }
}
