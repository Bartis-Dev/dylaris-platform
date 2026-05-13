"use client";

import { useState, useEffect, useCallback, useMemo, useRef } from 'react';
import {
    getGatewayRoutes, getGatewayEdges, getGatewayLinks,
    deleteGatewayRoute, triggerGatewaySync,
    getRouteSuffixes, bulkDeleteRoutesBySuffix, RouteSuffix,
    GatewayRoute, GatewayEdge, GatewayLink
} from '@/lib/api';
import {
    Globe, Trash2, RefreshCw, AlertTriangle, X, ExternalLink, Zap,
    Network, Server, Activity, Search, Copy, Check, Layers
} from 'lucide-react';

interface Toast {
    id: number;
    message: string;
    type: 'success' | 'error';
}

function StatCard({ label, value, sub, icon }: { label: string; value: string | number; sub?: string; icon: React.ReactNode }) {
    return (
        <div className="card p-4 flex flex-col gap-1.5">
            <span className="font-mono text-[10px] uppercase tracking-[0.08em] text-(--base-06)">{label}</span>
            <div className="flex items-baseline gap-1.5">
                <div className="text-(--accent-light) flex items-center" style={{ marginBottom: 1 }}>{icon}</div>
                <span className="font-display text-2xl font-bold text-(--base-09) tabular-nums leading-none">{value}</span>
                {sub && <span className="text-sm font-normal text-(--base-05)">{sub}</span>}
            </div>
        </div>
    );
}

