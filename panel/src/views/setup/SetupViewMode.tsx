"use client";

import React, { useState, useEffect, useCallback } from 'react';
import { Server, getServerRoutes, createServerRoute, deleteServerRoute, GatewayRoute, CreateRouteRequest } from '@/lib/api';
import { useAppData } from '@/lib/AppDataContext';
import { Pencil, Plus, PackageOpen, Cpu, HardDrive, ChevronDown, ChevronUp, AlertTriangle, Globe, Trash2 } from 'lucide-react';
import { JAVA_IMAGES } from './JavaVersionPicker';
import RouteDomainPicker from '@/components/RouteDomainPicker';

interface SetupViewModeProps {
    server: Server;
    activeServerMissing: boolean;
    onEdit: () => void;
    onAddNew: () => void;
    hasSubServers: boolean;
}

const formatInstallerType = (type?: string) => {
    if (!type) return null;
    if (type === 'upload') return 'Manual Upload';
    if (type === 'library') return 'From Library';
    return type.charAt(0).toUpperCase() + type.slice(1);
};

export default function SetupViewMode({ server, activeServerMissing, onEdit, onAddNew, hasSubServers }: SetupViewModeProps) {
    const { routingMode } = useAppData();
    const showRoutes = routingMode !== 'ip_port';
    const [flagsOpen, setFlagsOpen] = useState(false);
    const javaLabel = JAVA_IMAGES.find(j => j.id === server.image)?.label || 'Unknown';
    const javaNote = JAVA_IMAGES.find(j => j.id === server.image)?.note || '';
    const hasFlags = !!server.extraJvmFlags;
    const flagCount = server.extraJvmFlags ? server.extraJvmFlags.split(' ').filter(Boolean).length : 0;

    // Routes state
    const [routes, setRoutes] = useState<GatewayRoute[]>([]);
    const [routesLoading, setRoutesLoading] = useState(false);
    const [newRoute, setNewRoute] = useState<CreateRouteRequest>({ targetPort: 25565 });
    const [routeError, setRouteError] = useState('');
    const [routeCreating, setRouteCreating] = useState(false);
    const [confirmDeleteRoute, setConfirmDeleteRoute] = useState<number | null>(null);

    const loadRoutes = useCallback(async () => {
        setRoutesLoading(true);
        try {
            const res = await getServerRoutes(server.id);
            if (res.routes) setRoutes(res.routes);
            else if (Array.isArray(res)) setRoutes(res);
            else setRoutes([]);
        } catch { setRoutes([]); }
        setRoutesLoading(false);
    }, [server.id]);

    useEffect(() => { loadRoutes(); }, [loadRoutes]);

    const hasRouteInput = !!(newRoute.subdomain || newRoute.customDomain || newRoute.domain);

    // Backend queues route creation to the Hub asynchronously, so a single
    // refetch right after the POST usually sees stale data. We optimistically
    // insert a placeholder using the resolved domain from the create response,
    // then poll loadRoutes a few times until the real entry appears.
    const handleCreateRoute = async () => {
        setRouteError('');
        if (!hasRouteInput) { setRouteError('Domain is required'); return; }
        setRouteCreating(true);
        try {
            const res = await createServerRoute(server.id, newRoute);
            if (res.error) {
                setRouteError(res.error);
                setRouteCreating(false);
                return;
            }
            const resolvedDomain: string | undefined = res.domain || newRoute.domain || newRoute.customDomain
                || (newRoute.subdomain && newRoute.hosterDomain ? `${newRoute.subdomain}.${newRoute.hosterDomain}` : undefined);
            if (resolvedDomain) {
                const tempId = -Date.now();
                setRoutes(prev => [
                    ...prev,
                    { ID: tempId, domain: resolvedDomain, target_ip: '', target_port: newRoute.targetPort } as GatewayRoute,
                ]);
            }
            setNewRoute({ targetPort: 25565 });
            // Poll for the real route to replace our placeholder. Hub typically
            // commits within a few hundred ms; give up after ~6s.
            for (let i = 0; i < 8; i++) {
                await new Promise(r => setTimeout(r, i === 0 ? 400 : 800));
                try {
                    const list = await getServerRoutes(server.id);
                    const real: GatewayRoute[] = Array.isArray(list) ? list : (list.routes ?? []);
                    if (!resolvedDomain || real.some(r => r.domain === resolvedDomain)) {
                        setRoutes(real);
                        break;
                    }
                } catch { /* keep polling */ }
            }
        } catch { setRouteError('Failed to create route'); }
        setRouteCreating(false);
    };

    const handleDeleteRoute = async (routeId: number) => {
        try {
            await deleteServerRoute(server.id, routeId);
            setRoutes(prev => prev.filter(r => r.ID !== routeId));
        } catch { /* ignore */ }
        setConfirmDeleteRoute(null);
    };

    return (
        <div className="flex-1 card flex flex-col overflow-hidden min-w-0">
            {/* Header */}
            <div className="modal-header flex items-center justify-between shrink-0">
                <div>
                    <h3 className="modal-title">Server Configuration</h3>
                    {server.activeSubServer && (
                        <p className="text-xs text-(--base-07) mt-1">
                            Active: <span className="font-mono text-(--primary-light)">{server.activeSubServer}</span>
                        </p>
                    )}
                </div>
                {hasSubServers && (
                    <div className="flex gap-2">
                        <button onClick={onEdit} className="btn btn-secondary px-3 py-1.5 text-sm">
                            <Pencil size={14} /> Edit
                        </button>
                        <button onClick={onAddNew} className="btn btn-primary px-3 py-1.5 text-sm">
                            <Plus size={14} /> Add Server
                        </button>
                    </div>
                )}
            </div>

            {/* Warning banner */}
            {activeServerMissing && (
                <div className="mx-6 mt-4 p-4 bg-(--warning-ghost) border border-(--warning-border) rounded-xl flex items-start gap-3">
                    <AlertTriangle size={20} className="text-(--warning-light) shrink-0" />
                    <div className="text-sm">
                        <p className="font-medium text-(--warning-light)">Server folder not found</p>
                        <p className="text-(--base-07) mt-1">
                            The active server folder <code className="bg-black/20 px-1 rounded font-mono">{server.activeSubServer}</code> no longer exists.
                            Select an existing server from the sidebar to continue.
                        </p>
                    </div>
                </div>
            )}

            {/* Summary body */}
            <div className="flex-1 overflow-y-auto p-6 space-y-5">
                {/* Software hero badge */}
                {server.installerType && (
                    <div className="flex items-center gap-3 px-5 py-4 bg-(--base-03) rounded-lg border border-(--base-04)">
                        <div className="w-10 h-10 rounded-md bg-(--primary-ghost) border border-(--primary-border) flex items-center justify-center">
                            <PackageOpen size={20} className="text-(--primary-light)" />
                        </div>
                        <div>
                            <p className="text-base font-semibold text-(--base-09)">
                                {formatInstallerType(server.installerType)}
                            </p>
                            {server.minecraftVersion && (
                                <p className="text-sm text-(--base-07) font-mono">
                                    {server.minecraftVersion}
                                    {server.buildNumber && <span className="text-(--base-06)"> #{server.buildNumber}</span>}
                                </p>
                            )}
                        </div>
                    </div>
                )}

                {/* Stat chips */}
                <div className="flex flex-wrap gap-3">
                    <div className="flex items-center gap-2 px-3.5 py-2 bg-(--base-02) rounded-md border border-(--base-03)">
                        <Cpu size={14} className="text-(--accent-light)" />
                        <span className="text-sm text-(--base-08) font-medium">{javaLabel}</span>
                        <span className="text-xs text-(--base-06)">{javaNote}</span>
                    </div>
                    <div className="flex items-center gap-2 px-3.5 py-2 bg-(--base-02) rounded-md border border-(--base-03)">
                        <HardDrive size={14} className="text-(--primary-light)" />
                        <span className="text-sm text-(--base-08) font-medium">{server.memory} MB</span>
                        <span className="text-xs text-(--base-06)">RAM</span>
                    </div>
                    {hasFlags && (
                        <div className="flex items-center gap-2 px-3.5 py-2 bg-(--base-02) rounded-md border border-(--base-03)">
                            <span className="text-sm text-(--base-08) font-medium">{flagCount} JVM Flags</span>
                        </div>
                    )}
                </div>

                {/* Collapsible JVM flags */}
                {hasFlags && (
                    <div>
                        <button
                            type="button"
                            onClick={() => setFlagsOpen(o => !o)}
                            className="input-label flex items-center gap-1.5 w-fit cursor-pointer hover:text-(--base-08) transition-colors"
                        >
                            {flagsOpen ? <ChevronUp size={12} /> : <ChevronDown size={12} />}
                            <span>JVM Flags</span>
                        </button>
                        {flagsOpen && (
                            <div className="mt-2 p-3 bg-(--base-02) rounded-md border border-(--base-03) font-mono text-xs text-(--base-07) whitespace-pre-wrap animate-fade-in">
                                java -Xms{server.memory}M -Xmx{server.memory}M {server.extraJvmFlags}
                            </div>
                        )}
                    </div>
                )}

                {/* Routes / Domains — only shown when gateway actually routes traffic.
                    Existing routes stay in the DB even in ip_port mode so they're
                    preserved when an admin re-enables gateway routing later. */}
                {showRoutes && (
                <div className="border-t border-(--base-03) pt-5">
                    <h4 className="flex items-center gap-2 text-sm font-semibold text-(--base-09) mb-1">
                        <Globe size={14} className="text-(--accent-light)" />
                        Routes / Domains
                    </h4>
                    <p className="text-xs text-(--base-06) mb-3">Map custom domains to this server through the gateway.</p>

                    {routesLoading ? (
                        <p className="text-sm text-(--base-07)">Loading routes...</p>
                    ) : routes.length > 0 ? (
                        <div className="space-y-2 mb-3">
                            {routes.map(route => (
                                <div key={route.ID} className="flex items-center justify-between bg-(--base-02) rounded-md px-3 py-2 border border-(--base-03)">
                                    <div className="flex items-center gap-2.5">
                                        <Globe size={12} className="text-(--accent-light) shrink-0" />
                                        <div>
                                            <div className="text-sm font-medium text-(--base-09)">{route.domain}</div>
                                            <div className="font-mono text-[10px] text-(--base-06) uppercase tracking-[0.08em]">
                                                Port {route.target_port}
                                                {route.link_name && <> &middot; Link: {route.link_name}</>}
                                            </div>
                                        </div>
                                    </div>
                                    {confirmDeleteRoute === route.ID ? (
                                        <div className="flex gap-2">
                                            <button onClick={() => handleDeleteRoute(route.ID)} className="btn btn-danger px-2.5 py-1 text-xs">Delete</button>
                                            <button onClick={() => setConfirmDeleteRoute(null)} className="btn btn-secondary px-2.5 py-1 text-xs">Cancel</button>
                                        </div>
                                    ) : (
                                        <button
                                            onClick={() => setConfirmDeleteRoute(route.ID)}
                                            className="text-(--error-light) hover:text-(--error) transition-colors p-1"
                                            title="Delete route"
                                        >
                                            <Trash2 size={14} />
                                        </button>
                                    )}
                                </div>
                            ))}
                        </div>
                    ) : (
                        <p className="text-xs text-(--base-06) italic mb-3">No routes configured.</p>
                    )}

                    <div className="bg-(--base-03) rounded-md border border-(--base-04) p-3 space-y-2">
                        <div className="font-mono text-[10px] uppercase tracking-[0.08em] text-(--base-06)">Add Route</div>
                        <RouteDomainPicker
                            value={newRoute}
                            onChange={next => { setNewRoute(next); setRouteError(''); }}
                            error={routeError}
                            portChildren={
                                <div className="flex gap-2">
                                    <select
                                        value={newRoute.targetPort}
                                        onChange={e => setNewRoute(r => ({ ...r, targetPort: Number(e.target.value) }))}
                                        className="input-field text-sm w-32"
                                    >
                                        <option value={25565}>MC (25565)</option>
                                        <option value={80}>HTTP (80)</option>
                                        <option value={443}>HTTPS (443)</option>
                                    </select>
                                    <button
                                        onClick={handleCreateRoute}
                                        disabled={routeCreating || !hasRouteInput}
                                        className="btn btn-primary text-xs px-3 disabled:opacity-50"
                                    >
                                        <Plus size={13} />
                                        Add
                                    </button>
                                </div>
                            }
                        />
                    </div>
                </div>
                )}
            </div>
        </div>
    );
}
