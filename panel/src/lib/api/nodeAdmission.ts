// P0b-5 admin API: node admission config (join/IP mode + CIDRs), per-node
// reset-pairing, and the enroll-token surface. Mirrors the featureFlags.ts
// fetch/auth-header pattern (auth token key: authToken || token).

import { API_URL, getAuthHeader, handleResponse, handleError } from '@/lib/api/core';
import type { AdmissionCIDR, NodeEnrollToken } from '@/lib/api/types';

export async function getNodeAdmission(): Promise<{ success: boolean; joinMode?: string; ipMode?: string; cidrs?: AdmissionCIDR[]; message?: string }> {
    try {
        const res = await fetch(`${API_URL}/admin/settings/node-admission`, { headers: getAuthHeader() });
        return (await handleResponse(res)) as { success: boolean; joinMode?: string; ipMode?: string; cidrs?: AdmissionCIDR[]; message?: string };
    } catch (err) {
        return handleError(err) as { success: boolean; message?: string };
    }
}

export async function updateNodeAdmission(payload: { joinMode: string; ipMode: string }): Promise<{ success: boolean; joinMode?: string; ipMode?: string; message?: string }> {
    try {
        const res = await fetch(`${API_URL}/admin/settings/node-admission`, {
            method: 'PUT',
            headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
            body: JSON.stringify(payload),
        });
        return (await handleResponse(res)) as { success: boolean; joinMode?: string; ipMode?: string; message?: string };
    } catch (err) {
        return handleError(err) as { success: boolean; message?: string };
    }
}

export async function addAdmissionCIDR(payload: { cidr: string; label: string }): Promise<{ success: boolean; cidr?: string; message?: string }> {
    try {
        const res = await fetch(`${API_URL}/admin/settings/node-admission/cidrs`, {
            method: 'POST',
            headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
            body: JSON.stringify(payload),
        });
        return (await handleResponse(res)) as { success: boolean; cidr?: string; message?: string };
    } catch (err) {
        return handleError(err) as { success: boolean; message?: string };
    }
}

export async function deleteAdmissionCIDR(id: string): Promise<{ success: boolean; message?: string }> {
    try {
        const res = await fetch(`${API_URL}/admin/settings/node-admission/cidrs/${id}`, {
            method: 'DELETE',
            headers: getAuthHeader(),
        });
        return (await handleResponse(res)) as { success: boolean; message?: string };
    } catch (err) {
        return handleError(err) as { success: boolean; message?: string };
    }
}

export async function resetNodePairing(nodeId: number): Promise<{ success: boolean; token?: string; env?: string; note?: string; message?: string }> {
    try {
        const res = await fetch(`${API_URL}/admin/nodes/${nodeId}/reset-pairing`, {
            method: 'POST',
            headers: getAuthHeader(),
        });
        return (await handleResponse(res)) as { success: boolean; token?: string; env?: string; note?: string; message?: string };
    } catch (err) {
        return handleError(err) as { success: boolean; message?: string };
    }
}

export async function mintEnrollToken(payload: { label: string; expiresDays: number }): Promise<{ success: boolean; token?: string; grpcTlsFingerprint?: string; note?: string; message?: string }> {
    try {
        const res = await fetch(`${API_URL}/nodes/enroll-token`, {
            method: 'POST',
            headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
            body: JSON.stringify({ label: payload.label, expiresDays: payload.expiresDays }),
        });
        return (await handleResponse(res)) as { success: boolean; token?: string; grpcTlsFingerprint?: string; note?: string; message?: string };
    } catch (err) {
        return handleError(err) as { success: boolean; message?: string };
    }
}

export async function listEnrollTokens(): Promise<{ success: boolean; tokens?: NodeEnrollToken[]; message?: string }> {
    try {
        const res = await fetch(`${API_URL}/nodes/enroll-token`, { headers: getAuthHeader() });
        return (await handleResponse(res)) as { success: boolean; tokens?: NodeEnrollToken[]; message?: string };
    } catch (err) {
        return handleError(err) as { success: boolean; message?: string };
    }
}

export async function revokeEnrollToken(id: string): Promise<{ success: boolean; message?: string }> {
    try {
        const res = await fetch(`${API_URL}/nodes/enroll-token/${id}`, {
            method: 'DELETE',
            headers: getAuthHeader(),
        });
        return (await handleResponse(res)) as { success: boolean; message?: string };
    } catch (err) {
        return handleError(err) as { success: boolean; message?: string };
    }
}
