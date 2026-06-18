import { API_URL, getAuthHeader, handleResponse, handleError } from '@/lib/api/core';

// Plan is an admin-defined BYON tier. 0 = unlimited for every limit. Traffic
// limits are monthly GB and warn-only; max_nodes + R2 quota are hard-enforced.
export interface Plan {
    id: number;
    name: string;
    priceLabel: string;
    maxNodes: number;
    r2QuotaGb: number;
    trafficEdgeGb: number;
    trafficRelayGb: number;
    trafficCombinedGb: number;
    isDefault: boolean;
    createdAt?: string;
}

export type PlanInput = Omit<Plan, 'id' | 'createdAt'>;

// Per-user limit overrides. null on a field = use the plan value.
export interface LimitOverrides {
    maxNodes: number | null;
    trafficEdgeGb: number | null;
    trafficRelayGb: number | null;
    trafficCombinedGb: number | null;
}

export async function getPlans(): Promise<{ success: boolean; plans?: Plan[]; message?: string }> {
    try {
        const res = await fetch(`${API_URL}/admin/plans`, { headers: getAuthHeader() });
        return handleResponse(res) as any;
    } catch (err) { return handleError(err) as any; }
}

export async function createPlan(p: PlanInput): Promise<{ success: boolean; id?: number; message?: string }> {
    try {
        const res = await fetch(`${API_URL}/admin/plans`, {
            method: 'POST',
            headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
            body: JSON.stringify(p),
        });
        return handleResponse(res) as any;
    } catch (err) { return handleError(err) as any; }
}

export async function updatePlan(id: number, p: PlanInput): Promise<{ success: boolean; message?: string }> {
    try {
        const res = await fetch(`${API_URL}/admin/plans/${id}`, {
            method: 'PUT',
            headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
            body: JSON.stringify(p),
        });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}

export async function deletePlan(id: number): Promise<{ success: boolean; message?: string }> {
    try {
        const res = await fetch(`${API_URL}/admin/plans/${id}`, { method: 'DELETE', headers: getAuthHeader() });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}

export async function setUserPlan(userId: string, planId: number | null): Promise<{ success: boolean; message?: string }> {
    try {
        const res = await fetch(`${API_URL}/admin/users/${userId}/plan`, {
            method: 'PATCH',
            headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
            body: JSON.stringify({ planId }),
        });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
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
