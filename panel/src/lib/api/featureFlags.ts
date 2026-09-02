// read the public feature-toggle map exposed by /api/system/features.
// Used by the AppDataContext to drive panel banners + UI gating.

import { API_URL, getAuthHeader, handleResponse, handleError } from '@/lib/api/core';

export interface FeatureFlags {
    // The modpack subsystem. True means it exists at all; on its own that means
    // admins can author.
    modpacks: boolean;
    // End-user authoring. Separate from `modpacks` so the UI can tell "authoring
    // is closed on this platform" apart from "modpacks are off entirely".
    modpackAuthoring: boolean;
    tickets: boolean;
    // Raw platform flag. The panel ANDs this with the live routing mode, since
    // auto-move is only effective while the gateway is on.
    autoMove: boolean;
    // BYON tenancy. Gates tenant-facing UI like the server transfer control.
    byon: boolean;
    // Store integration (dylaris.com). True only when the hosted Core has both
    // STORE_URL + STORE_SHARED_KEY set. Gates the connect-store button and the
    // demo account/server admin UI; false on a self-host/open-core build.
    store: boolean;
    // Gates the builder's share-link create UI. Existing links still show/copy/
    // revoke while modpacks is on; this only reflects the create toggle.
    shareLinks: boolean;
    // Whether modpack archives have anywhere to go. Separate from `modpacks`:
    // the subsystem can be switched on with no storage behind it, and until now
    // that only surfaced as an HTTP 424 at the end of building a pack.
    modpackStorage: boolean;
}

export interface FeatureFlagsAdminPayload {
    tickets: boolean;
    // The modpack subsystem. On its own it means admins can author.
    modpacks: boolean;
    // Opens authoring to non-admin users. Requires `modpacks`; the backend folds
    // it to false when the subsystem is off, so the two can never disagree.
    modpackAuthoring: boolean;
    // Write-only instruction, not stored state: what a change to
    // modpackAuthoring should do to users whose per-user flag an admin set BY
    // HAND. Omitted/false leaves those rows alone.
    applyAuthoringToManual?: boolean;
    autoMove: boolean;
    byon: boolean;
    // The long-term statistics record. Off by default: it starts writing
    // history meant to survive for years, which is a decision with a date on
    // it. Nothing leaves the installation either way - the platform sends
    // nothing anywhere and this does not change that.
    metrics: boolean;
    // Whether NON-ADMINS may hold an API key at all. Default off: a key is a
    // second credential class that outlives a session and is not covered by the
    // account's 2FA, so a fresh install does not start handing them out.
    userApiKeys: boolean;
    // Comma-separated capability ids a non-admin may put on a key. EMPTY MEANS
    // NO EXTRA RESTRICTION, not "none" - the backend already stops a key from
    // exceeding what its creator holds.
    userApiKeyAllowedCaps: string;
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

// Result of the admin PUT. usersChanged is how many per-user modpack rows the
// authoring toggle rewrote (0 when nothing needed changing, absent when the
// authoring flag did not move), so the UI can report the side effect instead of
// leaving the admin to guess what the switch touched.
export interface UpdateSystemFeaturesResult {
    success: boolean;
    features?: FeatureFlagsAdminPayload;
    usersChanged?: number;
    message?: string;
}

// Admin-only PUT — writes the bundle of platform-wide feature toggles in
// one round-trip. Backend invalidates the cache + publishes features.changed.
export async function updateSystemFeatures(payload: FeatureFlagsAdminPayload): Promise<UpdateSystemFeaturesResult> {
    try {
        const res = await fetch(`${API_URL}/admin/settings/features`, {
            method: 'PUT',
            headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
            body: JSON.stringify(payload),
        });
        return (await handleResponse(res)) as UpdateSystemFeaturesResult;
    } catch (err) {
        return handleError(err) as UpdateSystemFeaturesResult;
    }
}
