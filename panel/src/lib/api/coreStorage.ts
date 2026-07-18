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
