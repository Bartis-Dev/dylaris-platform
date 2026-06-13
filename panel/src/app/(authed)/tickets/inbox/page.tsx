"use client";

import React, { useEffect, useState } from 'react';
import Link from 'next/link';
import { Inbox, Filter, ShieldAlert, Globe, Check } from 'lucide-react';
import { listInboxTickets, listTicketCategories, Ticket, TicketStatus, TicketCategory } from '@/lib/api/tickets';
import { useAppData } from '@/lib/AppDataContext';
import RegionBadge from '@/components/RegionBadge';
import { SkeletonCard } from '@/components/Skeleton';
import TicketsDisabledBanner from '@/components/tickets/TicketsDisabledBanner';

type Scope = 'all' | 'mine' | 'team' | 'unassigned';
const SCOPES: { id: Scope; label: string }[] = [
    { id: 'all',        label: 'All' },
    { id: 'mine',       label: 'Assigned to me' },
    { id: 'team',       label: 'My team' },
    { id: 'unassigned', label: 'Unassigned' },
];

const STATUS_FILTERS: { id: 'open_active' | 'waiting_user' | 'resolved' | 'closed' | 'all'; label: string; statuses?: TicketStatus[] }[] = [
    { id: 'open_active',  label: 'Active',       statuses: ['open', 'in_progress'] },
    { id: 'waiting_user', label: 'Waiting user', statuses: ['waiting_user'] },
    { id: 'resolved',     label: 'Resolved',     statuses: ['resolved'] },
    { id: 'closed',       label: 'Closed',       statuses: ['closed'] },
    { id: 'all',          label: 'All' },
];

export default function InboxPage() {
    const { user, regions, featureFlags } = useAppData();
    const [scope, setScope] = useState<Scope>('all');
    const [status, setStatus] = useState<typeof STATUS_FILTERS[number]['id']>('open_active');
    const [categoryId, setCategoryId] = useState<number | ''>('');
    const [tickets, setTickets] = useState<Ticket[]>([]);
    const [categories, setCategories] = useState<TicketCategory[]>([]);
    const [loading, setLoading] = useState(true);
    // client-side region multi-select. The list endpoint returns
    // every ticket the user can see; we trim by region locally so the
    // filter is instant and doesn't need a server round-trip.
    const [regionFilter, setRegionFilter] = useState<Set<string>>(new Set());
    const [showRegionMenu, setShowRegionMenu] = useState(false);
    const multiRegion = regions.length > 1;

    const isSupport = user?.role === 'support' || user?.isAdmin;

    useEffect(() => {
        listTicketCategories().then(res => {
            if (res.success) setCategories(res.categories || []);
        });
    }, []);

    useEffect(() => {
        if (!isSupport || !featureFlags.tickets) {
            setLoading(false);
            return;
        }
        let cancelled = false;
        (async () => {
            setLoading(true);
            const statusFilter = STATUS_FILTERS.find(s => s.id === status);
            const res = await listInboxTickets({
                scope,
                status: statusFilter?.statuses,
                categoryId: categoryId === '' ? undefined : Number(categoryId),
            });
            if (cancelled) return;
            if (res.success) setTickets(res.tickets || []);
            setLoading(false);
        })();
        return () => { cancelled = true; };
    }, [scope, status, categoryId, isSupport, featureFlags.tickets]);

    if (!featureFlags.tickets) return <TicketsDisabledBanner />;

    if (!isSupport) {
        return (
            <main className="flex-1 flex items-center justify-center p-6 text-(--base-06)">
                <div className="text-center max-w-md">
                    <ShieldAlert size={28} className="mx-auto text-(--error-light)" />
                    <p className="mt-3 text-sm">The inbox is reserved for support and admin roles.</p>
                </div>
            </main>
        );
    }

    return (
        <main className="flex-1 flex flex-col overflow-hidden p-6 gap-4">
            <header className="shrink-0">
                <h1 className="h-page flex items-center gap-2">
                    <Inbox size={22} className="text-(--accent-light)" />
                    Support inbox
                </h1>
                <p className="text-sm text-(--base-06) mt-1">All tickets you can see across the platform.</p>
            </header>

            <div className="shrink-0 space-y-2">
                <div className="flex items-center gap-1.5 overflow-x-auto">
                    {SCOPES.map(s => (
                        <button
                            key={s.id}
                            type="button"
                            onClick={() => setScope(s.id)}
                            className={`px-3 py-1 rounded-md text-xs font-medium border whitespace-nowrap transition-colors ${
                                scope === s.id
                                    ? 'border-(--accent-border) bg-(--accent-ghost) text-(--accent-light)'
                                    : 'border-(--base-04) text-(--base-07) hover:bg-(--base-03)'
                            }`}
                        >
                            {s.label}
                        </button>
                    ))}
                </div>
                <div className="flex items-center gap-2 flex-wrap">
                    <Filter size={14} className="text-(--base-06)" />
                    {STATUS_FILTERS.map(f => (
                        <button
                            key={f.id}
                            type="button"
                            onClick={() => setStatus(f.id)}
                            className={`px-2.5 py-0.5 rounded-md text-[11px] font-medium border whitespace-nowrap transition-colors ${
                                status === f.id
                                    ? 'border-(--base-06) bg-(--base-03) text-(--base-09)'
                                    : 'border-(--base-04) text-(--base-07) hover:bg-(--base-03)'
                            }`}
                        >
                            {f.label}
                        </button>
                    ))}
                    <select
                        value={categoryId}
                        onChange={e => setCategoryId(e.target.value ? Number(e.target.value) : '')}
                        className="input-field text-xs py-1 ml-auto w-48"
                    >
                        <option value="">All categories</option>
                        {categories.map(c => <option key={c.id} value={c.id}>{c.name}</option>)}
                    </select>
                    {multiRegion && (
                        <div className="relative">
                            <button
                                type="button"
                                onClick={() => setShowRegionMenu(o => !o)}
                                className={`inline-flex items-center gap-1.5 px-2.5 py-1 rounded-md text-[11px] font-medium border whitespace-nowrap transition-colors ${
                                    regionFilter.size > 0
                                        ? 'border-(--accent-border) bg-(--accent-ghost) text-(--accent-light)'
                                        : 'border-(--base-04) text-(--base-07) hover:bg-(--base-03)'
                                }`}
                            >
                                <Globe size={12} />
                                Region
                                {regionFilter.size > 0 && (
                                    <span className="ml-0.5 px-1 rounded-sm text-[10px] font-mono bg-(--accent-ghost)/60">
                                        {regionFilter.size}
                                    </span>
                                )}
                            </button>
                            {showRegionMenu && (
                                <div
                                    className="absolute right-0 top-full mt-1.5 z-20 min-w-[180px] rounded-md border border-(--base-04) bg-(--base-02) shadow-lg p-1"
                                    onMouseLeave={() => setShowRegionMenu(false)}
                                >
                                    {regions.map(r => {
                                        const selected = regionFilter.has(r.id);
                                        return (
                                            <button
                                                key={r.id}
                                                onClick={() => setRegionFilter(prev => {
                                                    const next = new Set(prev);
                                                    if (next.has(r.id)) next.delete(r.id); else next.add(r.id);
                                                    return next;
                                                })}
                                                className={`w-full flex items-center gap-2 px-2.5 py-1.5 rounded-sm text-xs text-left transition-colors ${
                                                    selected ? 'bg-(--accent-ghost) text-(--accent-light)' : 'text-(--base-07) hover:bg-(--base-03)'
                                                }`}
                                            >
                                                <span
                                                    className="inline-block w-2 h-2 rounded-full shrink-0"
                                                    style={{ background: r.color || 'var(--base-05)' }}
                                                />
                                                <span className="truncate flex-1">{r.displayName || r.id}</span>
                                                {selected && <Check size={12} className="shrink-0" />}
                                            </button>
                                        );
                                    })}
                                    {regionFilter.size > 0 && (
                                        <button
                                            onClick={() => setRegionFilter(new Set())}
                                            className="w-full mt-1 pt-1.5 border-t border-(--base-03) px-2.5 py-1 text-[10px] font-mono uppercase tracking-[0.08em] text-(--base-06) hover:text-(--base-09) transition-colors text-left"
                                        >
                                            Clear filter
                                        </button>
                                    )}
                                </div>
                            )}
                        </div>
                    )}
                </div>
            </div>

            <div className="flex-1 overflow-y-auto">
                {loading ? (
                    <div className="space-y-2">
                        {Array.from({ length: 6 }).map((_, i) => (
                            <SkeletonCard key={i} height="h-16" />
                        ))}
                    </div>
                ) : (() => {
                    const filtered = regionFilter.size > 0
                        ? tickets.filter(t => regionFilter.has(t.region || 'default'))
                        : tickets;
                    if (filtered.length === 0) {
                        return (
                            <div className="text-center py-16 text-(--base-06)">
                                <Inbox size={28} className="mx-auto" />
                                <p className="mt-2 text-sm">No tickets match this filter.</p>
                            </div>
                        );
                    }
                    return (
                        <div className="space-y-2">
                            {filtered.map(t => <InboxRow key={t.id} ticket={t} />)}
                        </div>
                    );
                })()}
            </div>
        </main>
    );
}

