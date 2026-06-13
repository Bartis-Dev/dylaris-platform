// panel client for user-owned API keys. Plaintext is only ever
// returned once on creation; subsequent reads (list) carry hash metadata
// without the key value.

import { API_URL, getAuthHeader, handleResponse, handleError } from '@/lib/api/core';

export interface APIKeyScope {
    servers: string[];
    permissions: string[];
}

export interface APIKey {
    id: number;
    userId: string;
    name: string;
    scope: APIKeyScope;
    lastUsedAt?: string;
    expiresAt?: string;
    revokedAt?: string;
    ratePerMin: number;
    createdAt: string;
}

export interface CreateAPIKeyInput {
    name: string;
    servers: string[];
    permissions: string[];
    ratePerMin?: number;
}

export interface CreateAPIKeyResponse {
    success: boolean;
    apiKey?: APIKey;
    plaintext?: string;
    message?: string;
}

export async function listAPIKeys(): Promise<{ success: boolean; keys?: APIKey[]; message?: string }> {
    try {
        const res = await fetch(`${API_URL}/me/api-keys`, { headers: getAuthHeader() });
        return handleResponse(res) as any;
    } catch (err) { return handleError(err) as any; }
}

export async function createAPIKey(input: CreateAPIKeyInput): Promise<CreateAPIKeyResponse> {
    try {
        const res = await fetch(`${API_URL}/me/api-keys`, {
            method: 'POST',
            headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
            body: JSON.stringify(input),
        });
        return handleResponse(res) as any;
    } catch (err) { return handleError(err) as any; }
}

export async function revokeAPIKey(id: number): Promise<{ success: boolean; message?: string }> {
    try {
        const res = await fetch(`${API_URL}/me/api-keys/${id}`, {
            method: 'DELETE',
            headers: getAuthHeader(),
        });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}
