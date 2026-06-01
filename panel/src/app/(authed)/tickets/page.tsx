"use client";

import React, { useEffect, useState } from 'react';
import Link from 'next/link';
import { LifeBuoy, Plus, Filter, CircleDot, CircleCheckBig, Clock, AlertTriangle } from 'lucide-react';
import { listMyTickets, Ticket, TicketStatus, TicketPriority } from '@/lib/api/tickets';
import { SkeletonCard } from '@/components/Skeleton';

const STATUS_FILTERS: { id: TicketStatus | 'all' | 'open_active'; label: string }[] = [
    { id: 'open_active', label: 'Open' },
    { id: 'waiting_user', label: 'Waiting on you' },
    { id: 'resolved', label: 'Resolved' },
    { id: 'closed', label: 'Closed' },
    { id: 'all', label: 'All' },
];

export default function MyTicketsPage() {
    const [tickets, setTickets] = useState<Ticket[]>([]);
    const [loading, setLoading] = useState(true);
    const [filter, setFilter] = useState<typeof STATUS_FILTERS[number]['id']>('open_active');

    useEffect(() => {
        let cancelled = false;
        const load = async () => {
            setLoading(true);
            const query: { status?: TicketStatus[] } = {};
            if (filter === 'open_active') query.status = ['open', 'in_progress'];
            else if (filter === 'waiting_user') query.status = ['waiting_user'];
            else if (filter === 'resolved') query.status = ['resolved'];
            else if (filter === 'closed') query.status = ['closed'];
            const res = await listMyTickets(query);
            if (cancelled) return;
            if (res.success) setTickets(res.tickets || []);
            setLoading(false);
        };
        load();
        return () => { cancelled = true; };
    }, [filter]);

    return (
        <main className="flex-1 flex flex-col overflow-hidden p-6 gap-5">
            <header className="flex items-center justify-between gap-4 shrink-0">
                <div>
                    <h1 className="h-page flex items-center gap-2">
                        <LifeBuoy size={22} className="text-(--accent-light)" />
                        Tickets
                    </h1>
                    <p className="text-sm text-(--base-06) mt-1">Your open conversations with support.</p>
                </div>
                <Link href="/tickets/new" className="btn btn-primary inline-flex items-center gap-2">
                    <Plus size={14} /> New ticket
                </Link>
            </header>

            <div className="flex items-center gap-1.5 shrink-0 overflow-x-auto">
                <Filter size={14} className="text-(--base-06) shrink-0" />
                {STATUS_FILTERS.map(f => (
                    <button
                        key={f.id}
                        type="button"
                        onClick={() => setFilter(f.id)}
                        className={`px-3 py-1 rounded-md text-xs font-medium border whitespace-nowrap transition-colors ${
                            filter === f.id
                                ? 'border-(--accent-border) bg-(--accent-ghost) text-(--accent-light)'
                                : 'border-(--base-04) text-(--base-07) hover:bg-(--base-03)'
                        }`}
                    >
                        {f.label}
                    </button>
                ))}
            </div>

            <div className="flex-1 overflow-y-auto">
                {loading ? (
                    <div className="space-y-2 max-w-4xl">
                        {Array.from({ length: 5 }).map((_, i) => (
                            <SkeletonCard key={i} height="h-20" />
                        ))}
                    </div>
                ) : tickets.length === 0 ? (
                    <EmptyState filter={filter} />
                ) : (
                    <div className="space-y-2 max-w-4xl">
                        {tickets.map(t => <TicketRow key={t.id} ticket={t} />)}
                    </div>
                )}
            </div>
        </main>
    );
}

