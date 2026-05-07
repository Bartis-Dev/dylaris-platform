"use client";

import { useState, useEffect, useCallback } from 'react';
import {
    Search, RefreshCw, Trash2, UserCheck, AlertTriangle,
    ChevronDown, ChevronRight, Loader2, X, Check
} from 'lucide-react';
import {
    getAdminServers, getAdminDiskAnalysis, updateServerOwner, deleteOrphanedFolder,
    deleteServer, getNodes, getUsers, AdminServer, DiskAnalysis, Node, User
} from '@/lib/api';

// ----------------------------------------------------------------
// Status badge
// ----------------------------------------------------------------
function StatusBadge({ status }: { status: string }) {
    const map: Record<string, string> = {
        online: 'bg-(--success-ghost) text-(--success-light) border-(--success-border)',
        offline: 'bg-(--base-03) text-(--base-06) border-(--base-04)',
        stopped: 'bg-(--base-03) text-(--base-06) border-(--base-04)',
        installing: 'bg-(--warning-ghost) text-(--warning) border-(--warning-border)',
        pending_setup: 'bg-(--base-03) text-(--base-05) border-(--base-03)',
        starting: 'bg-(--warning-ghost) text-(--warning) border-(--warning-border)',
        stopping: 'bg-(--warning-ghost) text-(--warning) border-(--warning-border)',
        suspended: 'bg-(--error-ghost) text-(--error-light) border-(--error-border)',
    };
    const cls = map[status] ?? 'bg-(--base-03) text-(--base-06) border-(--base-04)';
    return (
        <span className={`inline-flex items-center px-1.5 py-0.5 rounded text-[10px] font-mono border ${cls}`}>
            {status}
        </span>
    );
}

// ----------------------------------------------------------------
// Assign Owner Modal
// ----------------------------------------------------------------
function AssignOwnerModal({
    server,
    users,
    onClose,
    onAssigned,
}: {
    server: AdminServer;
    users: User[];
    onClose: () => void;
    onAssigned: () => void;
}) {
    const [search, setSearch] = useState('');
    const [loading, setLoading] = useState(false);
    const [error, setError] = useState('');

    const filtered = users.filter(u =>
        u.username.toLowerCase().includes(search.toLowerCase()) ||
        u.email?.toLowerCase().includes(search.toLowerCase())
    );

    const assign = async (userId: number) => {
        setLoading(true);
        setError('');
        const res = await updateServerOwner(server.id, userId);
        setLoading(false);
        if (res.success) {
            onAssigned();
            onClose();
        } else {
            setError(res.message ?? 'Failed to assign owner');
        }
    };

    return (
        <div className="modal-overlay animate-fade-in">
            <div className="modal-panel w-full max-w-md">
                <div className="modal-header">
                    <h3 className="modal-title">Assign Owner</h3>
                    <button onClick={onClose} className="p-1 rounded hover:bg-(--base-03) text-(--base-06)">
                        <X size={16} />
                    </button>
                </div>
                <div className="modal-body">
                    <p className="text-xs text-(--base-06) mb-3">
                        Assign <span className="text-(--base-08) font-medium">{server.name}</span> to a user.
                    </p>
                    {error && (
                        <div className="bg-(--error-ghost) border border-(--error-border) text-(--error-light) px-3 py-2 rounded-md mb-3 text-sm">
                            {error}
                        </div>
                    )}
                    <input
                        type="text"
                        placeholder="Search users..."
                        value={search}
                        onChange={e => setSearch(e.target.value)}
                        className="input-field w-full mb-3"
                    />
                    <div className="max-h-60 overflow-y-auto space-y-1">
                        {filtered.map(u => (
                            <button
                                key={u.id}
                                onClick={() => assign(u.id)}
                                disabled={loading}
                                className="w-full flex items-center justify-between px-3 py-2 rounded-md hover:bg-(--base-03) text-left transition-colors group"
                            >
                                <div>
                                    <p className="text-sm font-medium text-(--base-09)">{u.username}</p>
                                    {u.email && <p className="text-[11px] text-(--base-06)">{u.email}</p>}
                                </div>
                                {loading ? (
                                    <Loader2 size={14} className="animate-spin text-(--base-05)" />
                                ) : (
                                    <Check size={14} className="text-(--accent-light) opacity-0 group-hover:opacity-100 transition-opacity" />
                                )}
                            </button>
                        ))}
                        {filtered.length === 0 && (
                            <p className="text-xs text-(--base-05) text-center py-4">No users found</p>
                        )}
                    </div>
                </div>
            </div>
        </div>
    );
}

