// Typed client for /api/settings/core-storage. GET never returns the stored
// S3 secret (the backend blanks it and relies on the "omitempty" json tag to
// drop it entirely); s3SecretSet tells the UI whether one is already stored
// server-side so it can render "(unchanged)" instead of asking the admin to
// re-enter a secret that's still there.
import { API_URL, getAuthHeader, handleResponse, handleError } from '@/lib/api/core';
import type { CoreStorageConfig } from '@/lib/coreStorage';

export interface GetCoreStorageResponse {
  success: boolean;
  settings?: CoreStorageConfig;
  message?: string;
  /**
   * How many Core instances are currently heartbeating. 0 means the server
   * could not take the count (Redis unreachable), NOT that no Core is running.
   */
  onlineCores?: number;
  /**
   * False when a second Core is online, which makes the filesystem backend
   * incorrect: it stores files on one machine's disk, so each Core would serve
   * only what it wrote itself. The server enforces this on save; this field
   * exists so the form can say so before the admin tries.
   */
  hostPathAllowed?: boolean;
  /**
   * Set only when the ALREADY-SAVED backend is a filesystem path and a second
   * Core has since appeared. The backend deliberately does not auto-switch, so
   * this warning is the operator's only signal. Render it persistently.
   */
  hostPathWarning?: string;
}

export async function getCoreStorage(): Promise<GetCoreStorageResponse> {
  try {
    const res = await fetch(`${API_URL}/settings/core-storage`, { headers: getAuthHeader() });
    return (await handleResponse(res)) as GetCoreStorageResponse;
  } catch (err) {
    return handleError(err) as GetCoreStorageResponse;
  }
}

export interface SaveCoreStorageResponse {
  success: boolean;
  message?: string;
}

export async function saveCoreStorage(s: CoreStorageConfig): Promise<SaveCoreStorageResponse> {
  try {
    const res = await fetch(`${API_URL}/settings/core-storage`, {
      method: 'POST',
      headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
      body: JSON.stringify(s),
    });
    return (await handleResponse(res)) as SaveCoreStorageResponse;
  } catch (err) {
    return handleError(err) as SaveCoreStorageResponse;
  }
}

export interface TestCoreStorageResponse {
  success: boolean;
  ok?: boolean;
  message?: string;
  /**
   * Set when the probe SUCCEEDED but the configured location is not durable -
   * today: a path on the container's own filesystem rather than a mounted
   * volume. It rides along with ok:true on purpose, because the write/read
   * test really did pass; it is the durability of what was written that is in
   * doubt. Render it somewhere persistent, not in a toast that vanishes.
   */
  warning?: string;
}

// testCoreStorage posts the CANDIDATE config (the current, possibly-unsaved
// form state) so the admin can verify a backend works before committing to
// it. The backend builds a provider straight from this request body (falling
// back to the stored config only for an empty body) and never persists
// anything - it just writes/reads/deletes a throwaway probe object.
export async function testCoreStorage(candidate: CoreStorageConfig): Promise<TestCoreStorageResponse> {
  try {
    const res = await fetch(`${API_URL}/settings/core-storage/test`, {
      method: 'POST',
      headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
      body: JSON.stringify(candidate),
    });
    return (await handleResponse(res)) as TestCoreStorageResponse;
  } catch (err) {
    return handleError(err) as TestCoreStorageResponse;
  }
}