function TicketRow({ ticket }: { ticket: Ticket }) {
    return (
        <Link
            href={`/tickets/${ticket.id}`}
            className="card p-4 border border-(--base-03) hover:border-(--accent-border) hover:bg-(--base-03)/40 block transition-colors"
        >
            <div className="flex items-start justify-between gap-3">
                <div className="min-w-0">
                    <p className="text-sm font-medium text-(--base-09) truncate">{ticket.title}</p>
                    <p className="text-xs text-(--base-06) mt-0.5 font-mono">
                        #{ticket.id} · {ticket.categoryName || 'No category'}{ticket.serverName ? ` · ${ticket.serverName}` : ''}
                    </p>
                </div>
                <div className="flex items-center gap-2 shrink-0">
                    <StatusBadge status={ticket.status} />
                    <PriorityBadge priority={ticket.priority} />
                </div>
            </div>
            <div className="flex items-center justify-between gap-3 mt-2 text-xs text-(--base-06)">
                <span>Updated {timeAgo(ticket.updatedAt)}</span>
                {ticket.messageCount != null && ticket.messageCount > 0 && (
                    <span className="font-mono">{ticket.messageCount} message{ticket.messageCount === 1 ? '' : 's'}</span>
                )}
            </div>
        </Link>
    );
}

function StatusBadge({ status }: { status: TicketStatus }) {
    const map: Record<TicketStatus, { icon: typeof CircleDot; cls: string; label: string }> = {
        open:         { icon: CircleDot,     cls: 'bg-(--accent-ghost) text-(--accent-light) border-(--accent-border)', label: 'Open' },
        in_progress:  { icon: Clock,         cls: 'bg-(--warning-ghost) text-(--warning-light) border-(--warning)/30', label: 'In progress' },
        waiting_user: { icon: AlertTriangle, cls: 'bg-(--warning-ghost) text-(--warning-light) border-(--warning)/30', label: 'Waiting' },
        resolved:     { icon: CircleCheckBig,cls: 'bg-(--success-ghost) text-(--success-light) border-(--success)/30', label: 'Resolved' },
        closed:       { icon: CircleCheckBig,cls: 'bg-(--base-03) text-(--base-06) border-(--base-04)', label: 'Closed' },
    };
    const m = map[status] || map.open;
    const Icon = m.icon;
    return (
        <span className={`inline-flex items-center gap-1 px-1.5 py-0.5 rounded-sm text-[10px] font-mono uppercase tracking-[0.06em] border ${m.cls}`}>
            <Icon size={10} /> {m.label}
        </span>
    );
}

function PriorityBadge({ priority }: { priority: TicketPriority }) {
    const map: Record<TicketPriority, string> = {
        low:    'bg-(--base-03) text-(--base-06) border-(--base-04)',
        normal: 'bg-(--base-03) text-(--base-07) border-(--base-04)',
        high:   'bg-(--warning-ghost) text-(--warning-light) border-(--warning)/30',
        urgent: 'bg-(--error-ghost) text-(--error-light) border-(--error)/30',
    };
    return (
        <span className={`inline-flex items-center px-1.5 py-0.5 rounded-sm text-[10px] font-mono uppercase tracking-[0.06em] border ${map[priority] || map.normal}`}>
            {priority}
        </span>
    );
}

function EmptyState({ filter }: { filter: string }) {
    return (
        <div className="text-center py-16 max-w-md mx-auto">
            <LifeBuoy size={32} className="mx-auto text-(--base-06)" />
            <h2 className="font-display text-base mt-3">No tickets here</h2>
            <p className="text-sm text-(--base-06) mt-1">
                {filter === 'open_active'
                    ? 'You have no open tickets. Need help with something?'
                    : 'No tickets match this filter yet.'}
            </p>
            <Link href="/tickets/new" className="btn btn-primary inline-flex items-center gap-2 mt-4">
                <Plus size={14} /> Create a ticket
            </Link>
        </div>
    );
}

function timeAgo(iso: string): string {
    try {
        const diff = Date.now() - new Date(iso).getTime();
        const min = Math.floor(diff / 60000);
        if (min < 1) return 'just now';
        if (min < 60) return `${min}m ago`;
        const hr = Math.floor(min / 60);
        if (hr < 24) return `${hr}h ago`;
        const d = Math.floor(hr / 24);
        if (d < 30) return `${d}d ago`;
        const mo = Math.floor(d / 30);
        return `${mo}mo ago`;
    } catch { return ''; }
}
