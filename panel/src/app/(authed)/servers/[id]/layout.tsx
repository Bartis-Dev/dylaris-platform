"use client";

import React, { useState, useEffect } from 'react';
import { useParams, usePathname, useRouter } from 'next/navigation';
import Link from 'next/link';
import {
    Pencil, SlidersHorizontal, Trash2, AlertTriangle, Play, Square, RotateCcw, Skull,
    HardDrive, MoveHorizontal, RefreshCw, Copy, Globe, Link2, ChevronDown,
} from 'lucide-react';
import { DynamicIcon } from '@/lib/icons';
import {
    deleteServer, updateServerName, updateServerResources, serverPower,
    getServerStoragePath, migrateServerStorage, getServerRoutes,
    GatewayRoute, StoragePathInfo, TabPermissions,
} from '@/lib/api';
import { useAppData } from '@/lib/AppDataContext';

export default function ServerLayout({ children }: { children: React.ReactNode }) {
    const params = useParams();
    const pathname = usePathname();
    const router = useRouter();
    const { servers, user, refreshServers, gatewayEnabled, routingMode } = useAppData();

    const serverId = Number(params?.id);
    const selectedServer = servers.find(s => s.id === serverId);

    const [isEditingName, setIsEditingName] = useState(false);
    const [editedName, setEditedName] = useState('');

    const [showDeletePopup, setShowDeletePopup] = useState(false);
    const [deleteCountdown, setDeleteCountdown] = useState(5);

    const [showEditResourcesPopup, setShowEditResourcesPopup] = useState(false);
    const [editRam, setEditRam] = useState(0);
    const [editCpuLimit, setEditCpuLimit] = useState(0);
    const [editDiskLimit, setEditDiskLimit] = useState(0);
    const [editCpusetCpus, setEditCpusetCpus] = useState('');
    const [editResourcesAdvancedOpen, setEditResourcesAdvancedOpen] = useState(false);
    const [editHostPort, setEditHostPort] = useState(0);
    const [editContainerPort, setEditContainerPort] = useState(25565);

    const [storageCurrentPath, setStorageCurrentPath] = useState('');
    const [storagePaths, setStoragePaths] = useState<StoragePathInfo[]>([]);
    const [storageMigrateTarget, setStorageMigrateTarget] = useState('');
    const [storageMigrating, setStorageMigrating] = useState(false);
    const [storageMigrateMsg, setStorageMigrateMsg] = useState('');

    const [powerLoading, setPowerLoading] = useState<string | null>(null);
    const [waitingForStatus, setWaitingForStatus] = useState<string | null>(null);
    const [killCooldown, setKillCooldown] = useState(false);
    const [showKillConfirm, setShowKillConfirm] = useState(false);

    const [serverRoutes, setServerRoutes] = useState<GatewayRoute[]>([]);

    // Load gateway routes when relevant
    useEffect(() => {
        setServerRoutes([]);
        if (!selectedServer || !gatewayEnabled) return;
        getServerRoutes(selectedServer.id).then(res => {
            if (Array.isArray(res)) setServerRoutes(res);
            else if (res && Array.isArray(res.routes)) setServerRoutes(res.routes);
        });
    }, [selectedServer?.id, gatewayEnabled]);

    // Clear waitingForStatus when server reaches expected status
    useEffect(() => {
        if (waitingForStatus && selectedServer?.status === waitingForStatus) setWaitingForStatus(null);
    }, [selectedServer?.status, waitingForStatus]);

    // Fallback: reset waitingForStatus after 60s
    useEffect(() => {
        if (!waitingForStatus) return;
        const timeout = setTimeout(() => setWaitingForStatus(null), 60000);
        return () => clearTimeout(timeout);
    }, [waitingForStatus]);

    if (!selectedServer) {
        return (
            <main className="flex-1 flex flex-col overflow-hidden relative z-10">
                <div className="flex-1 flex items-center justify-center text-(--base-06)">
                    Server not found.
                </div>
            </main>
        );
    }

    const isPendingSetup = selectedServer.status === 'pending_setup';
    const isDiskFull = selectedServer.status === 'disk_full';
    const isServerOffline = ['stopped', 'offline', 'pending_setup', 'disk_full'].includes(selectedServer.status);
    const powerWaiting = waitingForStatus !== null || powerLoading !== null;

    const handleStartEditName = () => {
        setEditedName(selectedServer.name || '');
        setIsEditingName(true);
    };
    const handleSaveName = async () => {
        const trimmed = editedName.replace(/\s{2,}/g, ' ').trim();
        if (!trimmed || !/^[a-zA-Z0-9\-+ ]{1,50}$/.test(trimmed)) { setIsEditingName(false); return; }
        await updateServerName(selectedServer.id, trimmed);
        setIsEditingName(false);
        refreshServers();
    };

    const handleOpenDeletePopup = () => {
        setDeleteCountdown(5);
        setShowDeletePopup(true);
        const interval = setInterval(() => {
            setDeleteCountdown(prev => {
                if (prev <= 1) { clearInterval(interval); return 0; }
                return prev - 1;
            });
        }, 1000);
    };
    const handleDelete = async () => {
        await deleteServer(selectedServer.id);
        setShowDeletePopup(false);
        await refreshServers();
        router.push('/servers');
    };

    const handleOpenEditResources = async () => {
        setEditRam(selectedServer.memory || 1024);
        setEditCpuLimit(selectedServer.cpuLimit || 0);
        setEditDiskLimit((selectedServer.diskLimit || 0) / 1024);
        setEditHostPort(selectedServer.hostPort || 0);
        setEditContainerPort(selectedServer.containerPort || 25565);
        setEditCpusetCpus((selectedServer as any).cpusetCpus || '');
        setEditResourcesAdvancedOpen(false);
        setStorageCurrentPath('');
        setStoragePaths([]);
        setStorageMigrateTarget('');
        setStorageMigrateMsg('');
        setShowEditResourcesPopup(true);

        if (user?.isAdmin) {
            try {
                const res = await getServerStoragePath(selectedServer.id);
                if (res.success) {
                    setStorageCurrentPath(res.currentPath || '');
                    const paths: StoragePathInfo[] = Array.isArray(res.storagePaths) ? res.storagePaths : [];
                    setStoragePaths(paths);
                    const others = paths.filter(p => p.path !== res.currentPath);
                    if (others.length > 0) setStorageMigrateTarget(others[0].path);
                }
            } catch { /* ignore */ }
        }
    };
    const handleSaveResources = async () => {
        const ports = user?.isAdmin ? { hostPort: editHostPort, containerPort: editContainerPort } : undefined;
        await updateServerResources(
            selectedServer.id, editRam, editCpuLimit, editDiskLimit > 0 ? editDiskLimit * 1024 : 0,
            ports, editCpusetCpus || undefined,
        );
        setShowEditResourcesPopup(false);
        refreshServers();
    };

    const handleMigrateStorage = async () => {
        if (!storageMigrateTarget) return;
        setStorageMigrating(true);
        setStorageMigrateMsg('');
        try {
            const res = await migrateServerStorage(selectedServer.id, storageMigrateTarget);
            setStorageMigrateMsg(res.success ? res.message : (res.error || 'Migration failed'));
        } catch {
            setStorageMigrateMsg('Migration request failed');
        }
        setStorageMigrating(false);
    };

    const handlePower = async (action: 'start' | 'stop' | 'restart' | 'kill') => {
        setPowerLoading(action);
        if (action === 'kill') {
            setWaitingForStatus(null);
            if (!user?.isAdmin) setKillCooldown(true);
        } else {
            setWaitingForStatus(action === 'stop' ? 'stopped' : 'online');
        }
        try { await serverPower(selectedServer.id, action); } catch { /* ignore */ }
        setPowerLoading(null);
        if (action === 'kill' && !user?.isAdmin) {
            setTimeout(() => setKillCooldown(false), 60000);
        }
    };

    const getStatusColor = (status: string) => {
        switch (status) {
            case 'online': return 'bg-(--success-light)';
            case 'stopped':
            case 'offline':
            case 'disk_full': return 'bg-(--error)';
            default: return 'bg-(--warning) animate-pulse';
        }
    };

    const isOwner = selectedServer.role !== 'invited' && selectedServer.role !== 'inherited';
    const perms = selectedServer.permissions;
    const tabDisabled = (perm: keyof TabPermissions) => isPendingSetup || (!isOwner && !!perms && !perms[perm]);
    const canPower = isOwner || (perms?.power ?? false);

    // Tabs (path segments)
    const tabs: { slug: string; icon: string; label: string; disabled: boolean }[] = [
        { slug: 'setup',   icon: 'wrench',          label: 'Setup',         disabled: !isOwner && !!perms && !perms.setup },
        { slug: 'overview',icon: 'house',           label: 'Overview',      disabled: isPendingSetup },
        { slug: 'console', icon: 'square-terminal', label: 'Console',       disabled: tabDisabled('console') },
        { slug: 'files',   icon: 'folder-open',     label: 'Files',         disabled: tabDisabled('files') },
        { slug: 'config',  icon: 'settings',        label: 'Configuration', disabled: tabDisabled('config') },
        { slug: 'network', icon: 'network',         label: 'Network',       disabled: tabDisabled('network') },
        { slug: 'members', icon: 'users',           label: 'Members',       disabled: !isOwner && (!perms || !perms.members) },
    ];

    return (
        <main className="flex-1 flex flex-col overflow-hidden relative z-10">
            {/* Server Header */}
            <div className="bg-(--base-02) border-b border-(--base-03) px-6 pt-3 shrink-0 z-10">
                {/* Row 1: Server Name + Actions */}
                <div className="flex items-center justify-between mb-2">
                    <div className="flex items-center space-x-2">
                        <span className={`w-2.5 h-2.5 rounded-full shrink-0 ${getStatusColor(selectedServer.status)}`} title={selectedServer.status} />
                        {isEditingName ? (
                            <input
                                className="text-xl font-bold font-display bg-(--base-03) text-(--base-09) rounded-md px-2 py-0.5 outline-none border border-(--accent) focus:shadow-[0_0_0_3px_rgba(112,72,200,0.15)]"
                                value={editedName}
                                onChange={e => setEditedName(e.target.value)}
                                onKeyDown={e => { if (e.key === 'Enter') handleSaveName(); if (e.key === 'Escape') setIsEditingName(false); }}
                                onBlur={handleSaveName}
                                maxLength={50}
                                autoFocus
                            />
                        ) : (
                            <div className="flex items-center gap-2 bg-(--base-03)/40 border border-(--base-04) rounded-md px-3 py-1">
                                <h1 className="text-xl font-bold font-display text-(--base-09) tracking-wide">{selectedServer.name}</h1>
                                <button onClick={handleStartEditName} className="text-(--base-06) hover:text-(--base-09) transition-colors">
                                    <Pencil size={16} />
                                </button>
                            </div>
                        )}
                        {selectedServer.activeSubServer && (
                            <span className="text-[10px] bg-(--base-03) px-2 py-0.5 rounded-sm text-(--base-07) font-mono uppercase tracking-[0.08em]">
                                {selectedServer.activeSubServer}
                            </span>
                        )}
                        {selectedServer.serverType === 'proxy' && (
                            <span className="text-[10px] bg-(--accent-ghost) px-2 py-0.5 rounded-sm text-(--accent-light) font-mono uppercase tracking-[0.08em] flex items-center gap-1">
                                Proxy
                            </span>
                        )}
                        {selectedServer.proxyId && (() => {
                            const proxy = servers.find(s => s.id === selectedServer.proxyId);
                            return proxy ? (
                                <Link
                                    href={`/servers/${proxy.id}`}
                                    className="text-[10px] bg-(--accent-ghost) px-2 py-0.5 rounded-sm text-(--accent-light) font-mono uppercase tracking-[0.08em] flex items-center gap-1 hover:bg-(--accent)/20 transition-colors"
                                >
                                    {proxy.name}
                                </Link>
                            ) : null;
                        })()}
                        <span className="text-xs text-(--base-07)">
                            Owner: <span className="font-medium text-(--base-09)">{selectedServer.ownerName === user?.username ? 'You' : (selectedServer.ownerName || `ID: ${selectedServer.ownerId}`)}</span>
                        </span>
                    </div>
                    <div className="flex items-center gap-2">
                        <div className="flex items-center gap-1.5">
                            {isPendingSetup && (
                                <span className="px-2.5 py-1 rounded-sm text-[10px] font-mono font-medium uppercase tracking-[0.08em] border bg-(--warning-ghost) flex items-center gap-2 border-(--warning)/20 text-(--warning)">
                                    <div className="w-[5px] h-[5px] rounded-full bg-(--warning) animate-pulse"></div>
                                    pending_setup
                                </span>
                            )}
                            {isDiskFull && (
                                <span className="flex items-center gap-1.5 px-3 py-1.5 rounded-md bg-(--error-ghost) border border-(--error-border) text-(--error-light) text-xs font-semibold">
                                    <HardDrive size={14} />
                                    Speicher voll
                                </span>
                            )}
                            <button
                                onClick={() => handlePower('start')}
                                disabled={!canPower || isPendingSetup || isDiskFull || powerWaiting || !isServerOffline}
                                className={`flex items-center gap-1.5 px-3 py-1.5 rounded-md transition-colors disabled:opacity-30 disabled:cursor-not-allowed border ${
                                    isServerOffline && !isPendingSetup && !isDiskFull && canPower
                                        ? 'bg-(--success) text-white border-(--success) hover:bg-(--success-light)'
                                        : 'bg-(--success-ghost) text-(--success-light) border-(--success)/15 hover:bg-(--success)/15'
                                }`}
                                title={isDiskFull ? 'Speicher voll — Dateien loeschen oder Limit erhoehen' : canPower ? 'Start server' : 'No permission'}
                            >
                                <Play size={16} />
                                <span className="text-xs font-semibold">Start</span>
                            </button>
                            <button
                                onClick={() => handlePower('restart')}
                                disabled={!canPower || isPendingSetup || isDiskFull || powerWaiting || isServerOffline}
                                className="flex items-center gap-1.5 px-3 py-1.5 rounded-md bg-(--warning-ghost) hover:bg-(--warning)/15 transition-colors disabled:opacity-30 disabled:cursor-not-allowed border border-(--warning)/15"
                                title={isDiskFull ? 'Speicher voll' : canPower ? 'Restart server' : 'No permission'}
                            >
                                <RotateCcw size={16} className="text-(--warning)" />
                                <span className="text-xs font-semibold text-(--warning)">Restart</span>
                            </button>
                            <button
                                onClick={() => handlePower('stop')}
                                disabled={!canPower || isPendingSetup || powerWaiting || isServerOffline}
                                className="flex items-center gap-1.5 px-3 py-1.5 rounded-md bg-(--error-ghost) hover:bg-(--error)/15 transition-colors disabled:opacity-30 disabled:cursor-not-allowed border border-(--error)/15"
                                title={canPower ? 'Stop server' : 'No permission'}
                            >
                                <Square size={16} className="text-(--error)" />
                                <span className="text-xs font-semibold text-(--error)">Stop</span>
                            </button>
                            <button
                                onClick={() => setShowKillConfirm(true)}
                                disabled={!canPower || killCooldown || isServerOffline}
                                className="flex items-center gap-1.5 px-3 py-1.5 rounded-md bg-(--error-ghost) hover:bg-(--error)/15 transition-colors disabled:opacity-30 disabled:cursor-not-allowed border border-(--error)/15"
                                title={canPower ? 'Force kill container' : 'No permission'}
                            >
                                <Skull size={16} className="text-(--error)" />
                                <span className="text-xs font-semibold text-(--error)">Kill</span>
                            </button>
                        </div>
                        {user?.isAdmin && <div className="w-px h-6 bg-(--base-04)" />}
                        {user?.isAdmin && (
                            <div className="flex items-center gap-1.5">
                                <button onClick={handleOpenEditResources} className="btn btn-secondary px-2.5 py-1 text-xs">
                                    <SlidersHorizontal size={14} />
                                    Resources
                                </button>
                                <button onClick={handleOpenDeletePopup} className="btn btn-danger px-2.5 py-1 text-xs">
                                    <Trash2 size={14} />
                                    Delete
                                </button>
                            </div>
                        )}
                    </div>
                </div>

                {/* Connection Info */}
                {!isPendingSetup && (
                    (routingMode !== 'gateway' && selectedServer.nodeAddress && (selectedServer.hostPort ?? 0) > 0) ||
                    (gatewayEnabled && serverRoutes.length > 0) ||
                    (routingMode === 'gateway' && gatewayEnabled && serverRoutes.length === 0)
                ) && (
                    <div className={`flex items-center gap-4 py-2 border-t flex-wrap ${
                        routingMode === 'gateway' && gatewayEnabled && serverRoutes.length === 0
                            ? 'border-(--warning)/40 animate-pulse'
                            : 'border-(--base-03)/60'
                    }`}>
                        {routingMode !== 'gateway' && selectedServer.nodeAddress && (selectedServer.hostPort ?? 0) > 0 && (
                            <div className="flex items-center gap-2">
                                <span className="text-[10px] font-mono uppercase tracking-[0.08em] text-(--base-05)">Direct</span>
                                <div className="flex items-center gap-1.5 bg-(--base-03)/50 border border-(--base-04) rounded-md px-2.5 py-1">
                                    <Link2 size={11} className="text-(--base-06) shrink-0" />
                                    <span className="text-xs font-mono text-(--base-07)">{selectedServer.nodeAddress}:{selectedServer.hostPort}</span>
                                    <button
                                        onClick={() => navigator.clipboard.writeText(`${selectedServer.nodeAddress}:${selectedServer.hostPort}`)}
                                        className="text-(--base-06) hover:text-(--base-09) transition-colors ml-0.5"
                                        title="Copy address"
                                    >
                                        <Copy size={11} />
                                    </button>
                                </div>
                            </div>
                        )}
                        {gatewayEnabled && serverRoutes.length > 0 && (
                            <div className="flex items-center gap-2 flex-wrap">
                                <span className="text-[10px] font-mono uppercase tracking-[0.08em] text-(--base-05)">Gateway</span>
                                {serverRoutes.slice(0, 3).map(route => (
                                    <div key={route.ID} className="flex items-center gap-1.5 bg-(--accent-ghost) border border-(--accent-border) rounded-md px-2.5 py-1">
                                        <Globe size={11} className="text-(--accent-light) shrink-0" />
                                        <span className="text-xs font-mono text-(--accent-light)">{route.domain}</span>
                                        <button
                                            onClick={() => navigator.clipboard.writeText(route.domain)}
                                            className="text-(--accent-light)/60 hover:text-(--accent-light) transition-colors ml-0.5"
                                            title="Copy domain"
                                        >
                                            <Copy size={11} />
                                        </button>
                                    </div>
                                ))}
                            </div>
                        )}
                        {routingMode === 'gateway' && gatewayEnabled && serverRoutes.length === 0 && (
                            <div className="flex items-center gap-2">
                                <AlertTriangle size={12} className="text-(--warning)" />
                                <span className="text-xs text-(--warning) font-medium">No gateway route configured</span>
                            </div>
                        )}
                    </div>
                )}

                {/* Tab Bar */}
                <div className="flex space-x-1 overflow-x-auto hide-scrollbar">
                    {tabs.map(tab => {
                        const href = `/servers/${selectedServer.id}/${tab.slug}`;
                        const isActive = pathname === href;
                        if (tab.disabled) {
                            return (
                                <span
                                    key={tab.slug}
                                    className="flex items-center space-x-2 px-4 py-3 border-b-2 border-transparent text-(--base-06) opacity-30 cursor-not-allowed whitespace-nowrap"
                                >
                                    <DynamicIcon name={tab.icon} size={20} />
                                    <span>{tab.label}</span>
                                </span>
                            );
                        }
                        return (
                            <Link
                                key={tab.slug}
                                href={href}
                                replace
                                className={`flex items-center space-x-2 px-4 py-3 border-b-2 transition-all whitespace-nowrap ${
                                    isActive
                                        ? 'border-(--accent) text-(--accent-light) font-semibold'
                                        : 'border-transparent text-(--base-07) hover:text-(--base-09) hover:border-(--base-04)'
                                }`}
                            >
                                <DynamicIcon name={tab.icon} size={20} />
                                <span>{tab.label}</span>
                            </Link>
                        );
                    })}
                </div>
            </div>

            <div className="flex-1 overflow-y-auto p-6">{children}</div>

            {/* Delete Confirmation Popup */}
            {showDeletePopup && (
                <div className="modal-overlay animate-fade-in">
                    <div className="modal-panel w-full max-w-md">
                        <div className="modal-header">
                            <h3 className="modal-title text-(--error-light) flex items-center gap-2">
                                <AlertTriangle size={20} />
                                Delete Server
                            </h3>
                        </div>
                        <div className="modal-body">
                            <p className="text-sm text-(--base-07)">
                                Are you sure you want to delete <span className="font-semibold text-(--base-09)">{selectedServer.name}</span>?
                                This cannot be undone and all server data will be lost.
                            </p>
                        </div>
                        <div className="modal-footer">
                            <button onClick={() => setShowDeletePopup(false)} className="btn btn-secondary px-4 py-2 text-sm">Cancel</button>
                            <button
                                onClick={handleDelete}
                                disabled={deleteCountdown > 0}
                                className="btn btn-danger px-4 py-2 text-sm disabled:opacity-50 disabled:cursor-not-allowed"
                            >
                                {deleteCountdown > 0 ? `Delete (${deleteCountdown}s)` : 'Delete'}
                            </button>
                        </div>
                    </div>
                </div>
            )}

            {/* Kill Confirmation */}
            {showKillConfirm && (
                <div className="modal-overlay animate-fade-in" onClick={() => setShowKillConfirm(false)}>
                    <div className="modal-panel max-w-sm" onClick={e => e.stopPropagation()}>
                        <div className="modal-header">
                            <h3 className="modal-title flex items-center gap-2">
                                <Skull size={20} className="text-(--error)" />
                                Kill Server?
                            </h3>
                        </div>
                        <div className="modal-body">
                            <p className="text-sm text-(--base-07)">
                                The container will be stopped immediately. Unsaved data will be lost.
                            </p>
                        </div>
                        <div className="modal-footer">
                            <button onClick={() => setShowKillConfirm(false)} className="btn btn-secondary px-4 py-2 text-sm">Cancel</button>
                            <button onClick={() => { handlePower('kill'); setShowKillConfirm(false); }} className="btn btn-danger px-4 py-2 text-sm">Kill</button>
                        </div>
                    </div>
                </div>
            )}

            {/* Edit Resources Popup */}
            {showEditResourcesPopup && (
                <div className="modal-overlay animate-fade-in">
                    <div className="modal-panel w-full max-w-lg">
                        <div className="modal-header">
                            <h3 className="modal-title flex items-center gap-2">
                                <SlidersHorizontal size={18} />
                                Edit Resources
                            </h3>
                        </div>
                        <div className="modal-body space-y-4">
                            <div className="flex flex-col gap-[5px]">
                                <label className="input-label">RAM (MB)</label>
                                <input type="number" min={256} step={256} value={editRam} onChange={e => setEditRam(Number(e.target.value))} className="input-field w-full" />
                            </div>
                            <div className="flex flex-col gap-[5px]">
                                <label className="input-label">CPU Limit (Cores)</label>
                                <input type="number" min={0} step={0.5} value={editCpuLimit} onChange={e => setEditCpuLimit(Number(e.target.value))} placeholder="0 = unlimited" className="input-field w-full" />
                                <p className="text-xs text-(--base-06)">0 = no limit. Example: 2.0 = 2 cores</p>
                            </div>
                            <div className="flex flex-col gap-[5px]">
                                <label className="input-label">Storage Limit (GB)</label>
                                <input type="number" min={0} step={1} value={editDiskLimit} onChange={e => setEditDiskLimit(Number(e.target.value))} placeholder="0 = unlimited" className="input-field w-full" />
                                <p className="text-xs text-(--base-06)">0 = unlimited</p>
                            </div>

                            {user?.isAdmin && (
                                <div className="border-t border-(--base-03) pt-4 grid grid-cols-2 gap-3">
                                    <div className="flex flex-col gap-[5px]">
                                        <label className="input-label">Host Port</label>
                                        <input type="number" min={0} max={65535} value={editHostPort} onChange={e => setEditHostPort(Number(e.target.value))} placeholder="0 = auto" className="input-field w-full" />
                                        <p className="text-xs text-(--base-06)">0 = auto from range</p>
                                    </div>
                                    <div className="flex flex-col gap-[5px]">
                                        <label className="input-label">Container Port</label>
                                        <input type="number" min={1} max={65535} value={editContainerPort} onChange={e => setEditContainerPort(Number(e.target.value))} className="input-field w-full" />
                                    </div>
                                </div>
                            )}

                            {user?.isAdmin && storagePaths.length >= 1 && (
                                <div className="border-t border-(--base-03) pt-4 flex flex-col gap-3">
                                    <div className="flex items-center gap-2">
                                        <MoveHorizontal size={14} className="text-(--accent-light)" />
                                        <span className="input-label mb-0">{storagePaths.length > 1 ? 'Storage Path Migration' : 'Storage Path'}</span>
                                    </div>
                                    <div className="flex flex-col gap-[5px]">
                                        <label className="text-[10px] font-mono uppercase tracking-[0.08em] text-(--base-06)">Current Path</label>
                                        <p className="text-xs font-mono text-(--base-07) bg-(--base-02) border border-(--base-03) rounded-md px-3 py-2 truncate">
                                            {storageCurrentPath || <span className="text-(--base-05) italic">unknown</span>}
                                        </p>
                                    </div>
                                    {storagePaths.length > 1 && (
                                        <>
                                            <div className="flex gap-2 items-end">
                                                <div className="flex flex-col gap-[5px] flex-1 min-w-0">
                                                    <label className="text-[10px] font-mono uppercase tracking-[0.08em] text-(--base-06)">Migrate To</label>
                                                    <select value={storageMigrateTarget} onChange={e => setStorageMigrateTarget(e.target.value)} className="input-field text-sm" disabled={storageMigrating}>
                                                        {storagePaths.filter(p => p.path !== storageCurrentPath).map(p => (
                                                            <option key={p.path} value={p.path}>{p.path} — {(p.free_bytes / 1073741824).toFixed(1)} GB free</option>
                                                        ))}
                                                    </select>
                                                </div>
                                                <button type="button" onClick={handleMigrateStorage} disabled={storageMigrating || !storageMigrateTarget} className="btn btn-secondary px-3 py-2 text-sm flex items-center gap-1.5 shrink-0">
                                                    {storageMigrating ? <><RefreshCw size={14} className="animate-spin" /> Moving...</> : <><MoveHorizontal size={14} /> Migrate</>}
                                                </button>
                                            </div>
                                            {storageMigrateMsg && (
                                                <p className={`text-xs ${storageMigrateMsg.includes('queued') || storageMigrateMsg.includes('Migration') ? 'text-(--success-light)' : 'text-(--error-light)'}`}>
                                                    {storageMigrateMsg}
                                                </p>
                                            )}
                                            <p className="text-xs text-(--base-06)">Server will be stopped during migration. Restart it manually after.</p>
                                        </>
                                    )}
                                </div>
                            )}

                            {user?.isAdmin && (
                                <div className="border-t border-(--base-03) pt-4">
                                    <button type="button" onClick={() => setEditResourcesAdvancedOpen(o => !o)} className="flex items-center gap-2 text-xs text-(--base-06) hover:text-(--base-08) transition-colors w-full">
                                        <ChevronDown size={13} className={`transition-transform ${editResourcesAdvancedOpen ? 'rotate-180' : ''}`} />
                                        Advanced
                                    </button>
                                    {editResourcesAdvancedOpen && (
                                        <div className="mt-3 flex flex-col gap-[5px]">
                                            <label className="input-label">CPU Pinning (cpuset)</label>
                                            <input type="text" value={editCpusetCpus} onChange={e => setEditCpusetCpus(e.target.value)} placeholder="e.g. 0-7 or 0,2,4,6 — empty = no pinning" className="input-mono w-full" />
                                            <p className="text-xs text-(--base-06)">Pin this container to specific CPU cores (useful for AMD 3D V-Cache). Resets if the server is migrated to a different node.</p>
                                        </div>
                                    )}
                                </div>
                            )}
                        </div>
                        <div className="modal-footer">
                            <button onClick={() => setShowEditResourcesPopup(false)} className="btn btn-secondary px-4 py-2 text-sm">Cancel</button>
                            <button onClick={handleSaveResources} className="btn btn-primary px-4 py-2 text-sm">Save & Restart</button>
                        </div>
                    </div>
                </div>
            )}
        </main>
    );
}
