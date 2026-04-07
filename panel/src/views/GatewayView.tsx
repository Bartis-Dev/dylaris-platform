"use client";

import { useState, useEffect, useCallback, useRef } from 'react';
import {
    getGatewayRoutes, deleteGatewayRoute, triggerGatewaySync,
    GatewayRoute
} from '@/lib/api';
import { Globe, Trash2, RefreshCw, AlertTriangle, X } from 'lucide-react';

interface Toast {
    id: number;
    message: string;
    type: 'success' | 'error';
}

export default function GatewayView() {
    const [loading, setLoading] = useState(true);
    const [routes, setRoutes] = useState<GatewayRoute[]>([]);
    const [deleteModal, setDeleteModal] = useState<GatewayRoute | null>(null);
    const [deleting, setDeleting] = useState(false);
    const [toasts, setToasts] = useState<Toast[]>([]);
    const toastCounter = useRef(0);

    const showToast = useCallback((message: string, type: 'success' | 'error' = 'success') => {
        const id = ++toastCounter.current;
        setToasts(prev => [...prev, { id, message, type }]);
        setTimeout(() => setToasts(prev => prev.filter(t => t.id !== id)), 3000);
    }, []);

    const fetchData = useCallback(async () => {
        try {
            const routesRes = await getGatewayRoutes();
            if (routesRes.routes) setRoutes(routesRes.routes);
            else if (Array.isArray(routesRes)) setRoutes(routesRes);
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

    return (
        <div className="max-w-5xl mx-auto">
            {/* Header */}
            <div className="flex items-center justify-between mb-6">
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

            {/* Routes */}
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
