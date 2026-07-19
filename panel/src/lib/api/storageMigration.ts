// Typed client for /api/admin/storage/*. Uses the core helpers directly
// (the coreStorage.ts pattern) rather than the @/lib/api barrel.
import { API_URL, getAuthHeader, handleResponse, handleError } from '@/lib/api/core';
import type {
  MigrationForm,
  StorageDataSet,
  StorageJobKind,
  StorageManifest,
  StorageMigrationJob,
  VerifyMode,
} from '@/lib/storageMigration';

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

// StorageTargetConfigBody mirrors services.StorageTargetConfig. s3SecretKey is
// write-only: it goes up with the start request and is never returned by any
// endpoint, so the wizard must always collect it fresh.
export interface StorageTargetConfigBody {
  backend: string;
  path?: string;
  pathConfirmed?: boolean;
  s3Endpoint?: string;
  s3Bucket?: string;
  s3Region?: string;
  s3AccessKey?: string;
  s3SecretKey?: string;
  s3PathStyle?: boolean;
  s3Prefix?: string;
}

export interface StartStorageMigrationBody {
  kind: StorageJobKind;
  dataSet: string;
  // Exactly one of these on a migrate job. The server rejects both or neither.
  targetDataSet?: string;
  targetConfig?: StorageTargetConfigBody;
  verifyMode?: VerifyMode;
  deleteSource?: boolean;
  manifestId?: number;
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

// startMigrateFromForm is the wizard's submit path. It sends EXACTLY ONE of
// targetDataSet / targetConfig (the server rejects both or neither with 400)
// and drops deleteSource when the verify mode cannot authorize it.
//
// For a path target the s3 fields are omitted entirely rather than sent empty,
// and for an s3 target the path fields are omitted, so a half-filled form from
// a backend the operator switched away from cannot travel with the request.
export function startMigrateFromForm(form: MigrationForm): StartStorageMigrationBody {
  const body: StartStorageMigrationBody = {
    kind: 'migrate',
    dataSet: form.dataSet,
    verifyMode: form.verifyMode,
    deleteSource: form.verifyMode === 'full' ? form.deleteSource : false,
  };
  if (form.targetKind === 'dataset') {
    body.targetDataSet = form.targetDataSet;
    return body;
  }
  const c = form.targetConfig;
  body.targetConfig = c.backend === 's3'
    ? {
        backend: 's3',
        s3Endpoint: c.s3Endpoint,
        s3Bucket: c.s3Bucket,
        s3Region: c.s3Region,
        s3AccessKey: c.s3AccessKey,
        s3SecretKey: c.s3SecretKey,
        s3PathStyle: c.s3PathStyle,
        s3Prefix: c.s3Prefix,
      }
    : { backend: 'path', path: c.path, pathConfirmed: c.pathConfirmed };
  return body;
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

export async function listStorageManifests(dataSet?: string): Promise<ListStorageManifestsResponse> {
  try {
    const qs = dataSet ? `?dataSet=${encodeURIComponent(dataSet)}` : '';
    const res = await fetch(`${API_URL}/admin/storage/manifests${qs}`, { headers: getAuthHeader() });
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

// manifestExportURL builds the CSV download URL. The export is a plain GET so
// the browser can stream a 100k-row file straight to disk; the tab fetches it
// with the auth header and hands the blob to a temporary anchor, because the
// endpoint is Bearer-authed and a bare <a href> would arrive unauthenticated.
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
    URL.revokeObjectURL(url);
    return { success: true };
  } catch (err) {
    return handleError(err) as { success: boolean; message?: string };
  }
}