function InboxRow({ ticket }: { ticket: Ticket }) {
    const priorityCls =
        ticket.priority === 'urgent' ? 'bg-(--error-ghost) text-(--error-light) border-(--error)/30'
        : ticket.priority === 'high' ? 'bg-(--warning-ghost) text-(--warning-light) border-(--warning)/30'
        : 'bg-(--base-03) text-(--base-07) border-(--base-04)';

    return (
        <Link
            href={`/tickets/${ticket.id}`}
            className="card p-3 border border-(--base-03) hover:border-(--accent-border) hover:bg-(--base-03)/40 block transition-colors"
        >
            <div className="flex items-start justify-between gap-3">
                <div className="min-w-0 flex-1">
                    <p className="text-sm font-medium text-(--base-09) truncate">{ticket.title}</p>
                    <p className="text-xs text-(--base-06) mt-0.5 font-mono">
                        #{ticket.id} · {ticket.categoryName} · {ticket.username}
                        {ticket.assignedName && <> · assigned: <span className="text-(--accent-light)">{ticket.assignedName}</span></>}
                        {ticket.assignedTeam && !ticket.assignedName && <> · team: {ticket.assignedTeam}</>}
                    </p>
                </div>
                <div className="flex items-center gap-1.5 shrink-0">
                    <RegionBadge region={ticket.region} />
                    <span className={`inline-flex items-center px-1.5 py-0.5 rounded-sm text-[10px] font-mono uppercase tracking-[0.06em] border ${priorityCls}`}>
                        {ticket.priority}
                    </span>
                    <span className="inline-flex items-center px-1.5 py-0.5 rounded-sm text-[10px] font-mono uppercase tracking-[0.06em] border bg-(--accent-ghost) text-(--accent-light) border-(--accent-border)">
                        {ticket.status.replace('_', ' ')}
                    </span>
                </div>
            </div>
        </Link>
    );
}
