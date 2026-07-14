// Admin GET/PUT for the WS5 custom-tab reverse-proxy toggles.

import { API_URL, getAuthHeader, handleResponse, handleError } from '@/lib/api/core';

export interface TabProxySettings {
    enabled: boolean;
    allowPublicLinks: boolean;
    maxPerServer: number;
    maxShareLinksPerUser: number;
}

export async function getTabProxySettings(): Promise<{ success: boolean; settings?: TabProxySettings; message?: string }> {
    try {
        const res = await fetch(`${API_URL}/admin/settings/tab-proxy`, { headers: getAuthHeader() });
        return (await handleResponse(res)) as { success: boolean; settings?: TabProxySettings; message?: string };
    } catch (err) {
        return handleError(err) as { success: boolean; settings?: TabProxySettings; message?: string };
    }
}

export async function setTabProxySettings(payload: TabProxySettings): Promise<{ success: boolean; settings?: TabProxySettings; message?: string }> {
    try {
        const res = await fetch(`${API_URL}/admin/settings/tab-proxy`, {
            method: 'PUT',
            headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
            body: JSON.stringify(payload),
        });
        return (await handleResponse(res)) as { success: boolean; settings?: TabProxySettings; message?: string };
    } catch (err) {
        return handleError(err) as { success: boolean; settings?: TabProxySettings; message?: string };
    }
}
