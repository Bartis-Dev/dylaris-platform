"use client";

import { useState, useEffect, useCallback, useRef } from 'react';
import {
    getGatewayRoutes, getGatewayGates, getGatewayLinks,
    deleteGatewayRoute, triggerGatewaySync,
    GatewayRoute, GatewayGate, GatewayLink
} from '@/lib/api';
import { Globe, Trash2, RefreshCw, AlertTriangle, X, ChevronDown, ExternalLink, Zap } from 'lucide-react';

interface Toast {
    id: number;
    message: string;
    type: 'success' | 'error';
}

export default function GatewayView() {
    const [loading, setLoading] = useState(true);
    const [routes, setRoutes] = useState<GatewayRoute[]>([]);
    const [gates, setGates] = useState<GatewayGate[]>([]);
    const [links, setLinks] = useState<GatewayLink[]>([]);
    const [deleteModal, setDeleteModal] = useState<GatewayRoute | null>(null);
    const [deleting, setDeleting] = useState(false);
    const [toasts, setToasts] = useState<Toast[]>([]);
    const [infoExpanded, setInfoExpanded] = useState(false);
    const toastCounter = useRef(0);

    const showToast = useCallback((message: string, type: 'success' | 'error' = 'success') => {
        const id = ++toastCounter.current;
        setToasts(prev => [...prev, { id, message, type }]);
        setTimeout(() => setToasts(prev => prev.filter(t => t.id !== id)), 3000);
    }, []);

    const fetchData = useCallback(async () => {
        try {
            const [routesRes, gatesRes, linksRes] = await Promise.all([
                getGatewayRoutes(),
                getGatewayGates(),
                getGatewayLinks(),
            ]);
            if (routesRes.routes) setRoutes(routesRes.routes);
            else if (Array.isArray(routesRes)) setRoutes(routesRes);

            if (Array.isArray(gatesRes)) setGates(gatesRes);
            else if (gatesRes.gates && Array.isArray(gatesRes.gates)) setGates(gatesRes.gates);

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

    if (loading) {
        return <div className="flex items-center justify-center h-64 text-(--base-07)">Loading gateway...</div>;
    }

    const gatewayDeployed = gates.length > 0 || links.length > 0;

    return (
        <div className="max-w-5xl mx-auto">

            {/* No gateway deployed — big info banner */}
            {!gatewayDeployed && (
                <div className="card p-8 flex flex-col gap-5 mb-6 border-(--accent-border)/40">
                    <div className="flex items-start gap-4">
                        <div className="w-10 h-10 rounded-lg bg-(--accent-ghost) flex items-center justify-center shrink-0 mt-0.5">
                            <Zap size={20} className="text-(--accent-light)" />
                        </div>
                        <div className="flex flex-col gap-1.5">
                            <h2 className="font-display text-lg font-bold text-(--base-09)">No Gateway Deployed</h2>
                            <p className="text-sm text-(--base-07) leading-relaxed">
                                The Gateway module routes player traffic through dedicated Gate servers — no direct host port exposed to the internet is required.
                                A Gate receives inbound connections and tunnels them to your game servers via an encrypted Link.
                            </p>
                            <p className="text-sm text-(--base-07) leading-relaxed">
                                The Gateway infrastructure (Gate + Link) is currently <span className="text-(--base-09) font-medium">closed source and not publicly available</span>.
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

            {/* Gateway deployed — small collapsible info */}
            {gatewayDeployed && (
                <div className="mb-4">
                    <button
                        onClick={() => setInfoExpanded(v => !v)}
                        className="flex items-center gap-2 text-xs text-(--base-06) hover:text-(--base-07) transition-colors"
                    >
                        <ChevronDown
                            size={13}
                            className="transition-transform duration-150"
                            style={{ transform: infoExpanded ? 'rotate(0deg)' : 'rotate(-90deg)' }}
                        />
                        Gateway info — {gates.length} gate{gates.length !== 1 ? 's' : ''}, {links.filter(l => l.online).length}/{links.length} links online
                    </button>
                    {infoExpanded && (
                        <div className="mt-2 px-3 py-2.5 bg-(--base-02) border border-(--base-03) rounded-lg text-xs text-(--base-07) leading-relaxed">
                            Traffic is routed through Gate servers via Link tunnels.
                            Gates and Links auto-register via Redis — manage them from the Gateway infrastructure.
                        </div>
                    )}
                </div>
            )}

            {/* Routes table — always shown when gateway is deployed */}
            {gatewayDeployed && (
                <>
                    <div className="flex items-center justify-between mb-4">
                        <h2 className="modal-title flex items-center gap-2">
                            <Globe size={20} />
                            Routes ({routes.length})
                        </h2>
                        <button
                            onClick={handleSync}
                            className="btn btn-secondary flex items-center gap-2 px-4 py-2 text-sm"
                        >
                            <RefreshCw size={14} />
                            Sync
                        </button>
                    </div>

                    <div className="card p-6">
                        {routes.length === 0 ? (
                            <p className="text-(--base-06) text-sm italic">No routes configured</p>
                        ) : (
                            <div className="overflow-x-auto">
                                <table className="w-full text-sm">
                                    <thead>
                                        <tr className="border-b border-(--base-03)">
                                            <th className="font-mono text-[10px] uppercase tracking-[0.08em] text-(--base-06) text-left pb-3 pr-4">Domain</th>
                                            <th className="font-mono text-[10px] uppercase tracking-[0.08em] text-(--base-06) text-left pb-3 pr-4">Target</th>
                                            <th className="font-mono text-[10px] uppercase tracking-[0.08em] text-(--base-06) text-left pb-3 pr-4">Link</th>
                                            <th className="font-mono text-[10px] uppercase tracking-[0.08em] text-(--base-06) text-left pb-3 pr-4">Server</th>
                                            <th className="font-mono text-[10px] uppercase tracking-[0.08em] text-(--base-06) text-left pb-3 pr-4">Owner</th>
                                            <th className="font-mono text-[10px] uppercase tracking-[0.08em] text-(--base-06) text-left pb-3 w-10"></th>
                                        </tr>
                                    </thead>
                                    <tbody>
                                        {routes.map(route => (
                                            <tr key={route.ID} className="border-b border-(--base-03)/50 hover:bg-(--base-03)/30 transition-colors">
                                                <td className="py-3 pr-4 text-(--base-09) font-medium">{route.domain}</td>
                                                <td className="py-3 pr-4 text-(--base-07) font-mono text-xs">{route.target_ip}:{route.target_port}</td>
                                                <td className="py-3 pr-4 text-(--base-07)">{route.link_name || '—'}</td>
                                                <td className="py-3 pr-4 text-(--base-07)">{route.server_name || '—'}</td>
                                                <td className="py-3 pr-4 text-(--base-07)">{route.owner_name || '—'}</td>
                                                <td className="py-3">
                                                    <button
                                                        onClick={() => setDeleteModal(route)}
                                                        className="text-(--base-05) hover:text-(--error-light) transition-colors"
                                                        title="Delete route"
                                                    >
                                                        <Trash2 size={15} />
                                                    </button>
                                                </td>
                                            </tr>
                                        ))}
                                    </tbody>
                                </table>
                            </div>
                        )}
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
