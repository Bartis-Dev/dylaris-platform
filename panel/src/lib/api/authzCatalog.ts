// panel client for the authz catalog (grouped scope->category->cap), the
// code-defined preset registry, and the permissions_mode read. These are the
// single source of truth every permission-picking UI renders from - no
// hardcoded frontend permission arrays.

import { API_URL, getAuthHeader, handleResponse, handleError } from '@/lib/api/core';

export interface CatalogCapability {
    id: string;
    label: string;
    verb: string;
}

export interface CatalogCategory {
    category: string;
    capabilities: CatalogCapability[];
}

export interface CatalogScope {
    scope: string;
    categories: CatalogCategory[];
}

export interface Preset {
    id: string;
    label: string;
    description: string;
    capabilities: string[];
}

export type PermissionsMode = 'off' | 'simple' | 'advanced';

export async function getCatalog(): Promise<{ success: boolean; catalog?: CatalogScope[]; message?: string }> {
    try {
        const res = await fetch(`${API_URL}/authz/catalog`, { headers: getAuthHeader() });
        return handleResponse(res) as any;
    } catch (err) { return handleError(err) as any; }
}

export async function getPresets(): Promise<{ success: boolean; presets?: Preset[]; message?: string }> {
    try {
        const res = await fetch(`${API_URL}/authz/presets`, { headers: getAuthHeader() });
        return handleResponse(res) as any;
    } catch (err) { return handleError(err) as any; }
}

export async function getPermissionsMode(): Promise<{ success: boolean; mode?: PermissionsMode; message?: string }> {
    try {
        const res = await fetch(`${API_URL}/authz/mode`, { headers: getAuthHeader() });
        return handleResponse(res) as any;
    } catch (err) { return handleError(err) as any; }
}
