import { API_URL, getAuthHeader, handleResponse, handleError } from './core';

export type TicketStatus = 'open' | 'in_progress' | 'waiting_user' | 'resolved' | 'closed';
export type TicketPriority = 'low' | 'normal' | 'high' | 'urgent';

export interface TicketCategory {
    id: number;
    name: string;
    description: string;
    requiresServer: boolean;
    defaultPriority: TicketPriority;
    defaultAssigneeTeam?: string;
    color?: string;
    enabled: boolean;
    position: number;
    createdAt?: string;
}

export interface Ticket {
    id: number;
    region: string;
    categoryId: number;
    categoryName?: string;
    userId: string;
    username?: string;
    serverUuid?: string;
    serverRegion?: string;
    serverName?: string;
    title: string;
    status: TicketStatus;
    priority: TicketPriority;
    assignedUserId?: string | null;
    assignedName?: string;
    assignedTeam?: string;
    createdAt: string;
    updatedAt: string;
    closedAt?: string | null;
    messageCount?: number;
}

export interface TicketMessage {
    id: number;
    ticketId: number;
    userId: string;
    username?: string;
    userRole?: string;
    body: string;
    isInternal: boolean;
    createdAt: string;
}

export interface TicketWatcher {
    ticketId: number;
    userId: string;
    username?: string;
    canReply: boolean;
    addedAt: string;
    addedBy?: string;
}

export interface TicketAuditEvent {
    id: number;
    ticketId: number;
    eventType: string;
    actorUserId?: string;
    actorName?: string;
    metadata?: Record<string, unknown>;
    createdAt: string;
}

export interface TicketSettings {
    crossTeamVisibility: boolean;
    watchersDefaultCanReply: boolean;
    allowUsersToAddWatchers: boolean;
    auditRetentionDays: number;
    maxFileSizeMb: number;
    maxTicketSizeMb: number;
    maxUserSizeMb: number;
    autoCloseEnabled: boolean;
    autoCloseDaysAfterResolved: number;
    // Admin-only DELETE /api/tickets/{id} gate. Default false. When false the
    // backend returns 403 deletion_disabled even for admins.
    deletionEnabled: boolean;
}

// TicketDeletion is one row of the admin deletion-log audit table. Snapshot
// fields stay populated even after the source ticket / owner / category have
// been removed.
export interface TicketDeletion {
    id: string;
    ticketId: number;
    ticketSubject: string;
    ownerUserId?: string;
    ownerUsername: string;
    categoryName?: string;
    deletedBy: string;
    deletedByName: string;
    deletedAt: string;
    ipAddress?: string;
    userAgent?: string;
}

export interface TicketAttachment {
    id: number;
    ticketId: number;
    messageId?: number;
    filename: string;
    mime: string;
    sizeBytes: number;
    uploadedBy?: string;
    username?: string;
    createdAt: string;
}

export interface CannedResponse {
    id: number;
    name: string;
    body: string;
    categoryId?: number;
    createdBy?: string;
    createdAt: string;
    updatedAt: string;
}

// ── Categories ──────────────────────────────────────────────────────

