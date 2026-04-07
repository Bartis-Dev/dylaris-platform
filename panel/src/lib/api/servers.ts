import { API_URL, getAuthHeader, handleResponse, handleError } from './core';
import { Server } from './types'; // Using the interface from types.ts

export async function createServer(data: any) {
    try {
        const res = await fetch(`${API_URL}/servers`, {
            method: 'POST',
            headers: { ...Object.fromEntries(getAuthHeader()), 'Content-Type': 'application/json' },
            body: JSON.stringify(data)
        });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}

// NEU: Server abrufen
export async function getServers() {
    try {
        const res = await fetch(`${API_URL}/servers`, { headers: getAuthHeader() });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}