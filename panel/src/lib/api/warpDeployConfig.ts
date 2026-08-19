// The overlay addresses a BYON or route-only machine has to be given. Core
// resolves them (it is on the same network); the panel only renders them into
// the deploy snippet, so the customer never has to look up a value that exists
// nowhere they can see.

import { API_URL, getAuthHeader, handleResponse, handleError } from '@/lib/api/core';

export interface WarpDeployAddrs {
    /** Core gRPC over the overlay, e.g. 10.20.0.4:25501. "" = undetermined. */
    coreGrpcAddr: string;
    /** Central Redis over the overlay, e.g. 10.20.0.5:6379. "" = undetermined. */
    redisAddr: string;
    /** Stored overlay CIDR(s). "" until an operator saves one. */
    tunnelSubnets: string;
}

export async function getWarpDeployAddrs(): Promise<{ success: boolean; addrs?: WarpDeployAddrs; message?: string }> {
    try {
        const res = await fetch(`${API_URL}/warp/deploy-config`, { headers: getAuthHeader() });
        return (await handleResponse(res)) as { success: boolean; addrs?: WarpDeployAddrs; message?: string };
    } catch (err) {
        return handleError(err) as { success: boolean; message?: string };
    }
}
