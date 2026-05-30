// Phase 16 — read the public feature-toggle map exposed by /api/system/features.
// Used by the AppDataContext to drive panel banners + UI gating.

import { API_URL, getAuthHeader, handleResponse, handleError } from '@/lib/api/core';

export interface FeatureFlags {
    modpacks: boolean;
}

export async function getSystemFeatures(): Promise<{ success: boolean; features?: FeatureFlags; message?: string }> {
    try {
        const res = await fetch(`${API_URL}/system/features`, { headers: getAuthHeader() });
        return (await handleResponse(res)) as any;
    } catch (err) {
        return handleError(err) as any;
    }
}