export default function GatewayView() {
    const [loading, setLoading] = useState(true);
    const [routes, setRoutes] = useState<GatewayRoute[]>([]);
    const [edges, setEdges] = useState<GatewayEdge[]>([]);
    const [links, setLinks] = useState<GatewayLink[]>([]);
    const [search, setSearch] = useState('');
    const [deleteModal, setDeleteModal] = useState<GatewayRoute | null>(null);
    const [deleting, setDeleting] = useState(false);
    const [toasts, setToasts] = useState<Toast[]>([]);
    const [copiedIP, setCopiedIP] = useState<string | null>(null);
    const toastCounter = useRef(0);

    // Bulk-delete-by-suffix modal state
    const [bulkOpen, setBulkOpen] = useState(false);
    const [bulkSuffixes, setBulkSuffixes] = useState<RouteSuffix[]>([]);
    const [bulkSelected, setBulkSelected] = useState<string>('');
    const [bulkBusy, setBulkBusy] = useState(false);
    const [bulkConfirmCountdown, setBulkConfirmCountdown] = useState(0);

    const showToast = useCallback((message: string, type: 'success' | 'error' = 'success') => {
        const id = ++toastCounter.current;
        setToasts(prev => [...prev, { id, message, type }]);
        setTimeout(() => setToasts(prev => prev.filter(t => t.id !== id)), 3000);
    }, []);

    const fetchData = useCallback(async () => {
        try {
            const [routesRes, edgesRes, linksRes] = await Promise.all([
                getGatewayRoutes(),
                getGatewayEdges(),
                getGatewayLinks(),
            ]);
            if (routesRes.routes) setRoutes(routesRes.routes);
            else if (Array.isArray(routesRes)) setRoutes(routesRes);

            if (Array.isArray(edgesRes)) setEdges(edgesRes);
            else if (edgesRes.edges && Array.isArray(edgesRes.edges)) setEdges(edgesRes.edges);

            if (Array.isArray(linksRes)) setLinks(linksRes);
            else if (linksRes.links && Array.isArray(linksRes.links)) setLinks(linksRes.links);
        } catch {
            // silent
        } finally {
            setLoading(false);
        }
    }, []);

    useEffect(() => {
        fetchData();
        const interval = setInterval(fetchData, 5000);
        return () => clearInterval(interval);
    }, [fetchData]);

    const openBulkDelete = async () => {
        setBulkSelected('');
        setBulkConfirmCountdown(0);
        setBulkOpen(true);
        const res = await getRouteSuffixes();
        if (res.success) setBulkSuffixes(res.suffixes || []);
        else setBulkSuffixes([]);
    };

    // Reset and start a 5-second timer whenever the user picks a suffix.
    useEffect(() => {
        if (!bulkOpen || !bulkSelected) {
            setBulkConfirmCountdown(0);
            return;
        }
        setBulkConfirmCountdown(5);
        const id = setInterval(() => {
            setBulkConfirmCountdown(c => (c <= 1 ? 0 : c - 1));
        }, 1000);
        return () => clearInterval(id);
    }, [bulkOpen, bulkSelected]);

    const handleBulkDelete = async () => {
        if (!bulkSelected || bulkConfirmCountdown > 0) return;
        setBulkBusy(true);
        const res = await bulkDeleteRoutesBySuffix(bulkSelected);
        setBulkBusy(false);
        if (res.success) {
            showToast(`Deleted ${res.deleted} route(s) under .${bulkSelected}${res.failed ? ` — ${res.failed} failed` : ''}`, res.failed ? 'error' : 'success');
            setBulkOpen(false);
            fetchData();
        } else {
            showToast(res.message || 'Bulk delete failed', 'error');
        }
    };

    const handleSync = async () => {
        const res = await triggerGatewaySync();
        if (res.success !== false) {
            showToast('Gateway sync triggered');
        } else {
            showToast(res.error || 'Sync failed', 'error');
        }
    };

    const handleDeleteRoute = async () => {
        if (!deleteModal) return;
        setDeleting(true);
        try {
            const res = await deleteGatewayRoute(deleteModal.ID);
            if (res.success !== false) {
                setRoutes(prev => prev.filter(r => r.ID !== deleteModal.ID));
                showToast('Route deleted');
                setDeleteModal(null);
            } else {
                showToast(res.error || 'Failed to delete route', 'error');
            }
        } finally {
            setDeleting(false);
        }
    };

    const copyToClipboard = async (text: string) => {
        try {
            await navigator.clipboard.writeText(text);
            setCopiedIP(text);
            setTimeout(() => setCopiedIP(null), 1500);
        } catch {
            showToast('Copy failed', 'error');
        }
    };

    const onlineEdges = edges.filter(e => e.status === 'online');
    const onlineLinks = links.filter(l => l.online);
    const totalTunnels = edges.reduce((sum, e) => sum + ((e.stats as any)?.active_tunnels ?? 0), 0);
    const gatewayDeployed = edges.length > 0 || links.length > 0;

    const filteredRoutes = useMemo(() => {
        if (!search.trim()) return routes;
        const q = search.toLowerCase();
        return routes.filter(r =>
            r.domain.toLowerCase().includes(q) ||
            (r.target_ip || '').toLowerCase().includes(q) ||
            (r.link_name || '').toLowerCase().includes(q) ||
            (r.server_name || '').toLowerCase().includes(q) ||
            (r.owner_name || '').toLowerCase().includes(q),
        );
    }, [routes, search]);

    if (loading) {
        return <div className="flex items-center justify-center h-64 text-(--base-07)">Loading gateway...</div>;
    }

    return (
        <div className="h-full flex flex-col gap-4 overflow-y-auto">

            {/* Header */}
            <div className="flex items-center justify-between">
                <div className="flex items-center gap-3">
                    <div className="w-9 h-9 rounded-md bg-(--base-03) flex items-center justify-center">
                        <Globe size={18} className="text-(--accent-light)" />
                    </div>
                    <h1 className="font-display text-xl font-bold text-(--base-09)">Gateway</h1>
                </div>
                {gatewayDeployed && (
                    <button
                        onClick={handleSync}
                        className="btn px-3 py-1.5 rounded-md bg-(--base-03) border border-(--base-04) text-(--base-07) hover:text-(--base-09) transition-colors flex items-center gap-1.5 text-sm"
                    >
                        <RefreshCw size={14} />
                        Sync
                    </button>
                )}
            </div>

            {/* No gateway deployed — info card */}
            {!gatewayDeployed && (
                <div className="card p-8 flex flex-col gap-5 border-(--accent-border)/40">
                    <div className="flex items-start gap-4">
                        <div className="w-10 h-10 rounded-lg bg-(--accent-ghost) flex items-center justify-center shrink-0 mt-0.5">
                            <Zap size={20} className="text-(--accent-light)" />
                        </div>
                        <div className="flex flex-col gap-1.5">
                            <h2 className="font-display text-lg font-bold text-(--base-09)">No Gateway Deployed</h2>
                            <p className="text-sm text-(--base-07) leading-relaxed">
                                The Gateway module routes player traffic through dedicated Edge servers — no direct host port exposed to the internet is required.
                                An Edge receives inbound connections and tunnels them to your game servers via an encrypted Link.
                            </p>
                            <p className="text-sm text-(--base-07) leading-relaxed">
                                The Gateway infrastructure (Edge + Link) is currently <span className="text-(--base-09) font-medium">closed source and not publicly available</span>.
                                You can still enable this module to manage routes once a Gateway is deployed.
                            </p>
                            <a
                                href="https://dylaris.com/portless"
                                target="_blank"
                                rel="noopener noreferrer"
                                className="inline-flex items-center gap-1.5 text-sm text-(--accent-light) hover:underline mt-1 w-fit"
                            >
                                Learn more about Portless routing
                                <ExternalLink size={13} />
                            </a>
                        </div>
                    </div>
                </div>
            )}

            {gatewayDeployed && (
                <>
                    {/* Summary cards */}
                    <div className="grid gap-3 grid-cols-2 md:grid-cols-4">
                        <StatCard label="Routes" value={routes.length} icon={<Globe size={16} />} />
                        <StatCard label="Edges" value={onlineEdges.length} sub={`/ ${edges.length}`} icon={<Server size={16} />} />
                        <StatCard label="Links" value={onlineLinks.length} sub={`/ ${links.length}`} icon={<Network size={16} />} />
                        <StatCard label="Tunnels" value={totalTunnels} icon={<Activity size={16} />} />
                    </div>

                    {/* DNS Targets — Edge IPs to point DNS to */}
                    {onlineEdges.length > 0 && (
                        <div className="card p-4 flex flex-col gap-2.5">
                            <div className="flex items-center gap-2">
                                <span className="font-mono text-[10px] uppercase tracking-[0.08em] text-(--base-06)">DNS Targets</span>
                                <span className="text-[11px] text-(--base-05)">— point your domains to one or more of these edge IPs</span>
                            </div>
                            <div className="flex flex-wrap gap-2">
                                {onlineEdges.map(edge => (
                                    <button
                                        key={edge.edge_id}
                                        onClick={() => copyToClipboard(edge.ip)}
                                        className="group flex items-center gap-2 px-3 py-1.5 rounded-md bg-(--base-02) border border-(--base-03) hover:border-(--accent-border) transition-colors"
                                        title={`${edge.name} — click to copy`}
                                    >
                                        <div className="w-1.5 h-1.5 rounded-full bg-(--success-light) shadow-[0_0_5px_var(--success-light)]" />
                                        <span className="font-mono text-xs text-(--base-09)">{edge.ip}</span>
                                        <span className="text-[10px] text-(--base-06) font-mono">{edge.name}</span>
                                        {copiedIP === edge.ip
                                            ? <Check size={12} className="text-(--success-light)" />
                                            : <Copy size={12} className="text-(--base-05) group-hover:text-(--base-08) transition-colors" />
                                        }
                                    </button>
                                ))}
                            </div>
                        </div>
                    )}

                    {/* Routes table */}
                    <div className="card p-4 flex-1 flex flex-col gap-3 min-h-0">
                        <div className="flex items-center justify-between gap-3 flex-wrap">
                            <div className="flex items-center gap-2">
                                <h2 className="font-display text-base font-bold text-(--base-09)">Routes</h2>
                                <span className="text-xs text-(--base-06)">
                                    ({filteredRoutes.length}{search ? ` of ${routes.length}` : ''})
                                </span>
                                {routes.length > 0 && (
                                    <button
                                        onClick={openBulkDelete}
                                        className="btn btn-secondary px-2.5 py-1 text-xs ml-2 inline-flex items-center gap-1.5"
                                        title="Delete all routes that share a domain suffix"
                                    >
                                        <Layers size={12} />
                                        Bulk delete
                                    </button>
                                )}
                            </div>
                            <div className="relative max-w-sm flex-1 min-w-[180px]">
                                <Search size={13} className="absolute left-3 top-1/2 -translate-y-1/2 text-(--base-05)" />
                                <input
                                    type="text"
                                    placeholder="Search domain, target, link, server, owner..."
                                    value={search}
                                    onChange={e => setSearch(e.target.value)}
                                    className="input-field w-full pl-8 text-sm"
                                />
                                {search && (
                                    <button
                                        onClick={() => setSearch('')}
                                        className="absolute right-2 top-1/2 -translate-y-1/2 text-(--base-05) hover:text-(--base-07) p-0.5"
                                    >
                                        <X size={13} />
                                    </button>
                                )}
                            </div>
                        </div>

                        <div className="flex-1 overflow-auto min-h-0">
                            {routes.length === 0 ? (
                                <p className="text-(--base-06) text-sm italic py-8 text-center">No routes configured. Add a route from a Server's Network tab.</p>
                            ) : filteredRoutes.length === 0 ? (
                                <p className="text-(--base-06) text-sm italic py-8 text-center">No routes match „{search}"</p>
                            ) : (
                                <table className="w-full text-sm">
                                    <thead className="sticky top-0 bg-(--base-02) z-10">
                                        <tr className="border-b border-(--base-03)">
                                            <th className="font-mono text-[10px] uppercase tracking-[0.08em] text-(--base-06) text-left pb-2 pr-4">Domain</th>
                                            <th className="font-mono text-[10px] uppercase tracking-[0.08em] text-(--base-06) text-left pb-2 pr-4">Target</th>
                                            <th className="font-mono text-[10px] uppercase tracking-[0.08em] text-(--base-06) text-left pb-2 pr-4 hidden md:table-cell">Link</th>
                                            <th className="font-mono text-[10px] uppercase tracking-[0.08em] text-(--base-06) text-left pb-2 pr-4 hidden lg:table-cell">Server</th>
                                            <th className="font-mono text-[10px] uppercase tracking-[0.08em] text-(--base-06) text-left pb-2 pr-4 hidden lg:table-cell">Owner</th>
                                            <th className="pb-2 w-10"></th>
                                        </tr>
                                    </thead>
                                    <tbody>
                                        {filteredRoutes.map(route => (
                                            <tr key={route.ID} className="border-b border-(--base-03)/50 hover:bg-(--base-03)/30 transition-colors group">
                                                <td className="py-2.5 pr-4">
                                                    <div className="flex items-center gap-2">
                                                        <span className="text-(--base-09) font-medium">{route.domain}</span>
                                                        <button
                                                            onClick={() => copyToClipboard(route.domain)}
                                                            className="text-(--base-05) hover:text-(--base-08) opacity-0 group-hover:opacity-100 transition-opacity p-0.5"
                                                            title="Copy domain"
                                                        >
                                                            {copiedIP === route.domain
                                                                ? <Check size={11} className="text-(--success-light)" />
                                                                : <Copy size={11} />
                                                            }
                                                        </button>
                                                    </div>
                                                </td>
                                                <td className="py-2.5 pr-4 text-(--base-07) font-mono text-xs">{route.target_ip}:{route.target_port}</td>
                                                <td className="py-2.5 pr-4 text-(--base-07) hidden md:table-cell">{route.link_name || '—'}</td>
                                                <td className="py-2.5 pr-4 text-(--base-07) hidden lg:table-cell">{route.server_name || '—'}</td>
                                                <td className="py-2.5 pr-4 text-(--base-07) hidden lg:table-cell">{route.owner_name || '—'}</td>
                                                <td className="py-2.5">
                                                    <button
                                                        onClick={() => setDeleteModal(route)}
                                                        className="text-(--base-05) hover:text-(--error-light) transition-colors p-1"
                                                        title="Delete route"
                                                    >
                                                        <Trash2 size={14} />
                                                    </button>
                                                </td>
                                            </tr>
                                        ))}
                                    </tbody>
                                </table>
                            )}
                        </div>
                    </div>
                </>
            )}

            {/* Delete confirm modal */}
            {deleteModal && (
                <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60">
                    <div className="card w-full max-w-sm p-6 flex flex-col gap-4">
                        <div className="flex items-center justify-between">
                            <div className="flex items-center gap-2">
                                <AlertTriangle size={16} className="text-(--error)" />
                                <h2 className="font-display text-base font-bold text-(--base-09)">Delete Route</h2>
                            </div>
                            <button onClick={() => setDeleteModal(null)} className="p-1 text-(--base-06) hover:text-(--base-09)">
                                <X size={15} />
                            </button>
                        </div>
                        <p className="text-sm text-(--base-07)">
                            Delete route <span className="text-(--base-09) font-medium">{deleteModal.domain}</span>?
                            This will immediately stop routing traffic through this entry.
                        </p>
                        <div className="flex justify-end gap-2">
                            <button
                                onClick={() => setDeleteModal(null)}
                                className="px-4 py-2 rounded-md text-sm text-(--base-07) hover:text-(--base-09) transition-colors"
                            >
                                Cancel
                            </button>
                            <button
                                onClick={handleDeleteRoute}
                                disabled={deleting}
                                className="px-4 py-2 rounded-md text-sm font-semibold bg-(--error) text-white hover:opacity-90 transition-opacity disabled:opacity-40"
                            >
                                {deleting ? 'Deleting...' : 'Delete'}
                            </button>
                        </div>
                    </div>
                </div>
            )}

            {/* Bulk-delete-by-suffix modal */}
            {bulkOpen && (
                <div className="modal-overlay animate-fade-in">
                    <div className="modal-panel w-full max-w-lg">
                        <div className="modal-header flex items-center justify-between">
                            <h3 className="modal-title flex items-center gap-2 text-(--error-light)">
                                <Layers size={18} />
                                Bulk Delete Routes
                            </h3>
                            <button onClick={() => setBulkOpen(false)} className="text-(--base-06) hover:text-(--error-light)">
                                <X size={18} />
                            </button>
                        </div>
                        <div className="modal-body space-y-3">
                            <p className="text-sm text-(--base-07)">
                                Pick a domain suffix. Every route whose domain equals or ends with{' '}
                                <code className="font-mono text-(--base-09) bg-(--base-02) px-1 py-0.5 rounded">.{`<suffix>`}</code>{' '}
                                will be deleted. Independent of configured hoster domains.
                            </p>
                            {bulkSuffixes.length === 0 ? (
                                <p className="text-xs text-(--base-06) italic py-6 text-center">No suffixes found.</p>
                            ) : (
                                <div className="flex flex-col gap-1 max-h-72 overflow-y-auto rounded-md border border-(--base-03) bg-(--base-02) p-1.5">
                                    {bulkSuffixes.map(s => {
                                        const selected = bulkSelected === s.suffix;
                                        return (
                                            <button
                                                key={`${s.depth}-${s.suffix}`}
                                                type="button"
                                                onClick={() => setBulkSelected(s.suffix)}
                                                className={`flex items-center gap-2 px-2 py-1.5 rounded-md text-left transition-colors ${
                                                    selected
                                                        ? 'bg-(--error-ghost) border border-(--error-border)'
                                                        : 'border border-transparent hover:bg-(--base-03)'
                                                }`}
                                            >
                                                <span className="font-mono text-[9px] uppercase tracking-[0.08em] text-(--base-06) bg-(--base-03) px-1.5 py-0.5 rounded">
                                                    {s.depth === 1 ? 'apex' : 'sub'}
                                                </span>
                                                <span className="flex-1 font-mono text-sm text-(--base-09) truncate">
                                                    *.{s.suffix}
                                                </span>
                                                <span className="font-mono text-xs text-(--base-06)">{s.count} route{s.count !== 1 ? 's' : ''}</span>
                                            </button>
                                        );
                                    })}
                                </div>
                            )}
                            {bulkSelected && (
                                <div className="bg-(--error-ghost) border border-(--error-border) text-(--error-light) px-3 py-2.5 rounded-md text-xs flex items-start gap-2">
                                    <AlertTriangle size={14} className="shrink-0 mt-0.5" />
                                    <span>
                                        This will permanently delete every route ending in{' '}
                                        <code className="font-mono">.{bulkSelected}</code>. Action cannot be undone.
                                    </span>
                                </div>
                            )}
                        </div>
                        <div className="modal-footer">
                            <button
                                type="button"
                                onClick={() => setBulkOpen(false)}
                                disabled={bulkBusy}
                                className="btn btn-secondary px-4 py-1.5 text-sm"
                            >
                                Cancel
                            </button>
                            <button
                                type="button"
                                onClick={handleBulkDelete}
                                disabled={!bulkSelected || bulkBusy || bulkConfirmCountdown > 0}
                                className="btn btn-danger px-4 py-1.5 text-sm disabled:opacity-50 inline-flex items-center gap-1.5"
                            >
                                {bulkBusy
                                    ? <><RefreshCw size={13} className="animate-spin" /> Deleting…</>
                                    : bulkConfirmCountdown > 0
                                        ? `Delete (${bulkConfirmCountdown}s)`
                                        : <><Trash2 size={13} /> Delete all matching</>}
                            </button>
                        </div>
                    </div>
                </div>
            )}

            {/* Toasts */}
            {toasts.length > 0 && (
                <div className="fixed bottom-6 right-6 z-50 flex flex-col gap-2">
                    {toasts.map(toast => (
                        <div
                            key={toast.id}
                            className={`px-4 py-2.5 rounded-(--radius-lg) text-sm font-medium shadow-lg border ${
                                toast.type === 'success'
                                    ? 'bg-(--base-02) border-(--success-light)/30 text-(--success-light)'
                                    : 'bg-(--base-02) border-(--error-light)/30 text-(--error-light)'
                            }`}
                        >
                            {toast.message}
                        </div>
                    ))}
                </div>
            )}
        </div>
    );
}