// ----------------------------------------------------------------
// Disk Analysis Panel (per node, on-demand)
// ----------------------------------------------------------------
function DiskAnalysisPanel({
    node,
    onOrphanDeleted,
    autoLoad,
    onAutoLoadConsumed,
}: {
    node: Node;
    onOrphanDeleted: () => void;
    autoLoad?: boolean;
    onAutoLoadConsumed?: () => void;
}) {
    const [data, setData] = useState<DiskAnalysis | null>(null);
    const [loading, setLoading] = useState(false);
    const [deleting, setDeleting] = useState<string | null>(null);
    const [deletingDbId, setDeletingDbId] = useState<number | null>(null);
    const [expanded, setExpanded] = useState(false);

    const load = async () => {
        setLoading(true);
        const res = await getAdminDiskAnalysis(node.id);
        setLoading(false);
        if (res.success) {
            setData({
                nodeOnline: res.nodeOnline,
                matched: res.matched ?? [],
                orphaned: res.orphaned ?? [],
                missing: res.missing ?? [],
            });
            setExpanded(true);
        }
    };

    // Auto-load + expand when navigated to from Infrastructure NodeCard
    useEffect(() => {
        if (autoLoad && !data && !loading) {
            load();
            onAutoLoadConsumed?.();
        }
    }, [autoLoad]);

    const handleDelete = async (orphanUUID: string) => {
        if (!confirm(`Delete orphaned folder ${orphanUUID} from node "${node.name}"? This cannot be undone.`)) return;
        setDeleting(orphanUUID);
        const res = await deleteOrphanedFolder(node.id, orphanUUID);
        setDeleting(null);
        if (res.success) {
            setData(prev => prev ? { ...prev, orphaned: prev.orphaned.filter(o => o.uuid !== orphanUUID) } : prev);
            onOrphanDeleted();
        } else {
            alert(res.message ?? 'Delete failed');
        }
    };

    const handleDeleteDbLeiche = async (m: { id: number; serverName: string }) => {
        if (!confirm(`"${m.serverName}" aus DB entfernen? Der Disk-Folder existiert nicht mehr — der DB-Eintrag ist eine Leiche.`)) return;
        setDeletingDbId(m.id);
        const res = await deleteServer(m.id);
        setDeletingDbId(null);
        if (res.success) {
            setData(prev => prev ? { ...prev, missing: prev.missing.filter(x => x.id !== m.id) } : prev);
            onOrphanDeleted();
        } else {
            alert(res.message ?? 'Delete failed');
        }
    };

    const orphanCount = data?.orphaned.length ?? 0;

    return (
        <div className="border border-(--base-03) rounded-lg overflow-hidden">
            <button
                onClick={() => expanded ? setExpanded(false) : (data ? setExpanded(true) : load())}
                className="w-full flex items-center justify-between px-4 py-3 bg-(--base-02) hover:bg-(--base-03) transition-colors text-left"
            >
                <div className="flex items-center gap-2.5">
                    <div className={`w-1.5 h-1.5 rounded-full ${node.status === 'online' ? 'bg-(--success-light)' : 'bg-(--error)'}`} />
                    <span className="text-sm font-medium text-(--base-09)">{node.name}</span>
                    {orphanCount > 0 && (
                        <span className="badge bg-(--warning-ghost) text-(--warning) border border-(--warning-border) text-[10px]">
                            {orphanCount} orphaned
                        </span>
                    )}
                    {data && data.matched.length > 0 && (
                        <span className="text-[11px] text-(--base-06)">{data.matched.length} matched · {data.missing.length} missing</span>
                    )}
                </div>
                <div className="flex items-center gap-2">
                    {loading && <Loader2 size={14} className="animate-spin text-(--base-05)" />}
                    {!data && !loading && (
                        <span className="text-[11px] text-(--accent-light)">Run analysis</span>
                    )}
                    {data && (expanded ? <ChevronDown size={14} className="text-(--base-05)" /> : <ChevronRight size={14} className="text-(--base-05)" />)}
                </div>
            </button>

            {expanded && data && (
                <div className="p-4 space-y-4">
                    {/* Orphaned folders */}
                    {data.orphaned.length > 0 && (
                        <div>
                            <p className="text-[10px] font-mono uppercase tracking-[0.08em] text-(--warning) mb-2 flex items-center gap-1.5">
                                <AlertTriangle size={10} />
                                Orphaned (disk only — no DB record)
                            </p>
                            <div className="space-y-1">
                                {data.orphaned.map(o => (
                                    <div key={o.uuid} className="flex items-center justify-between px-3 py-2 bg-(--base-02) rounded-md border border-(--warning-border)/30">
                                        <span className="font-mono text-xs text-(--base-07) truncate">{o.uuid}</span>
                                        <button
                                            onClick={() => handleDelete(o.uuid)}
                                            disabled={deleting === o.uuid}
                                            className="flex items-center gap-1.5 text-[11px] text-(--error-light) hover:bg-(--error-ghost) px-2 py-1 rounded transition-colors ml-3 shrink-0"
                                        >
                                            {deleting === o.uuid ? <Loader2 size={12} className="animate-spin" /> : <Trash2 size={12} />}
                                            Delete
                                        </button>
                                    </div>
                                ))}
                            </div>
                        </div>
                    )}

                    {/* Heartbeat-Safety Warning when node is not online */}
                    {data.nodeOnline === false && (
                        <div className="flex items-start gap-2 bg-(--warning-ghost) border border-(--warning-border) rounded-md px-3 py-2.5 text-xs text-(--warning)">
                            <AlertTriangle size={13} className="shrink-0 mt-0.5" />
                            <span>Node ist offline / kein Heartbeat — DB-Leichen können nicht zuverlässig erkannt werden.</span>
                        </div>
                    )}

                    {/* Missing from disk (DB-Leichen) — nur anzeigen wenn Node online war */}
                    {data.nodeOnline !== false && data.missing.length > 0 && (
                        <div>
                            <p className="text-[10px] font-mono uppercase tracking-[0.08em] text-(--base-06) mb-2">
                                Missing from disk (DB-Leichen)
                            </p>
                            <div className="space-y-1">
                                {data.missing.map(m => (
                                    <div key={m.uuid} className="flex items-center justify-between px-3 py-2 bg-(--base-02) rounded-md border border-(--base-03)">
                                        <div className="min-w-0">
                                            <span className="text-sm text-(--base-07)">{m.serverName}</span>
                                            <span className="font-mono text-[10px] text-(--base-05) ml-2">{m.uuid}</span>
                                        </div>
                                        <button
                                            onClick={() => handleDeleteDbLeiche(m)}
                                            disabled={deletingDbId === m.id}
                                            className="flex items-center gap-1.5 text-[11px] text-(--error-light) hover:bg-(--error-ghost) px-2 py-1 rounded transition-colors ml-3 shrink-0"
                                        >
                                            {deletingDbId === m.id ? <Loader2 size={12} className="animate-spin" /> : <Trash2 size={12} />}
                                            Aus DB entfernen
                                        </button>
                                    </div>
                                ))}
                            </div>
                        </div>
                    )}

                    {/* Matched */}
                    {data.nodeOnline !== false && data.orphaned.length === 0 && data.missing.length === 0 && (
                        <p className="text-xs text-(--success-light) flex items-center gap-1.5">
                            <Check size={13} />
                            All {data.matched.length} server folders match DB records
                        </p>
                    )}
                </div>
            )}
        </div>
    );
}

