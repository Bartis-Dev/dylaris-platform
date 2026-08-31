"use client";

import React, { useCallback, useEffect, useState } from 'react';

import { FileText, Filter, Loader2, ShieldCheck, ShieldOff, AlertTriangle } from 'lucide-react';
import { useAppData } from '@/lib/AppDataContext';
import { listServerAudit, getServerAuditStatus, setServerAuditForce, ServerAuditEvent, ServerAuditState } from '@/lib/api/serverAudit';
import { Skeleton, SkeletonText, SkeletonList } from '@/components/Skeleton';
import { useRouteId } from '@/lib/routeParams';

// Event-type → friendly label. Unknown types fall through verbatim, so a
// later server-side addition shows up immediately without a panel deploy.
const EVENT_LABELS: Record<string, string> = {
    power_action:           'Power action',
    member_invited:         'Member invited',
    member_removed:         'Member removed',
    member_perms_changed:   'Permissions changed',
    resources_changed:      'Resources changed',
    name_changed:           'Name changed',
    setup:                  'Server setup',
    reinstall:              'Server reinstall',
    subserver_deleted:      'Sub-server deleted',
    subserver_switched:     'Sub-server switched',
    deleted:                'Server deleted',
    audit_force_on_changed: 'Audit force-on changed',
};

const EVENT_FILTERS: { id: string; label: string }[] = [
    { id: '',                      label: 'All events' },
    { id: 'power_action',          label: 'Power' },
    { id: 'member_invited',        label: 'Invites' },
    { id: 'member_removed',        label: 'Removals' },
    { id: 'member_perms_changed',  label: 'Permissions' },
    { id: 'resources_changed',     label: 'Resources' },
    { id: 'name_changed',          label: 'Name' },
];

