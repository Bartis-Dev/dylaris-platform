import { API_URL, getAuthHeader, handleResponse, handleError } from './core';

// One importable modpack advertised by an external Solder instance.
export interface SolderImportPack {
    slug: string;
    name: string;
}

async function postJSON(path: string, body: unknown) {
    try {
        const res = await fetch(`${API_URL}${path}`, {
            method: 'POST',
            headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
            body: JSON.stringify(body),
        });
        return handleResponse(res) as any;
    } catch (err) {
        return handleError(err) as any;
    }
}

// Reads the pack index of an external Solder instance (no downloads).
export const importSolderPreview = (url: string) =>
    postJSON('/me/packs/import-solder/preview', { url });

// Imports one modpack (all builds) from an external Solder instance.
export const importSolder = (url: string, slug: string) =>
    postJSON('/me/packs/import-solder', { url, slug });
