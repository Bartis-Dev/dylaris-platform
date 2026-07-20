// Typed client for GET /api/storage/connection. Returns the current
// reachability of both storage backends so a client that connects mid-outage
// has a starting state; live transitions arrive over the
// `storage.connection.changed` SSE event.
import { API_URL, getAuthHeader, handleResponse, handleError } from '@/lib/api/core';
import type { StorageConnectionResponse } from '@/lib/storageConnection';

export interface GetStorageConnectionResponse extends StorageConnectionResponse {
  success: boolean;
  message?: string;
}

export async function getStorageConnection(): Promise<GetStorageConnectionResponse> {
  try {
    const res = await fetch(`${API_URL}/storage/connection`, { headers: getAuthHeader() });
    return (await handleResponse(res)) as GetStorageConnectionResponse;
  } catch (err) {
    return handleError(err) as GetStorageConnectionResponse;
  }
}
