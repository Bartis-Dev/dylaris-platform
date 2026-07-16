// panel client for panel-admin roles (instance-wide capability bundles),
// per-user panel-role assignment, and the permissions_mode write. Used by
// the panel-admin Settings tab.

import { API_URL, getAuthHeader, handleResponse, handleError } from '@/lib/api/core';
import type { PermissionsMode } from '@/lib/api/authzCatalog';

export interface PanelRole {
    id: number;
    name: string;
    capabilities: string[];
    isSystem: boolean;
}

export async function listPanelRoles(): Promise<{ success: boolean; roles?: PanelRole[]; message?: string }> {
    try {
        const res = await fetch(`${API_URL}/admin/panel-roles`, { headers: getAuthHeader() });
        return handleResponse(res) as any;
    } catch (err) { return handleError(err) as any; }
}

export async function createPanelRole(name: string, capabilities: string[]): Promise<{ success: boolean; role?: PanelRole; message?: string }> {
    try {
        const res = await fetch(`${API_URL}/admin/panel-roles`, {
            method: 'POST',
            headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
            body: JSON.stringify({ name, capabilities }),
        });
        return handleResponse(res) as any;
    } catch (err) { return handleError(err) as any; }
}

export async function updatePanelRole(id: number, name: string, capabilities: string[]): Promise<{ success: boolean; role?: PanelRole; message?: string }> {
    try {
        const res = await fetch(`${API_URL}/admin/panel-roles/${id}`, {
            method: 'PATCH',
            headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
            body: JSON.stringify({ name, capabilities }),
        });
        return handleResponse(res) as any;
    } catch (err) { return handleError(err) as any; }
}

export async function deletePanelRole(id: number): Promise<{ success: boolean; message?: string }> {
    try {
        const res = await fetch(`${API_URL}/admin/panel-roles/${id}`, {
            method: 'DELETE',
            headers: getAuthHeader(),
        });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}

export async function assignUserPanelRole(userId: string, panelRoleId: number | null, grantCaps: string[], denyCaps: string[]): Promise<{ success: boolean; message?: string }> {
    try {
        const res = await fetch(`${API_URL}/admin/users/${userId}/panel-role`, {
            method: 'PUT',
            headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
            body: JSON.stringify({ panelRoleId, grantCaps, denyCaps }),
        });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}

export async function getUserPanelRole(userId: string): Promise<{ success: boolean; panelRoleId?: number | null; grantCaps?: string[]; denyCaps?: string[]; message?: string }> {
    try {
        const res = await fetch(`${API_URL}/admin/users/${userId}/panel-role`, { headers: getAuthHeader() });
        return handleResponse(res) as any;
    } catch (err) { return handleError(err) as any; }
}

export async function setPermissionsMode(mode: PermissionsMode): Promise<{ success: boolean; message?: string }> {
    try {
        const res = await fetch(`${API_URL}/admin/settings/permissions-mode`, {
            method: 'PUT',
            headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
            body: JSON.stringify({ mode }),
        });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}
