import { API_URL, getAuthHeader, handleResponse, handleError } from './core';

export interface Notification {
    id: number;
    userId: string;
    type: string;
    title: string;
    body: string;
    link?: string;
    readAt?: string;
    createdAt: string;
}

export async function listNotifications(unreadOnly = false, limit = 50) {
    try {
        const params = new URLSearchParams();
        if (unreadOnly) params.set('unread_only', '1');
        if (limit) params.set('limit', String(limit));
        const res = await fetch(`${API_URL}/notifications?${params.toString()}`, { headers: getAuthHeader() });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}

export async function getUnreadCount() {
    try {
        const res = await fetch(`${API_URL}/notifications/unread-count`, { headers: getAuthHeader() });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}

export async function markNotificationRead(id: number) {
    try {
        const res = await fetch(`${API_URL}/notifications/${id}/read`, {
            method: 'POST',
            headers: getAuthHeader(),
        });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}

export async function markAllNotificationsRead() {
    try {
        const res = await fetch(`${API_URL}/notifications/read-all`, {
            method: 'POST',
            headers: getAuthHeader(),
        });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}
