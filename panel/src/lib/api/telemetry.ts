import { API_URL, getAuthHeader, handleResponse, handleError } from '@/lib/api/core';

// TelemetrySettings is the wire shape for /api/admin/settings/telemetry.
// Endpoint is included so an operator could repoint to a self-hosted mirror,
// but the panel doesn't render it in V1 — the single boolean toggle is all
// the surface we expose.
export interface TelemetrySettings {
    enabled: boolean;
    endpoint: string;
}

export async function getTelemetrySettings(): Promise<{ success: boolean; settings?: TelemetrySettings; message?: string }> {
    try {
        const res = await fetch(`${API_URL}/admin/settings/telemetry`, { headers: getAuthHeader() });
        return (await handleResponse(res)) as { success: boolean; settings?: TelemetrySettings; message?: string };
    } catch (err) {
        return handleError(err) as { success: false; message: string };
    }
}

export async function setTelemetrySettings(s: TelemetrySettings): Promise<{ success: boolean; message?: string }> {
    try {
        const res = await fetch(`${API_URL}/admin/settings/telemetry`, {
            method: 'PUT',
            headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
            body: JSON.stringify(s),
        });
        return (await handleResponse(res)) as { success: boolean; message?: string };
    } catch (err) {
        return handleError(err) as { success: false; message: string };
    }
}
