// read the public feature-toggle map exposed by /api/system/features.
// Used by the AppDataContext to drive panel banners + UI gating.

import { API_URL, getAuthHeader, handleResponse, handleError } from '@/lib/api/core';

export interface FeatureFlags {
    modpacks: boolean;
    tickets: boolean;
}

export interface FeatureFlagsAdminPayload {
    tickets: boolean;
    modpacks: boolean;
}

export async function getSystemFeatures(): Promise<{ success: boolean; features?: FeatureFlags; message?: string }> {
    try {
        const res = await fetch(`${API_URL}/system/features`, { headers: getAuthHeader() });
        return (await handleResponse(res)) as { success: boolean; features?: FeatureFlags; message?: string };
    } catch (err) {
        return handleError(err) as { success: boolean; features?: FeatureFlags; message?: string };
    }
}

// Admin-only GET — returns the same shape as the public /system/features map
// but with a body shape ready to round-trip back through PUT.
export async function getSystemFeaturesAdmin(): Promise<{ success: boolean; features?: FeatureFlagsAdminPayload; message?: string }> {
    try {
        const res = await fetch(`${API_URL}/admin/settings/features`, { headers: getAuthHeader() });
        return (await handleResponse(res)) as { success: boolean; features?: FeatureFlagsAdminPayload; message?: string };
    } catch (err) {
        return handleError(err) as { success: boolean; features?: FeatureFlagsAdminPayload; message?: string };
    }
}

// Admin-only PUT — writes the bundle of platform-wide feature toggles in
// one round-trip. Backend invalidates the cache + publishes features.changed.
export async function updateSystemFeatures(payload: FeatureFlagsAdminPayload): Promise<{ success: boolean; features?: FeatureFlagsAdminPayload; message?: string }> {
    try {
        const res = await fetch(`${API_URL}/admin/settings/features`, {
            method: 'PUT',
            headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
            body: JSON.stringify(payload),
        });
        return (await handleResponse(res)) as { success: boolean; features?: FeatureFlagsAdminPayload; message?: string };
    } catch (err) {
        return handleError(err) as { success: boolean; features?: FeatureFlagsAdminPayload; message?: string };
    }
}