// ----------------------------------------------------------------
// Main AdminView
// ----------------------------------------------------------------
type AdminTab = 'servers' | 'disk';

interface AdminViewProps {
    initialFocus?: { tab: 'disk'; nodeId: number } | null;
    onFocusConsumed?: () => void;
}

export default function AdminView({ initialFocus, onFocusConsumed }: AdminViewProps = {}) {
    const [tab, setTab] = useState<AdminTab>('servers');
    const [servers, setServers] = useState<AdminServer[]>([]);
    const [nodes, setNodes] = useState<Node[]>([]);
    const [users, setUsers] = useState<User[]>([]);
    const [search, setSearch] = useState('');
    const [loading, setLoading] = useState(true);
    const [assignTarget, setAssignTarget] = useState<AdminServer | null>(null);
    const [autoExpandNodeId, setAutoExpandNodeId] = useState<number | null>(null);
    const [refreshKey, setRefreshKey] = useState(0);

    const refresh = useCallback(() => setRefreshKey(k => k + 1), []);

    // Deep-link from Infrastructure → Admin/Disk Analysis
    useEffect(() => {
        if (initialFocus?.tab === 'disk') {
            setTab('disk');
            setAutoExpandNodeId(initialFocus.nodeId);
            onFocusConsumed?.();
        }
    }, [initialFocus]);

    useEffect(() => {
        setLoading(true);
        Promise.all([
            getAdminServers({ search: search || undefined }),
            getNodes(),
            getUsers(),
        ]).then(([sRes, nRes, uRes]) => {
            setServers(sRes.success ? (sRes.servers ?? []) : []);
            setNodes(nRes.success ? (nRes.nodes ?? []) : []);
            setUsers(uRes.success ? (uRes.users ?? []) : []);
            setLoading(false);
        });
    }, [refreshKey]); // search is handled client-side to avoid extra fetches

    const filteredServers = search
        ? servers.filter(s =>
            s.name.toLowerCase().includes(search.toLowerCase()) ||
            s.uuid.toLowerCase().includes(search.toLowerCase()) ||
            (s.owner ?? '').toLowerCase().includes(search.toLowerCase())
        )
        : servers;

    const tabs: { id: AdminTab; label: string }[] = [
        { id: 'servers', label: 'All Servers' },
        { id: 'disk', label: 'Disk Analysis' },
    ];

    return (
        <div className="h-full flex flex-col gap-0">
            {/* Header */}
            <div className="flex items-center justify-between mb-6">
                <div>
                    <h2 className="text-xl font-display font-bold text-(--base-09)">Admin</h2>
                    <p className="text-sm text-(--base-06)">Server management and storage diagnostics</p>
                </div>
                <button onClick={refresh} className="btn btn-secondary px-3 py-1.5 text-sm flex items-center gap-1.5">
                    <RefreshCw size={13} />
                    Refresh
                </button>
            </div>

            {/* Tabs */}
            <div className="flex gap-1 mb-4 border-b border-(--base-03) pb-0">
                {tabs.map(t => (
                    <button
                        key={t.id}
                        onClick={() => setTab(t.id)}
                        className={`px-4 py-2 text-sm font-medium transition-colors border-b-2 -mb-px ${
                            tab === t.id
                                ? 'border-(--accent) text-(--accent-light)'
                                : 'border-transparent text-(--base-06) hover:text-(--base-08)'
                        }`}
                    >
                        {t.label}
                    </button>
                ))}
            </div>

            {/* Servers Tab */}
            {tab === 'servers' && (
                <div className="flex flex-col gap-4 flex-1 min-h-0">
                    <div className="flex items-center gap-3">
                        <div className="relative flex-1 max-w-sm">
                            <Search size={13} className="absolute left-3 top-1/2 -translate-y-1/2 text-(--base-05)" />
                            <input
                                type="text"
                                placeholder="Search by name, UUID or owner..."
                                value={search}
                                onChange={e => setSearch(e.target.value)}
                                className="input-field w-full pl-8"
                            />
                        </div>
                        {search && (
                            <button onClick={() => setSearch('')} className="text-(--base-05) hover:text-(--base-07) transition-colors">
                                <X size={14} />
                            </button>
                        )}
                        <span className="text-xs text-(--base-06) ml-auto">{filteredServers.length} server{filteredServers.length !== 1 ? 's' : ''}</span>
                    </div>

                    <div className="flex-1 overflow-auto">
                        {loading ? (
                            <div className="flex items-center justify-center py-16">
                                <Loader2 size={20} className="animate-spin text-(--base-05)" />
                            </div>
                        ) : filteredServers.length === 0 ? (
                            <div className="text-center py-16 text-(--base-05) text-sm">No servers found</div>
                        ) : (
                            <table className="w-full text-sm">
                                <thead>
                                    <tr className="text-left border-b border-(--base-03)">
                                        <th className="pb-2 pr-4 font-mono text-[10px] uppercase tracking-[0.08em] text-(--base-06) font-normal">Name</th>
                                        <th className="pb-2 pr-4 font-mono text-[10px] uppercase tracking-[0.08em] text-(--base-06) font-normal hidden md:table-cell">UUID</th>
                                        <th className="pb-2 pr-4 font-mono text-[10px] uppercase tracking-[0.08em] text-(--base-06) font-normal">Owner</th>
                                        <th className="pb-2 pr-4 font-mono text-[10px] uppercase tracking-[0.08em] text-(--base-06) font-normal hidden lg:table-cell">Node</th>
                                        <th className="pb-2 pr-4 font-mono text-[10px] uppercase tracking-[0.08em] text-(--base-06) font-normal">Status</th>
                                        <th className="pb-2 font-mono text-[10px] uppercase tracking-[0.08em] text-(--base-06) font-normal">Actions</th>
                                    </tr>
                                </thead>
                                <tbody className="divide-y divide-(--base-03)">
                                    {filteredServers.map(s => (
                                        <tr key={s.id} className="hover:bg-(--base-02) transition-colors group">
                                            <td className="py-2.5 pr-4">
                                                <div>
                                                    <p className="font-medium text-(--base-09)">{s.name}</p>
                                                    {s.activeSubServer && (
                                                        <p className="text-[11px] text-(--base-05) font-mono">{s.activeSubServer}</p>
                                                    )}
                                                </div>
                                            </td>
                                            <td className="py-2.5 pr-4 hidden md:table-cell">
                                                <span className="font-mono text-[11px] text-(--base-06)">{s.uuid.split('-')[0]}…</span>
                                            </td>
                                            <td className="py-2.5 pr-4">
                                                <span className="text-(--base-07)">{s.owner ?? <span className="text-(--base-04) italic">unassigned</span>}</span>
                                            </td>
                                            <td className="py-2.5 pr-4 hidden lg:table-cell">
                                                <span className="text-(--base-06) text-xs">{s.node}</span>
                                            </td>
                                            <td className="py-2.5 pr-4">
                                                <StatusBadge status={s.status} />
                                            </td>
                                            <td className="py-2.5">
                                                <div className="flex items-center gap-1 opacity-0 group-hover:opacity-100 transition-opacity">
                                                    <button
                                                        onClick={() => setAssignTarget(s)}
                                                        className="flex items-center gap-1 text-[11px] text-(--accent-light) hover:bg-(--accent-ghost) px-2 py-1 rounded transition-colors"
                                                        title="Assign owner"
                                                    >
                                                        <UserCheck size={12} />
                                                        Assign
                                                    </button>
                                                </div>
                                            </td>
                                        </tr>
                                    ))}
                                </tbody>
                            </table>
                        )}
                    </div>
                </div>
            )}

            {/* Disk Analysis Tab */}
            {tab === 'disk' && (
                <div className="flex flex-col gap-3 flex-1 min-h-0 overflow-auto">
                    <p className="text-sm text-(--base-06)">
                        Compare UUID folders on each node's disk against database records. Click a node to run the analysis.
                    </p>
                    <div className="flex items-start gap-2 bg-(--warning-ghost) border border-(--warning-border) rounded-md px-3 py-2.5 text-xs text-(--warning)">
                        <AlertTriangle size={13} className="shrink-0 mt-0.5" />
                        <span>Deleting orphaned folders is permanent and cannot be undone. Verify the UUID is not in use before deleting.</span>
                    </div>
                    <div className="space-y-2 pb-4">
                        {nodes.length === 0 ? (
                            <p className="text-sm text-(--base-05) text-center py-8">No nodes registered</p>
                        ) : (
                            nodes.map(n => (
                                <DiskAnalysisPanel
                                    key={n.id}
                                    node={n}
                                    onOrphanDeleted={refresh}
                                    autoLoad={autoExpandNodeId === n.id}
                                    onAutoLoadConsumed={() => setAutoExpandNodeId(null)}
                                />
                            ))
                        )}
                    </div>
                </div>
            )}

            {/* Modals */}
            {assignTarget && (
                <AssignOwnerModal
                    server={assignTarget}
                    users={users}
                    onClose={() => setAssignTarget(null)}
                    onAssigned={refresh}
                />
            )}
        </div>
    );
}
