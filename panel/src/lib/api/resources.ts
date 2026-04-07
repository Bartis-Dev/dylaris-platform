import { API_URL, getAuthHeader, handleResponse, handleError } from './core';
import { User, Node, AppModule } from './types';

// --- USERS ---
export async function getUsers() {
    try {
        const res = await fetch(`${API_URL}/users`, { headers: getAuthHeader() });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}

export async function createUser(user: Partial<User>) {
    try {
        const res = await fetch(`${API_URL}/users`, {
            method: 'POST',
            headers: { ...Object.fromEntries(getAuthHeader()), 'Content-Type': 'application/json' },
            body: JSON.stringify(user)
        });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}

export async function updateUser(id: number, user: Partial<User>) {
    try {
        const res = await fetch(`${API_URL}/users/${id}`, {
            method: 'PUT',
            headers: { ...Object.fromEntries(getAuthHeader()), 'Content-Type': 'application/json' },
            body: JSON.stringify(user)
        });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}

export async function deleteUser(id: number) {
    try {
        const res = await fetch(`${API_URL}/users/${id}`, { method: 'DELETE', headers: getAuthHeader() });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}

// --- NODES ---
export async function getNodes() {
    try {
        const res = await fetch(`${API_URL}/nodes`, { headers: getAuthHeader() });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}

export async function createNode(node: Partial<Node>) {
    try {
        const res = await fetch(`${API_URL}/nodes`, {
            method: 'POST',
            headers: { ...Object.fromEntries(getAuthHeader()), 'Content-Type': 'application/json' },
            body: JSON.stringify(node)
        });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}

// --- MODULES ---
export async function getModules() {
    try {
        const res = await fetch(`${API_URL}/modules`, { headers: getAuthHeader() });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}

export async function createModule(module: Partial<AppModule>) {
    try {
        const res = await fetch(`${API_URL}/modules`, {
            method: 'POST',
            headers: { ...Object.fromEntries(getAuthHeader()), 'Content-Type': 'application/json' },
            body: JSON.stringify(module)
        });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}

export async function deleteModule(id: number) {
    try {
        const res = await fetch(`${API_URL}/modules/${id}`, { method: 'DELETE', headers: getAuthHeader() });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}