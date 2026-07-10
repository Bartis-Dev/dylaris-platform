// Admin API: provision / roll the gateway Hub admin Redis ACL user (gw-hub-admin)
// on the ONE shared Redis instance. Mirrors the nodeAdmission.ts fetch/auth-header
// pattern.

import { API_URL, getAuthHeader, handleResponse, handleError } from '@/lib/api/core';

export interface HubRedisAdminStatus {
    success: boolean;
    provisioned?: boolean;
    mode?: 'auto' | 'manual';
    addr?: string;
    db?: number;
    provisionedAt?: string;
    lastRolledAt?: string;
    message?: string;
}

// HubEnv is the ready-to-paste Hub deploy environment returned once on
// provision/roll. REDIS_ADDR is present in auto mode (Core's own address unless the
// admin overrode it) and present in manual mode only when an address was supplied.
export interface HubEnv {
    REDIS_ADDR?: string;
    REDIS_USER: string;
    REDIS_PASS: string;
    REDIS_DB?: number;
}

export interface HubRedisProvisionResult {
    success: boolean;
    username?: string;
    password?: string;
    hubEnv?: HubEnv;
    aclCommand?: string; // manual mode only
    message?: string;
}

export async function getHubRedisAdminStatus(): Promise<HubRedisAdminStatus> {
    try {
        const res = await fetch(`${API_URL}/settings/gateway/hub-redis-admin`, { headers: getAuthHeader() });
        return (await handleResponse(res)) as HubRedisAdminStatus;
    } catch (err) {
        return handleError(err) as HubRedisAdminStatus;
    }
}

export async function provisionHubRedisAdmin(
    body: { mode: 'auto' | 'manual'; db: number; hubAddr?: string },
): Promise<HubRedisProvisionResult> {
    try {
        const res = await fetch(`${API_URL}/settings/gateway/hub-redis-admin`, {
            method: 'POST',
            headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
            body: JSON.stringify(body),
        });
        return (await handleResponse(res)) as HubRedisProvisionResult;
    } catch (err) {
        return handleError(err) as HubRedisProvisionResult;
    }
}

export async function rollHubRedisAdmin(): Promise<HubRedisProvisionResult> {
    try {
        const res = await fetch(`${API_URL}/settings/gateway/hub-redis-admin/roll`, {
            method: 'POST',
            headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
            body: JSON.stringify({}),
        });
        return (await handleResponse(res)) as HubRedisProvisionResult;
    } catch (err) {
        return handleError(err) as HubRedisProvisionResult;
    }
}