export async function listTicketCategories() {
    try {
        const res = await fetch(`${API_URL}/ticket-categories`, { headers: getAuthHeader() });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}

export async function adminListTicketCategories() {
    try {
        const res = await fetch(`${API_URL}/admin/ticket-categories`, { headers: getAuthHeader() });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}

export async function createTicketCategory(payload: Partial<TicketCategory>) {
    try {
        const res = await fetch(`${API_URL}/admin/ticket-categories`, {
            method: 'POST',
            headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
            body: JSON.stringify(payload),
        });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}

export async function updateTicketCategory(id: number, payload: Partial<TicketCategory>) {
    try {
        const res = await fetch(`${API_URL}/admin/ticket-categories/${id}`, {
            method: 'PATCH',
            headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
            body: JSON.stringify(payload),
        });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}

export async function deleteTicketCategory(id: number) {
    try {
        const res = await fetch(`${API_URL}/admin/ticket-categories/${id}`, {
            method: 'DELETE',
            headers: getAuthHeader(),
        });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}

// ── Tickets ─────────────────────────────────────────────────────────

interface ListTicketsQuery {
    status?: TicketStatus[];
    priority?: TicketPriority[];
    categoryId?: number;
    scope?: 'all' | 'mine' | 'team' | 'unassigned';
    limit?: number;
    offset?: number;
}

function buildQuery(q: ListTicketsQuery): string {
    const params = new URLSearchParams();
    if (q.status && q.status.length > 0) params.set('status', q.status.join(','));
    if (q.priority && q.priority.length > 0) params.set('priority', q.priority.join(','));
    if (q.categoryId != null) params.set('categoryId', String(q.categoryId));
    if (q.scope) params.set('scope', q.scope);
    if (q.limit != null) params.set('limit', String(q.limit));
    if (q.offset != null) params.set('offset', String(q.offset));
    const s = params.toString();
    return s ? `?${s}` : '';
}

export async function listMyTickets(query: ListTicketsQuery = {}) {
    try {
        const res = await fetch(`${API_URL}/tickets${buildQuery(query)}`, { headers: getAuthHeader() });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}

export async function listInboxTickets(query: ListTicketsQuery = {}) {
    try {
        const res = await fetch(`${API_URL}/tickets/inbox${buildQuery(query)}`, { headers: getAuthHeader() });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}

export async function getTicket(id: number) {
    try {
        const res = await fetch(`${API_URL}/tickets/${id}`, { headers: getAuthHeader() });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}

export interface CreateTicketPayload {
    categoryId: number;
    serverUuid?: string;
    serverRegion?: string;
    title: string;
    firstMessage: string;
    priority?: TicketPriority;
}

export async function createTicket(payload: CreateTicketPayload) {
    try {
        const res = await fetch(`${API_URL}/tickets`, {
            method: 'POST',
            headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
            body: JSON.stringify(payload),
        });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}

export async function addTicketReply(id: number, body: string, isInternal = false) {
    try {
        const res = await fetch(`${API_URL}/tickets/${id}/messages`, {
            method: 'POST',
            headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
            body: JSON.stringify({ body, isInternal }),
        });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}

export async function setTicketStatus(id: number, status: TicketStatus) {
    try {
        const res = await fetch(`${API_URL}/tickets/${id}/status`, {
            method: 'PATCH',
            headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
            body: JSON.stringify({ status }),
        });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}

export async function setTicketAssignment(id: number, payload: { assignedUserId?: string | null; assignedTeam?: string }) {
    try {
        const res = await fetch(`${API_URL}/tickets/${id}/assignment`, {
            method: 'PATCH',
            headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
            body: JSON.stringify(payload),
        });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}

export async function addTicketWatcher(id: number, payload: { userId?: string; username?: string; canReply?: boolean }) {
    try {
        const res = await fetch(`${API_URL}/tickets/${id}/watchers`, {
            method: 'POST',
            headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
            body: JSON.stringify(payload),
        });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}

export async function removeTicketWatcher(id: number, userId: string) {
    try {
        const res = await fetch(`${API_URL}/tickets/${id}/watchers/${userId}`, {
            method: 'DELETE',
            headers: getAuthHeader(),
        });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}

// deleteTicket DELETE /api/tickets/{id} — admin only, gated by
// tickets.deletionEnabled. The backend stamps the audit log BEFORE the
// cascade so a failed delete is still observable.
export async function deleteTicket(id: number) {
    try {
        const res = await fetch(`${API_URL}/tickets/${id}`, {
            method: 'DELETE',
            headers: getAuthHeader(),
        });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}

// listTicketDeletions GET /api/admin/tickets/deletion-log — paginated.
// deletedBy is a UUID filter; pass nothing or empty string for no filter.
export interface ListTicketDeletionsQuery {
    limit?: number;
    offset?: number;
    deletedBy?: string;
}

export async function listTicketDeletions(opts: ListTicketDeletionsQuery = {}) {
    try {
        const params = new URLSearchParams();
        if (opts.limit != null) params.set('limit', String(opts.limit));
        if (opts.offset != null) params.set('offset', String(opts.offset));
        if (opts.deletedBy) params.set('deletedBy', opts.deletedBy);
        const q = params.toString();
        const res = await fetch(`${API_URL}/admin/tickets/deletion-log${q ? `?${q}` : ''}`, { headers: getAuthHeader() });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}

export async function listServersViaTickets() {
    try {
        const res = await fetch(`${API_URL}/me/servers/via-tickets`, { headers: getAuthHeader() });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}

// ── Settings ────────────────────────────────────────────────────────

export async function getTicketSettings() {
    try {
        const res = await fetch(`${API_URL}/admin/settings/tickets`, { headers: getAuthHeader() });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}

export async function saveTicketSettings(payload: TicketSettings) {
    try {
        const res = await fetch(`${API_URL}/admin/settings/tickets`, {
            method: 'PUT',
            headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
            body: JSON.stringify(payload),
        });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}

// ── Attachments ──────────────────────────────────────────

export async function listTicketAttachments(ticketId: number) {
    try {
        const res = await fetch(`${API_URL}/tickets/${ticketId}/attachments`, { headers: getAuthHeader() });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}

// uploadTicketAttachment uses multipart/form-data. messageId is optional —
// when present the backend links the attachment to that message.
export async function uploadTicketAttachment(ticketId: number, file: File, messageId?: number) {
    try {
        const form = new FormData();
        form.append('file', file);
        if (messageId != null) form.append('messageId', String(messageId));
        const res = await fetch(`${API_URL}/tickets/${ticketId}/attachments`, {
            method: 'POST',
            headers: getAuthHeader(), // don't set Content-Type — browser writes the multipart boundary
            body: form,
        });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}

export function downloadTicketAttachmentURL(ticketId: number, attachmentId: number): string {
    // Direct anchor link — auth header travels in cookies on browser request.
    // Token also goes in querystring for SSE-style endpoints; download is a
    // simple GET so we rely on the Bearer the AuthMiddleware also accepts.
    const token = (typeof window !== 'undefined'
        && (localStorage.getItem('authToken') || localStorage.getItem('token'))) || '';
    return `${API_URL}/tickets/${ticketId}/attachments/${attachmentId}/download?token=${encodeURIComponent(token)}`;
}

export async function deleteTicketAttachment(ticketId: number, attachmentId: number) {
    try {
        const res = await fetch(`${API_URL}/tickets/${ticketId}/attachments/${attachmentId}`, {
            method: 'DELETE',
            headers: getAuthHeader(),
        });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}

// ── Canned responses ─────────────────────────────────────

export async function listCannedResponses(categoryId?: number) {
    try {
        const url = new URL(`${API_URL}/ticket-canned-responses`);
        if (categoryId != null) url.searchParams.set('categoryId', String(categoryId));
        const res = await fetch(url.toString(), { headers: getAuthHeader() });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}

export async function adminListCannedResponses() {
    try {
        const res = await fetch(`${API_URL}/admin/ticket-canned-responses`, { headers: getAuthHeader() });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}

export async function createCannedResponse(payload: { name: string; body: string; categoryId?: number | null }) {
    try {
        const res = await fetch(`${API_URL}/admin/ticket-canned-responses`, {
            method: 'POST',
            headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
            body: JSON.stringify(payload),
        });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}

export async function updateCannedResponse(id: number, payload: { name: string; body: string; categoryId?: number | null }) {
    try {
        const res = await fetch(`${API_URL}/admin/ticket-canned-responses/${id}`, {
            method: 'PATCH',
            headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
            body: JSON.stringify(payload),
        });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}

export async function deleteCannedResponse(id: number) {
    try {
        const res = await fetch(`${API_URL}/admin/ticket-canned-responses/${id}`, {
            method: 'DELETE',
            headers: getAuthHeader(),
        });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}

// expandCannedTemplate substitutes the small variable set we support:
//   {{user_name}}     — the ticket creator's username
//   {{ticket_id}}     — the ticket id
//   {{server_name}}   — the attached server name (or empty)
//   {{actor_name}}    — the current user (often = the support replying)
export function expandCannedTemplate(body: string, ctx: {
    userName?: string;
    ticketId?: number;
    serverName?: string;
    actorName?: string;
}): string {
    return body
        .replace(/\{\{user_name\}\}/g, ctx.userName ?? '')
        .replace(/\{\{ticket_id\}\}/g, ctx.ticketId != null ? String(ctx.ticketId) : '')
        .replace(/\{\{server_name\}\}/g, ctx.serverName ?? '')
        .replace(/\{\{actor_name\}\}/g, ctx.actorName ?? '');
}
