// first-run setup wizard client. Two endpoints:
//   GET  /api/setup/status — what mode are we in?
//   POST /api/setup/admin  — create the first admin
//
// Both are open routes (no auth header needed) — the setup-lock middleware
// short-circuits /api/setup/* even in Fresh-Install mode where every other
// API route returns 503 setup_required.

import { API_URL, handleResponse, handleError } from '@/lib/api/core';

export type SetupMode = 'fresh_install' | 'lost_admin' | 'complete';

export interface SetupStatus {
    success: boolean;
    mode: SetupMode;
    requiresRecoveryToken: boolean;
    frontendUrl?: string;
    message?: string;
}

export interface SetupTOTPInfo {
    secret: string;
    code: string;
}

export interface SetupAdminRequest {
    username: string;
    password: string;
    recoveryToken?: string;
    totp?: SetupTOTPInfo;
}

export interface SetupAdminUser {
    id: string;
    username: string;
    isAdmin: boolean;
}

export interface SetupAdminResponse {
    success: boolean;
    error?: string;
    message?: string;
    user?: SetupAdminUser;
    token?: string;
}

export async function getSetupStatus(): Promise<SetupStatus> {
    try {
        const res = await fetch(`${API_URL}/setup/status`);
        return (await handleResponse(res)) as any;
    } catch (err) {
        // On hard transport failure, fall back to "complete" so a flaky
        // network doesn't lock the user into the wizard forever. The next
        // user action that fetches anything else will reveal whether the
        // backend is actually reachable.
        return { success: false, mode: 'complete', requiresRecoveryToken: false };
    }
}

export async function createFirstAdmin(req: SetupAdminRequest): Promise<SetupAdminResponse> {
    try {
        const res = await fetch(`${API_URL}/setup/admin`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(req),
        });
        return (await handleResponse(res)) as any;
    } catch (err) {
        return handleError(err) as any;
    }
}