export default function ServerAuditView() {
    const paramId = useRouteId('servers');
    const serverId = Number(paramId);
    const { user, servers } = useAppData();
    const isAdmin = !!user?.isAdmin;
    const server = servers.find(s => s.id === serverId);
    const isOwner = server?.ownerId === user?.id;

    const [state, setState] = useState<ServerAuditState | null>(null);
    const [events, setEvents] = useState<ServerAuditEvent[]>([]);
    const [total, setTotal] = useState(0);
    const [filter, setFilter] = useState<string>('');
    const [loading, setLoading] = useState(true);
    const [toggling, setToggling] = useState(false);

    const reload = useCallback(async () => {
        if (!Number.isFinite(serverId) || serverId <= 0) return;
        const [s, e] = await Promise.all([
            getServerAuditStatus(serverId),
            listServerAudit(serverId, { eventType: filter || undefined, limit: 200 }),
        ]);
        if (s.success && s.state) setState(s.state);
        if (e.success) {
            setEvents(e.events || []);
            setTotal(e.total || 0);
        }
        setLoading(false);
    }, [serverId, filter]);

    useEffect(() => { reload(); }, [reload]);

    const handleToggleForce = async () => {
        if (!state) return;
        setToggling(true);
        const res = await setServerAuditForce(serverId, !state.forceOn);
        setToggling(false);
        if (res.success) {
            setState(prev => prev ? { ...prev, forceOn: res.forceOn, effectiveOn: prev.enabled || res.forceOn } : prev);
        }
    };

    if (loading) return (
        <main className="flex-1 flex flex-col overflow-hidden p-6 gap-4">
            <header className="shrink-0 flex items-start justify-between gap-4">
                <div className="space-y-2">
                    <SkeletonText width="w-40" className="h-5" />
                    <SkeletonText width="w-80" className="h-3" />
                </div>
                <Skeleton className="w-24 h-7 rounded-md" />
            </header>
            <section className="card p-4 border border-(--base-03) flex items-center justify-between gap-3">
                <SkeletonText width="w-64" className="h-3" />
                <Skeleton className="w-28 h-7 rounded-md" />
            </section>
            <div className="shrink-0 flex items-center gap-1.5">
                {Array.from({ length: 7 }).map((_, i) => (
                    <Skeleton key={i} className="w-20 h-7 rounded-md" />
                ))}
            </div>
            <div className="flex-1 max-w-4xl">
                <SkeletonList rows={6} />
            </div>
        </main>
    );

    if (!isOwner && !isAdmin) {
        return (
            <main className="flex-1 flex items-center justify-center text-(--base-06) p-6">
                <p className="text-sm">The audit log is reserved for the server&apos;s owner and platform admins.</p>
            </main>
        );
    }

    return (
        <main className="flex-1 flex flex-col overflow-hidden p-6 gap-4">
            <header className="shrink-0 flex items-start justify-between gap-4">
                <div>
                    <h1 className="h-page flex items-center gap-2">
                        <FileText size={22} className="text-(--accent-light)" />
                        Audit log
                    </h1>
                    <p className="text-sm text-(--base-06) mt-1">
                        Who did what on this server. Audit only kicks in the first time you invite a member — solo-owner
                        servers don&apos;t accumulate audit overhead until they need to.
                    </p>
                </div>
                {state && (
                    <div className={`flex items-center gap-2 px-3 py-1.5 rounded-md border text-xs font-mono ${
                        state.effectiveOn
                            ? 'border-(--success-border) bg-(--success-ghost) text-(--success-light)'
                            : 'border-(--base-04) bg-(--base-03) text-(--base-06)'
                    }`}>
                        {state.effectiveOn ? <ShieldCheck size={14} /> : <ShieldOff size={14} />}
                        {state.effectiveOn ? 'ACTIVE' : 'DORMANT'}
                    </div>
                )}
            </header>

            {/* State summary */}
            {state && (
                <section className="card p-4 border border-(--base-03) flex flex-col sm:flex-row sm:items-center justify-between gap-3">
                    <div className="text-sm text-(--base-07)">
                        <p>
                            {state.enabled
                                ? <>Auto-enabled because a member was invited. </>
                                : <>Auto-enable hasn&apos;t kicked in yet. </>}
                            {state.forceOn
                                ? <>Admin has <span className="font-mono text-(--accent-light)">forced</span> audit on. </>
                                : null}
                            Logged events so far: <span className="font-mono">{state.eventCount}</span>.
                        </p>
                    </div>
                    {isAdmin && (
                        <button
                            type="button"
                            onClick={handleToggleForce}
                            disabled={toggling}
                            className={`btn btn-sm inline-flex items-center gap-2 disabled:opacity-40 ${
                                state.forceOn ? 'btn-secondary' : 'btn-primary'
                            }`}
                        >
                            {toggling && <Loader2 size={12} className="animate-spin" />}
                            {state.forceOn ? 'Drop force-on' : 'Force audit on'}
                        </button>
                    )}
                </section>
            )}

            {/* Filters */}
            <div className="shrink-0 flex items-center gap-1.5 overflow-x-auto">
                <Filter size={14} className="text-(--base-06) shrink-0" />
                {EVENT_FILTERS.map(f => (
                    <button
                        key={f.id || 'all'}
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

            {/* Events */}
            <div className="flex-1 overflow-y-auto">
                {events.length === 0 ? (
                    <EmptyState effectiveOn={state?.effectiveOn ?? false} />
                ) : (
                    <ul className="space-y-2 max-w-4xl">
                        {events.map(ev => <EventRow key={ev.id} ev={ev} />)}
                    </ul>
                )}
                {events.length > 0 && events.length < total && (
                    <p className="text-xs text-(--base-06) text-center mt-4">
                        Showing {events.length} of {total}. Pagination/load-more coming in a polish pass.
                    </p>
                )}
            </div>
        </main>
    );
}

function EventRow({ ev }: { ev: ServerAuditEvent }) {
    const label = EVENT_LABELS[ev.eventType] || ev.eventType;
    return (
        <li className="card p-3 border border-(--base-03)">
            <div className="flex items-start justify-between gap-3">
                <div className="min-w-0">
                    <p className="text-sm font-medium text-(--base-09)">{label}</p>
                    <p className="text-xs text-(--base-06) mt-0.5 font-mono">
                        {ev.actorName ? <>by <span className="text-(--accent-light)">{ev.actorName}</span></> : 'system'}
                        {ev.targetName && <> · target: <span className="text-(--accent-light)">{ev.targetName}</span></>}
                        {ev.ipAddress && <> · {ev.ipAddress}</>}
                    </p>
                </div>
                <time className="text-[10px] font-mono text-(--base-06) shrink-0" title={ev.createdAt}>
                    {new Date(ev.createdAt).toLocaleString()}
                </time>
            </div>
            {ev.metadata && Object.keys(ev.metadata).length > 0 && (
                <pre className="mt-2 text-[10px] font-mono text-(--base-07) bg-(--base-02) border border-(--base-03) rounded p-2 overflow-x-auto">
{JSON.stringify(ev.metadata, null, 2)}
                </pre>
            )}
        </li>
    );
}

function EmptyState({ effectiveOn }: { effectiveOn: boolean }) {
    return (
        <div className="text-center py-16 text-(--base-06) max-w-md mx-auto">
            <AlertTriangle size={28} className="mx-auto" />
            <p className="mt-3 text-sm">
                {effectiveOn
                    ? 'Audit is active but no events have been recorded yet for the selected filter.'
                    : 'Audit is dormant — it auto-activates the first time you invite a member. Solo-owner activity isn’t logged to save space.'}
            </p>
        </div>
    );
}
