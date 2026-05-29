// Phase 11 — panel client for Spark profile history. Profile start/stop
// itself goes through the existing /console/command endpoint; this file
// only covers the persistent history list + record-completion.

import { API_URL, getAuthHeader, handleResponse, handleError } from '@/lib/api/core';

export interface SparkProfile {
    id: number;
    serverId: number;
    subServerName: string;
    url: string;
    startedAt?: string;
    completedAt: string;
    requestedBy?: number;
}

// SPARK_URL_RE matches a spark.lucko.me share link in the console output.
// Spark prints these on profile completion in the form:
//   "Profile complete: https://spark.lucko.me/<id>"
export const SPARK_URL_RE = /https:\/\/spark\.lucko\.me\/[a-zA-Z0-9_-]+/;

export async function listSparkProfiles(serverId: number): Promise<SparkProfile[]> {
    try {
        const res = await fetch(`${API_URL}/servers/${serverId}/spark/profiles`, { headers: getAuthHeader() });
        const data = await handleResponse(res);
        return (data as any).profiles || [];
    } catch (err) { handleError(err); return []; }
}

export async function recordSparkProfile(serverId: number, url: string, startedAt?: string): Promise<{ success: boolean; message?: string }> {
    try {
        const res = await fetch(`${API_URL}/servers/${serverId}/spark/profiles`, {
            method: 'POST',
            headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
            body: JSON.stringify({ url, startedAt }),
        });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}

export async function deleteSparkProfile(serverId: number, profileId: number): Promise<{ success: boolean; message?: string }> {
    try {
        const res = await fetch(`${API_URL}/servers/${serverId}/spark/profiles/${profileId}`, {
            method: 'DELETE',
            headers: getAuthHeader(),
        });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}
