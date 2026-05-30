// Phase 14 — panel client for the modpack-authoring surface: per-user
// modpacks, their versions, and the mods that make up each version.

import { API_URL, getAuthHeader, handleResponse, handleError } from '@/lib/api/core';

export type ModpackChannel = 'draft' | 'beta' | 'release';

export interface Modpack {
    id: number;
    ownerId: string;
    name: string;
    slug: string;
    summary: string;
    mcVersion: string;
    loader: string;
    modrinthProjectId: string;
    modrinthVisibility: 'unlisted' | 'listed';
    createdAt: string;
    updatedAt: string;
}

export interface ModpackVersion {
    id: number;
    modpackId: number;
    versionString: string;
    channel: ModpackChannel;
    changelog: string;
    fileSize: number;
    modrinthVersionId: string;
    createdAt: string;
    publishedAt?: string;
    // Phase 16 / Wave A — version becomes frozen after the first publish or
    // export. Once frozen, the mods list is immutable; the only way to "edit"
    // is to create a new version.
    frozen?: boolean;
    // Storage key under the configured provider root. Empty until the version
    // is first persisted (publish or .mrpack export).
    mrpackStorageKey?: string;
    // SHA-256 of the persisted .mrpack — set at the same moment frozen flips.
    mrpackSHA256?: string;
}

export interface ModpackMod {
    id: number;
    modpackVersionId: number;
    modrinthProjectId: string;
    modrinthProjectSlug: string;
    modrinthVersionId: string;
    title: string;
    fileName: string;
    downloadUrl: string;
    sha512: string;
    side: 'client' | 'server' | 'both';
    required: boolean;
}

export interface CreateModpackInput {
    name: string;
    slug?: string;
    summary?: string;
    mcVersion?: string;
    loader?: string;
    modrinthVisibility?: 'unlisted' | 'listed';
}

export async function listModpacks(): Promise<Modpack[]> {
    try {
        const res = await fetch(`${API_URL}/me/modpacks`, { headers: getAuthHeader() });
        const data = await handleResponse(res);
        return (data as any).modpacks || [];
    } catch (err) { handleError(err); return []; }
}

export async function getModpack(id: number): Promise<Modpack | null> {
    try {
        const res = await fetch(`${API_URL}/modpacks/${id}`, { headers: getAuthHeader() });
        const data = await handleResponse(res);
        return (data as any).modpack || null;
    } catch (err) { handleError(err); return null; }
}

export async function createModpack(input: CreateModpackInput): Promise<{ success: boolean; modpack?: Modpack; message?: string }> {
    try {
        const res = await fetch(`${API_URL}/me/modpacks`, {
            method: 'POST',
            headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
            body: JSON.stringify(input),
        });
        return handleResponse(res) as any;
    } catch (err) { return handleError(err) as any; }
}

export async function updateModpack(id: number, input: Partial<CreateModpackInput>): Promise<{ success: boolean; message?: string }> {
    try {
        const res = await fetch(`${API_URL}/modpacks/${id}`, {
            method: 'PATCH',
            headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
            body: JSON.stringify(input),
        });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}

export async function deleteModpack(id: number): Promise<{ success: boolean; message?: string }> {
    try {
        const res = await fetch(`${API_URL}/modpacks/${id}`, {
            method: 'DELETE', headers: getAuthHeader(),
        });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}

// --- Versions ---

export async function listVersions(modpackId: number): Promise<ModpackVersion[]> {
    try {
        const res = await fetch(`${API_URL}/modpacks/${modpackId}/versions`, { headers: getAuthHeader() });
        const data = await handleResponse(res);
        return (data as any).versions || [];
    } catch (err) { handleError(err); return []; }
}

export async function createVersion(modpackId: number, payload: { versionString: string; channel?: ModpackChannel; changelog?: string }): Promise<{ success: boolean; version?: ModpackVersion; message?: string }> {
    try {
        const res = await fetch(`${API_URL}/modpacks/${modpackId}/versions`, {
            method: 'POST',
            headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
            body: JSON.stringify(payload),
        });
        return handleResponse(res) as any;
    } catch (err) { return handleError(err) as any; }
}

export async function deleteVersion(modpackId: number, versionId: number): Promise<{ success: boolean; message?: string }> {
    try {
        const res = await fetch(`${API_URL}/modpacks/${modpackId}/versions/${versionId}`, {
            method: 'DELETE', headers: getAuthHeader(),
        });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}

// --- Mods ---

export async function listMods(modpackId: number, versionId: number): Promise<ModpackMod[]> {
    try {
        const res = await fetch(`${API_URL}/modpacks/${modpackId}/versions/${versionId}/mods`, { headers: getAuthHeader() });
        const data = await handleResponse(res);
        return (data as any).mods || [];
    } catch (err) { handleError(err); return []; }
}

export async function addMod(modpackId: number, versionId: number, mod: {
    projectId: string;
    projectSlug?: string;
    versionId: string;
    title: string;
    fileName: string;
    downloadUrl: string;
    sha512?: string;
    side?: 'client' | 'server' | 'both';
    required?: boolean;
}): Promise<{ success: boolean; mod?: ModpackMod; message?: string }> {
    try {
        const res = await fetch(`${API_URL}/modpacks/${modpackId}/versions/${versionId}/mods`, {
            method: 'POST',
            headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
            body: JSON.stringify(mod),
        });
        return handleResponse(res) as any;
    } catch (err) { return handleError(err) as any; }
}

export async function removeMod(modpackId: number, versionId: number, modId: number): Promise<{ success: boolean; message?: string }> {
    try {
        const res = await fetch(`${API_URL}/modpacks/${modpackId}/versions/${versionId}/mods/${modId}`, {
            method: 'DELETE', headers: getAuthHeader(),
        });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}
