// Typed client for /api/admin/storage/*. Uses the core helpers directly
// (the coreStorage.ts pattern) rather than the @/lib/api barrel.
import { API_URL, getAuthHeader, handleResponse, handleError } from '@/lib/api/core';
import {
  startMigrateFromForm,
  type StorageDataSet,
  type StorageManifest,
  type StorageMigrationJob,
  type StorageTargetConfigBody,
  type StartStorageMigrationBody,
} from '@/lib/storageMigration';

// startMigrateFromForm and the two body interfaces it builds live in
// storageMigration.ts (not here) so they sit next to MigrationForm and get
// exercised by the plain vitest suite there instead of needing DOM/fetch
// mocks. Re-exported so this client's existing consumers are unaffected.
export { startMigrateFromForm };
export type { StorageTargetConfigBody, StartStorageMigrationBody };

export interface StorageOverviewResponse {
  success: boolean;
  dataSets?: StorageDataSet[];
  message?: string;
}

export async function getStorageOverview(): Promise<StorageOverviewResponse> {
  try {
    const res = await fetch(`${API_URL}/admin/storage/overview`, { headers: getAuthHeader() });
    return (await handleResponse(res)) as StorageOverviewResponse;
  } catch (err) {
    return handleError(err) as StorageOverviewResponse;
  }
}

export interface StorageMigrationJobResponse {
  success: boolean;
  hasJob?: boolean;
  job?: StorageMigrationJob;
  message?: string;
}

export async function getStorageMigration(): Promise<StorageMigrationJobResponse> {
  try {
    const res = await fetch(`${API_URL}/admin/storage/migration`, { headers: getAuthHeader() });
    return (await handleResponse(res)) as StorageMigrationJobResponse;
  } catch (err) {
    return handleError(err) as StorageMigrationJobResponse;
  }
}

export async function startStorageMigration(body: StartStorageMigrationBody): Promise<StorageMigrationJobResponse> {
  try {
    const res = await fetch(`${API_URL}/admin/storage/migration`, {
      method: 'POST',
      headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    });
    return (await handleResponse(res)) as StorageMigrationJobResponse;
  } catch (err) {
    return handleError(err) as StorageMigrationJobResponse;
  }
}

export async function cancelStorageMigration(): Promise<{ success: boolean; message?: string }> {
  try {
    const res = await fetch(`${API_URL}/admin/storage/migration/cancel`, {
      method: 'POST',
      headers: getAuthHeader(),
    });
    return (await handleResponse(res)) as { success: boolean; message?: string };
  } catch (err) {
    return handleError(err) as { success: boolean; message?: string };
  }
}

export interface ListStorageManifestsResponse {
  success: boolean;
  manifests?: StorageManifest[];
  message?: string;
}

// listStorageManifests leaves limit unset by default; the handler's
// PostgresStore.ListStorageManifests then falls back to 50. Pass any positive
// value to raise that; the store applies no upper cap. A non-positive limit
// hits the same fallback, so it cannot be used to ask for fewer.
export async function listStorageManifests(dataSet?: string, limit?: number): Promise<ListStorageManifestsResponse> {
  try {
    const params = new URLSearchParams();
    if (dataSet) params.set('dataSet', dataSet);
    if (limit !== undefined) params.set('limit', String(limit));
    const qs = params.toString();
    const res = await fetch(`${API_URL}/admin/storage/manifests${qs ? `?${qs}` : ''}`, { headers: getAuthHeader() });
    return (await handleResponse(res)) as ListStorageManifestsResponse;
  } catch (err) {
    return handleError(err) as ListStorageManifestsResponse;
  }
}

export async function deleteStorageManifest(id: number): Promise<{ success: boolean; message?: string }> {
  try {
    const res = await fetch(`${API_URL}/admin/storage/manifests/${id}`, {
      method: 'DELETE',
      headers: getAuthHeader(),
    });
    return (await handleResponse(res)) as { success: boolean; message?: string };
  } catch (err) {
    return handleError(err) as { success: boolean; message?: string };
  }
}

// manifestExportURL builds the CSV download URL. The endpoint accepts a
// ?token= query param too (AuthMiddleware allows that on GET for SSE +
// downloads), but the tab fetches with the Authorization header and hands the
// blob to a temporary anchor instead of using a bare <a href>, so the session
// JWT never ends up in a URL, a Referer header or an access log.
export function manifestExportURL(id: number): string {
  return `${API_URL}/admin/storage/manifests/${id}/export`;
}

export async function downloadManifestCSV(id: number, dataSet: string): Promise<{ success: boolean; message?: string }> {
  try {
    const res = await fetch(manifestExportURL(id), { headers: getAuthHeader() });
    if (!res.ok) {
      return { success: false, message: `Export failed (HTTP ${res.status})` };
    }
    const blob = await res.blob();
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = `dylaris-manifest-${id}-${dataSet.replace(/[^a-zA-Z0-9_-]/g, '-')}.csv`;
    document.body.appendChild(a);
    a.click();
    a.remove();
    // Deferred: revoking synchronously right after click() can abort the
    // download in some browsers before they finish reading the blob URL.
    setTimeout(() => URL.revokeObjectURL(url), 0);
    return { success: true };
  } catch (err) {
    return handleError(err) as { success: boolean; message?: string };
  }
}
