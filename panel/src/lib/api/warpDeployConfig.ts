// What a BYON or route-only deploy snippet still needs from Core.
//
// The two overlay addresses used to be here as well. They now go straight to
// the machine's warp, which proxies them on fixed local ports, so no snippet
// carries an address any more and none has to be re-copied when the platform
// overlay is rebuilt.

import { API_URL, getAuthHeader, handleResponse, handleError } from '@/lib/api/core';

export interface WarpDeployConfig {
    /** Stored overlay CIDR(s), or Core's detected value. "" = undetermined. */
    tunnelSubnets: string;
}

export async function getWarpDeployConfig(): Promise<{ success: boolean; config?: WarpDeployConfig; message?: string }> {
    try {
        const res = await fetch(`${API_URL}/warp/deploy-config`, { headers: getAuthHeader() });
        return (await handleResponse(res)) as { success: boolean; config?: WarpDeployConfig; message?: string };
    } catch (err) {
        return handleError(err) as { success: boolean; message?: string };
    }
}
