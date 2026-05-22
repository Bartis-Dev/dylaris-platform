import { API_URL, getAuthHeader, handleResponse, handleError } from './core';

export async function listOrphanFiles(nodeId: number, uuid: string, path: string) {
  try {
    const res = await fetch(
      `${API_URL}/disk/orphans/${nodeId}/${encodeURIComponent(uuid)}/files?path=${encodeURIComponent(path)}`,
      { headers: getAuthHeader() },
    );
    return handleResponse(res);
  } catch (err) {
    return handleError(err);
  }
}

export async function getOrphanFileContent(nodeId: number, uuid: string, path: string) {
  try {
    const res = await fetch(
      `${API_URL}/disk/orphans/${nodeId}/${encodeURIComponent(uuid)}/content?path=${encodeURIComponent(path)}`,
      { headers: getAuthHeader() },
    );
    return handleResponse(res);
  } catch (err) {
    return handleError(err);
  }
}

export async function inspectOrphan(nodeId: number, uuid: string) {
  try {
    const res = await fetch(
      `${API_URL}/disk/orphans/${nodeId}/${encodeURIComponent(uuid)}/inspect`,
      { headers: getAuthHeader() },
    );
    return handleResponse(res);
  } catch (err) {
    return handleError(err);
  }
}

export interface AssignOrphanInput {
  node_id: number;
  uuid: string;
  name: string;
  owner_user_id?: number;
  new_user?: { username: string; password: string };
  memory_mb: number;
  cpu_limit: number;
}

export async function assignOrphan(input: AssignOrphanInput) {
  try {
    const res = await fetch(`${API_URL}/disk/orphans/assign`, {
      method: 'POST',
      headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
      body: JSON.stringify(input),
    });
    return handleResponse(res);
  } catch (err) {
    return handleError(err);
  }
}
