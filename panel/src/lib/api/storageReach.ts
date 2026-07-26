// Typed client for /api/settings/storage-reach: the fleet-wide storage health
// surface (which Cores are currently failing their self-check, and which are
// online at all). Read-only - it never triggers a check itself, it only
// reads what the periodic self-check and any in-flight config round already
// recorded server-side.
import { API_URL, getAuthHeader, handleResponse, handleError } from '@/lib/api/core';
import type { CoreReachResult } from '@/lib/storageReach';

export interface StorageFault {
    coreId: string;
    hostname?: string;
    status: CoreReachResult['status'];
    detail?: string;
    missingPeers?: string[];
    deniedPeers?: string[];
    since: number;
    at: number;
}

export interface StorageReachStatusResponse {
    success: boolean;
    message?: string;
    faults?: StorageFault[];
    onlineCores?: string[];
}

export async function getStorageReachStatus(): Promise<StorageReachStatusResponse> {
    try {
        const res = await fetch(`${API_URL}/settings/storage-reach`, {
            headers: { ...getAuthHeader() },
        });
        return (await handleResponse(res)) as StorageReachStatusResponse;
    } catch (err) {
        return handleError(err) as StorageReachStatusResponse;
    }
}
