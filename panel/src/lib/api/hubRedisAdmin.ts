// TP2b admin API: provision / test / roll the gateway Hub admin Redis ACL user
// (gw-hub-admin). Mirrors the nodeAdmission.ts fetch/auth-header pattern.

import { API_URL, getAuthHeader, handleResponse, handleError } from '@/lib/api/core';

export interface HubRedisAdminStatus {
    success: boolean;
    provisioned?: boolean;
    mode?: 'same' | 'external' | 'manual';
    addr?: string;
    db?: number;
    adminUser?: string;
    provisionedAt?: string;
    lastRolledAt?: string;
    message?: string;
}

// HubEnv is the ready-to-paste Hub deploy environment returned once on
// provision/roll. REDIS_ADDR is absent in manual mode (the operator supplies it).
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

export interface HubRedisExternalTarget {
    addr: string;
    db: number;
    username: string;
    password?: string;
}

export async function getHubRedisAdminStatus(): Promise<HubRedisAdminStatus> {
    try {
        const res = await fetch(`${API_URL}/settings/gateway/hub-redis-admin`, { headers: getAuthHeader() });
        return (await handleResponse(res)) as HubRedisAdminStatus;
    } catch (err) {
        return handleError(err) as HubRedisAdminStatus;
    }
}

export async function testHubRedisConnection(
    body: HubRedisExternalTarget,
): Promise<{ success: boolean; ok?: boolean; whoami?: string; message?: string }> {
    try {
        const res = await fetch(`${API_URL}/settings/gateway/hub-redis-admin/test-connection`, {
            method: 'POST',
            headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
            body: JSON.stringify(body),
        });
        return (await handleResponse(res)) as { success: boolean; ok?: boolean; whoami?: string; message?: string };
    } catch (err) {
        return handleError(err) as { success: boolean; message?: string };
    }
}

export async function provisionHubRedisAdmin(
    body: { mode: 'same' | 'external' | 'manual'; db: number; external?: HubRedisExternalTarget },
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

export async function rollHubRedisAdmin(
    body: { external?: { password: string } },
): Promise<HubRedisProvisionResult> {
    try {
        const res = await fetch(`${API_URL}/settings/gateway/hub-redis-admin/roll`, {
            method: 'POST',
            headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
            body: JSON.stringify(body),
        });
        return (await handleResponse(res)) as HubRedisProvisionResult;
    } catch (err) {
        return handleError(err) as HubRedisProvisionResult;
    }
}
