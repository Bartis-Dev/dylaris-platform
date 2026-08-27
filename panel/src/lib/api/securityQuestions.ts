import { API_URL, getAuthHeader, handleResponse, handleError } from './core';

export interface SecurityQAItem {
    question: string;
    answer: string;
}

// getSecurityQuestionPool — public. Returns:
//   { enabled, pool, required }
// where `enabled=false` means the admin has not turned the feature on at all.
// The frontend uses this on /register and /reset-password to render pickers
// only when needed.
export async function getSecurityQuestionPool() {
    try {
        const res = await fetch(`${API_URL}/auth/security-questions/pool`);
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}

// getMySecurityQuestions — auth required. Returns just the question texts
// the current user has chosen — never the answers / hashes.
export async function getMySecurityQuestions() {
    try {
        const res = await fetch(`${API_URL}/me/security-questions`, { headers: getAuthHeader() });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}

// setMySecurityQuestions — auth required. Replaces the user's whole list.
// Backend validates each item is from the current pool + count matches policy.
// Core re-authenticates this one: these answers ARE the password-reset path,
// so overwriting them survives a password change and even revoking every API
// key. code is only required when the account has 2FA.
export async function setMySecurityQuestions(items: SecurityQAItem[], password: string, code?: string) {
    try {
        const res = await fetch(`${API_URL}/me/security-questions`, {
            method: 'PUT',
            headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
            body: JSON.stringify({ items, password, code }),
        });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}

// Admin pool management.

export async function getAdminSecurityQuestionPool() {
    try {
        const res = await fetch(`${API_URL}/admin/settings/security-questions-pool`, { headers: getAuthHeader() });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}

export async function setAdminSecurityQuestionPool(pool: string[]) {
    try {
        const res = await fetch(`${API_URL}/admin/settings/security-questions-pool`, {
            method: 'PUT',
            headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
            body: JSON.stringify({ pool }),
        });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}
