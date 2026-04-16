"use client";

import React, { useState, useEffect, useCallback } from 'react';
import { useRouter } from 'next/navigation';
import { logout, getProfile, updateProfile as apiUpdateProfile, getModules, getServers, deleteServer, updateServerName, updateServerResources, serverPower, getFeatureSettings, getServerStoragePath, migrateServerStorage, getServerRoutes, getRoutingMode, GatewayRoute, AppModule, Server, User, TabPermissions, ServerStats, StoragePathInfo, RoutingMode, FileAccessMode } from '@/lib/api';
import { API_URL } from '@/lib/api/core';
import { Server as ServerIcon, PlusCircle, Wrench, ChevronDown, UserCog, LogOut, Pencil, SlidersHorizontal, Trash2, AlertTriangle, Play, Square, RotateCcw, Skull, House, Terminal, FolderOpen, Settings, Users, HardDrive, MoveHorizontal, RefreshCw, Copy, Globe, Link2 } from 'lucide-react';
import { DynamicIcon } from '@/lib/icons';
import ProfilePopup from '@/components/ProfilePopup';
import Navbar from '@/components/Navbar';
import Sidebar from '@/components/Sidebar';

// Views
import OverviewView from '@/views/OverviewView';
import ConsoleView from '@/views/ConsoleView';
import FileBrowserView from '@/views/FileBrowserView';
import PlaceholderView from '@/views/PlaceholderView';
import SetupView from '@/views/SetupView';
import MembersView from '@/views/MembersView';
import LibraryView from '@/views/LibraryView';
import NetworkView from '@/views/NetworkView';
import GatewayView from '@/views/GatewayView';
import InfrastructureView from '@/views/InfrastructureView';

// Components
import CreateServerWizard from '@/components/CreateServerWizard';
import SettingsPanel from '@/components/settings/SettingsPanel';

export default function Dashboard() {
    const [user, setUser] = useState<User | null>(null);
    const [isAuthChecked, setIsAuthChecked] = useState(false);
    
    const [modules, setModules] = useState<AppModule[]>([]);
    const [activeModuleId, setActiveModuleId] = useState<string>('servers');
    
    // Server State
    const [servers, setServers] = useState<Server[]>([]);
    const [selectedServerId, setSelectedServerId] = useState<number | null>(null);
    const [serverDetailView, setServerDetailView] = useState('setup');
    
    // Dropdown & Popups
    const [isProfileDropdownOpen, setIsProfileDropdownOpen] = useState(false);
    const [showProfilePopup, setShowProfilePopup] = useState(false);
    const [popupError, setPopupError] = useState("");
    const [popupSuccess, setPopupSuccess] = useState("");
    const [showCreateServerModal, setShowCreateServerModal] = useState(false);

    // Editable server name
    const [isEditingName, setIsEditingName] = useState(false);
    const [editedName, setEditedName] = useState('');

    // Delete popup
    const [showDeletePopup, setShowDeletePopup] = useState(false);
    const [deleteCountdown, setDeleteCountdown] = useState(5);

    // Edit resources popup
    const [showEditResourcesPopup, setShowEditResourcesPopup] = useState(false);
    const [editRam, setEditRam] = useState(0);
    const [editCpuLimit, setEditCpuLimit] = useState(0);
    const [editDiskLimit, setEditDiskLimit] = useState(0);
    const [editCpusetCpus, setEditCpusetCpus] = useState('');
    const [editResourcesAdvancedOpen, setEditResourcesAdvancedOpen] = useState(false);

    // Storage migration state (admin only)
    const [storageCurrentPath, setStorageCurrentPath] = useState('');
    const [storagePaths, setStoragePaths] = useState<StoragePathInfo[]>([]);
    const [storageMigrateTarget, setStorageMigrateTarget] = useState('');
    const [storageMigrating, setStorageMigrating] = useState(false);
    const [storageMigrateMsg, setStorageMigrateMsg] = useState('');

    // Feature flags
    const [proxiesEnabled, setProxiesEnabled] = useState(true);

    // Live stats
    const [liveStats, setLiveStats] = useState<ServerStats | null>(null);

    // Power controls
    const [powerLoading, setPowerLoading] = useState<string | null>(null);
    const [waitingForStatus, setWaitingForStatus] = useState<string | null>(null);
    const [killCooldown, setKillCooldown] = useState(false);
    const [showKillConfirm, setShowKillConfirm] = useState(false);

    // Server routes (gateway)
    const [serverRoutes, setServerRoutes] = useState<GatewayRoute[]>([]);

    // Routing mode
    const [routingMode, setRoutingMode] = useState<RoutingMode>('ip_port');
    const [fileAccessMode, setFileAccessMode] = useState<FileAccessMode>('sftp');

    // Edit resources — port fields
    const [editHostPort, setEditHostPort] = useState(0);
    const [editContainerPort, setEditContainerPort] = useState(25565);

    const router = useRouter();
    
    const activeModule = modules.find(m => String(m.id) === activeModuleId || m.name.toLowerCase() === activeModuleId);
    const selectedServer = servers.find(s => s.id === selectedServerId);

    // Click outside closes the dropdown
    useEffect(() => {
        const handleClickOutside = (e: MouseEvent) => {
            const target = e.target as HTMLElement;
            if (!target.closest('.profile-dropdown-container')) {
                setIsProfileDropdownOpen(false);
            }
        };
        document.addEventListener('click', handleClickOutside);
        return () => document.removeEventListener('click', handleClickOutside);
    }, []);


    const loadData = useCallback(async () => {
        const profile = await getProfile();
        if (profile) {
             setUser(profile);
             
             const modulesResult = await getModules();
             if(modulesResult.success && modulesResult.modules) {
                setModules(modulesResult.modules);
                if(!activeModuleId || activeModuleId === 'servers') {
                    const serverMod = modulesResult.modules.find((m: AppModule) => m.name === 'Servers');
                    if(serverMod) setActiveModuleId(String(serverMod.id));
                    else if(modulesResult.modules.length > 0) setActiveModuleId(String(modulesResult.modules[0].id));
                }
             }

             refreshServers();

             getFeatureSettings().then(res => {
                 if (res.success && res.settings) {
                     setProxiesEnabled(res.settings.proxyEnabled);
                 }
             });

             getRoutingMode().then(res => {
                 if (res.success) {
                     setRoutingMode(res.mode || 'ip_port');
                     setFileAccessMode(res.fileMode || 'sftp');
                 }
             });

        } else {
            logout(); router.push('/login');
        }
        setIsAuthChecked(true);
    }, [router, activeModuleId]);

    const refreshServers = async () => {
        const serverResult = await getServers();
        if (serverResult.success && serverResult.servers) {
            setServers(serverResult.servers);
            if (!selectedServerId && serverResult.servers.length > 0) {
                setSelectedServerId(serverResult.servers[0].id);
            }
        }
    };

    useEffect(() => {
        const token = localStorage.getItem("token");
        if (!token) { router.push('/login'); return; }
        loadData();
    }, [router, loadData]);

    // Default to Setup tab for pending_setup servers, Overview otherwise
    useEffect(() => {
        if (!selectedServer) return;
        setServerDetailView(selectedServer.status === 'pending_setup' ? 'setup' : 'overview');
    }, [selectedServerId]);

    // SSE stats subscription for selected server
    useEffect(() => {
        setLiveStats(null);
        if (!selectedServer) return;

        const token = localStorage.getItem('token') || localStorage.getItem('authToken') || '';
        const url = `${API_URL}/servers/${selectedServer.id}/stats/stream?token=${encodeURIComponent(token)}`;
        const es = new EventSource(url);

        es.onmessage = (e) => {
            try {
                const data = JSON.parse(e.data) as ServerStats;
                setLiveStats(data);
            } catch { /* ignore parse errors */ }
        };

        return () => {
            es.close();
        };
    }, [selectedServerId]);

    // Poll server list for status updates (without touching selection)
    useEffect(() => {
        const interval = setInterval(async () => {
            const serverResult = await getServers();
            if (serverResult.success && serverResult.servers) {
                setServers(serverResult.servers);
            }
        }, 5000);
        return () => clearInterval(interval);
    }, []);

    const handleProfileUpdate = async (data: any) => {
        setPopupError(""); setPopupSuccess("");
        if (data.newPassword === "passwords-do-not-match") { setPopupError("Passwords do not match."); return; }
        const result = await apiUpdateProfile(data);
        if (result.success) {
            setPopupSuccess("Saved!");
            loadData(); 
            setTimeout(() => { setShowProfilePopup(false); setPopupSuccess(""); }, 1500);
        } else { setPopupError(result.message || "An error occurred."); }
    };

    // --- Name editing ---
    const handleStartEditName = () => {
        setEditedName(selectedServer?.name || '');
        setIsEditingName(true);
    };
    const handleSaveName = async () => {
        if (!selectedServer) return;
        const trimmed = editedName.replace(/\s{2,}/g, ' ').trim();
        if (!trimmed || !/^[a-zA-Z0-9\-+ ]{1,50}$/.test(trimmed)) { setIsEditingName(false); return; }
        await updateServerName(selectedServer.id, trimmed);
        setIsEditingName(false);
        refreshServers();
    };

    // --- Delete popup ---
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
        if (!selectedServer) return;
        await deleteServer(selectedServer.id);
        setShowDeletePopup(false);
        setSelectedServerId(null);
        refreshServers();
    };

    // --- Edit resources popup ---
    const handleOpenEditResources = async () => {
        if (!selectedServer) return;
        setEditRam(selectedServer.memory || 1024);
        setEditCpuLimit(selectedServer.cpuLimit || 0);
        setEditDiskLimit((selectedServer.diskLimit || 0) / 1024);
        setEditHostPort(selectedServer.hostPort || 0);
        setEditContainerPort(selectedServer.containerPort || 25565);
        setEditCpusetCpus(selectedServer.cpusetCpus || '');
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
        if (!selectedServer) return;
        const ports = user?.isAdmin ? { hostPort: editHostPort, containerPort: editContainerPort } : undefined;
        await updateServerResources(selectedServer.id, editRam, editCpuLimit, editDiskLimit > 0 ? editDiskLimit * 1024 : 0, ports, editCpusetCpus || undefined);
        setShowEditResourcesPopup(false);
        refreshServers();
    };

    const handleMigrateStorage = async () => {
        if (!selectedServer || !storageMigrateTarget) return;
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

    const getStatusColor = (status: string) => {
        switch (status) {
            case 'online': return 'bg-(--success-light)';
            case 'stopped':
            case 'offline': return 'bg-(--error)';
            case 'disk_full': return 'bg-(--error)';
            default: return 'bg-(--warning) animate-pulse';
        }
    };

    // Power controls
    const isServerOffline = selectedServer?.status === 'stopped' || selectedServer?.status === 'offline' || selectedServer?.status === 'pending_setup' || selectedServer?.status === 'disk_full';
    const isServerOnline = selectedServer?.status === 'online';
    const powerWaiting = waitingForStatus !== null || powerLoading !== null;

    const handlePower = async (action: 'start' | 'stop' | 'restart' | 'kill') => {
        if (!selectedServer) return;
        setPowerLoading(action);
        if (action === 'kill') {
            // Kill overrides any pending wait state from other actions
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

    // Clear waitingForStatus when server reaches expected status
    useEffect(() => {
        if (waitingForStatus && selectedServer?.status === waitingForStatus) {
            setWaitingForStatus(null);
        }
    }, [selectedServer?.status, waitingForStatus]);

    // Fallback: reset waitingForStatus after 60s
    useEffect(() => {
        if (!waitingForStatus) return;
        const timeout = setTimeout(() => setWaitingForStatus(null), 60000);
        return () => clearTimeout(timeout);
    }, [waitingForStatus]);

    // Fetch per-server gateway routes when server or modules change
    useEffect(() => {
        setServerRoutes([]);
        if (!selectedServer) return;
        const gatewayOn = modules.some(m => m.name === 'Gateway' && m.isEnabled);
        if (!gatewayOn) return;
        getServerRoutes(selectedServer.id).then(res => {
            if (Array.isArray(res)) setServerRoutes(res);
            else if (res && Array.isArray(res.routes)) setServerRoutes(res.routes);
        });
    }, [selectedServerId, modules]);

    // When a server switches from pending_setup to another status, default to 'setup' tab first
    const isPendingSetup = selectedServer?.status === 'pending_setup';
    const isDiskFull = selectedServer?.status === 'disk_full';
    const gatewayModuleEnabled = modules.some(m => m.name === 'Gateway' && m.isEnabled);
    const libraryEnabled = modules.some(m => m.name === 'Library' && m.isEnabled);

    const renderContent = () => {
        if (activeModuleId === 'settings' && user) {
            if (!user.isAdmin) return <div className="text-center mt-20 text-(--error) font-semibold text-xl font-display">Access denied. Administrator rights required.</div>;
            return <SettingsPanel modules={modules} onModulesChange={loadData} currentUser={user} />;
        }

        if (!activeModule) return <div className="flex h-full items-center justify-center text-(--base-07)">Loading modules...</div>;
        if (activeModule.type === 'iframe' && activeModule.url) return <div className="w-full h-full rounded-xl overflow-hidden border border-(--base-03)"><iframe src={activeModule.url} className="w-full h-full" title={activeModule.name} /></div>;
        if (activeModule.name === 'Modpacks') return <PlaceholderView viewName="Modpack Manager (Internal)" />;

        if (activeModule.name === 'Library') {
            return <LibraryView />;
        }

        if (activeModule.name === 'Gateway') {
            return <GatewayView />;
        }

        if (activeModule.name === 'Infrastructure') {
            return <InfrastructureView />;
        }

        if (activeModule.name === 'Servers') {
            if (!selectedServer) return (
                <div className="flex h-full items-center justify-center text-(--base-06) flex-col">
                    <ServerIcon size={60} className="mb-4 opacity-40" />
                    <p className="text-lg font-display font-bold mb-6 text-(--base-07)">No server selected</p>
                    <button onClick={() => setShowCreateServerModal(true)} className="btn btn-primary px-6 py-3 text-sm">
                        <PlusCircle size={16} />
                        Create new server
                    </button>
                </div>
            );

            switch (serverDetailView) {
                case 'setup': return <SetupView server={selectedServer} libraryEnabled={libraryEnabled} onSetupComplete={refreshServers} />;
                case 'overview': return <OverviewView server={selectedServer} />;
                case 'console': return <ConsoleView server={selectedServer} liveStats={liveStats} />;
                case 'file-browser': return (
                    <div className="flex flex-col gap-3 h-full">
                        {fileAccessMode !== 'beam' && selectedServer.nodeAddress && (
                            <div className="flex items-center gap-3 px-3 py-2 bg-(--base-02) border border-(--base-03) rounded-lg shrink-0">
                                <Terminal size={13} className="text-(--base-06) shrink-0" />
                                <code className="text-xs font-mono text-(--base-07) flex-1 min-w-0 truncate">
                                    sftp {user?.username}@{selectedServer.nodeAddress} -p 2222
                                </code>
                                <button
                                    onClick={() => navigator.clipboard.writeText(`sftp ${user?.username}@${selectedServer.nodeAddress} -p 2222`)}
                                    className="text-(--base-06) hover:text-(--base-09) transition-colors shrink-0"
                                    title="Copy SFTP command"
                                >
                                    <Copy size={12} />
                                </button>
                                <span className="text-xs text-(--base-05) shrink-0 hidden sm:block">Password = your Dylaris password</span>
                            </div>
                        )}
                        <FileBrowserView serverUuid={selectedServer.uuid} currentServerPath={selectedServer.activeSubServer || ''} />
                    </div>
                );
                case 'network': return <NetworkView server={selectedServer} allServers={servers} onServerSelect={(id) => { setSelectedServerId(id); }} onRefreshServers={refreshServers} />;
                case 'members': return <MembersView server={selectedServer} />;
                default: return <PlaceholderView viewName={serverDetailView} />;
            }
        }
        return <div className="text-center mt-10">Unknown module: {activeModule.name}</div>;
    };

    if (!isAuthChecked || !user) return <div className="flex h-screen items-center justify-center bg-(--base-00) text-(--base-07)">Loading...</div>;

    // --- NEW LAYOUT: Flex Column (Navbar Top, Content Bottom) ---
    return (
        <div className="flex flex-col h-screen bg-(--base-00) text-(--base-09) font-body overflow-hidden">
            
            {/* 1. TOP NAVBAR (Full width across the entire page) */}
            <div className="relative z-30 shrink-0">
                <Navbar modules={modules} activeView={activeModuleId} onChangeView={setActiveModuleId}>
                    
                    {/* System Settings */}
                    {user.isAdmin && (
                        <button
                            onClick={() => setActiveModuleId('settings')}
                            className={`flex items-center space-x-2 px-3 py-1.5 rounded-md transition-colors font-medium border mr-2 ${
                                activeModuleId === 'settings'
                                    ? 'bg-(--accent-ghost) text-(--accent-light) border-(--accent-border)'
                                    : 'text-(--base-07) hover:bg-(--base-04)/50 hover:text-(--base-09) border-transparent'
                            }`}
                        >
                            <Wrench size={20} />
                            <span className="text-sm hidden md:block">Settings</span>
                        </button>
                    )}

                    {/* User Profile Dropdown */}
                    <div className="relative profile-dropdown-container border-l border-(--base-03) pl-3">
                        <button
                            onClick={() => setIsProfileDropdownOpen(!isProfileDropdownOpen)}
                            className={`flex items-center space-x-2 px-2 py-1.5 rounded-md transition-colors border border-transparent ${isProfileDropdownOpen ? 'bg-(--base-03)' : 'hover:bg-(--base-03)'}`}
                        >
                            <div className="w-8 h-8 rounded-full bg-(--accent-dim) flex items-center justify-center text-(--accent-light) font-semibold text-sm overflow-hidden border border-(--base-04)">
                                {user.minecraftUsername ? (
                                    <img src={`https://cravatar.eu/helmavatar/${user.minecraftUsername}/32.png`} alt="" className="w-full h-full object-cover" />
                                ) : (
                                    user.username.charAt(0).toUpperCase()
                                )}
                            </div>
                            <span className="font-medium hidden md:block text-sm max-w-[100px] truncate text-(--base-09)">{user.username}</span>
                            <ChevronDown size={20} className="text-(--base-06) transition-transform duration-200" style={{ transform: isProfileDropdownOpen ? 'rotate(180deg)' : 'rotate(0deg)' }} />
                        </button>

                        {isProfileDropdownOpen && (
                            <div className="dropdown-menu right-0 mt-3 w-48 animate-fade-in origin-top-right">
                                <div className="px-4 py-2 border-b border-(--base-03) mb-1">
                                    <div className="dropdown-label">Logged in as</div>
                                    <div className="font-medium truncate text-(--base-09)">{user.username}</div>
                                </div>
                                <button onClick={() => { setIsProfileDropdownOpen(false); setShowProfilePopup(true); }} className="dropdown-item">
                                    <UserCog size={20} className="mr-3" /> Edit Profile
                                </button>
                                <button onClick={() => { setIsProfileDropdownOpen(false); logout(); router.push('/login'); }} className="dropdown-item text-(--error) hover:bg-(--error-ghost) mt-1">
                                    <LogOut size={20} className="mr-3" /> Logout
                                </button>
                            </div>
                        )}
                    </div>
                </Navbar>
            </div>

            {/* 2. MAIN CONTENT WRAPPER (Below the Navbar) */}
            <div className="flex flex-1 overflow-hidden relative">
                
                {/* SIDEBAR (Only shown in the "Servers" view) */}
                {activeModule && activeModule.name === 'Servers' && activeModuleId !== 'settings' && (
                    <Sidebar
                        servers={servers}
                        activeServerId={selectedServerId}
                        onServerSelect={(id) => { setSelectedServerId(id); }}
                        onNewServer={() => setShowCreateServerModal(true)}
                        currentUser={user}
                        proxiesEnabled={proxiesEnabled}
                    />
                )}

                {/* CONTENT AREA (Right of the Sidebar or Full-Width) */}
                <main className="flex-1 flex flex-col overflow-hidden relative z-10">
                    
                    {/* Server Header */}
                    {activeModuleId !== 'settings' && activeModule && activeModule.name === 'Servers' && selectedServer && (
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
                                            <button
                                                onClick={() => setSelectedServerId(proxy.id)}
                                                className="text-[10px] bg-(--accent-ghost) px-2 py-0.5 rounded-sm text-(--accent-light) font-mono uppercase tracking-[0.08em] flex items-center gap-1 hover:bg-(--accent)/20 transition-colors"
                                            >
                                                {proxy.name}
                                            </button>
                                        ) : null;
                                    })()}
                                    <span className="text-xs text-(--base-07)">
                                        Owner: <span className="font-medium text-(--base-09)">{selectedServer.ownerName === user.username ? 'You' : (selectedServer.ownerName || `ID: ${selectedServer.ownerId}`)}</span>
                                    </span>
                                </div>
                                <div className="flex items-center gap-2">
                                    {/* Power Buttons */}
                                    {(() => {
                                        const isOwner = selectedServer.role !== 'invited' && selectedServer.role !== 'inherited';
                                        const canPower = isOwner || (selectedServer.permissions?.power);
                                        return (
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
                                            title={isDiskFull ? "Speicher voll — Dateien loeschen oder Limit erhoehen" : canPower ? "Start server" : "No permission"}
                                        >
                                            <Play size={16} />
                                            <span className="text-xs font-semibold">Start</span>
                                        </button>
                                        <button
                                            onClick={() => handlePower('restart')}
                                            disabled={!canPower || isPendingSetup || isDiskFull || powerWaiting || isServerOffline}
                                            className="flex items-center gap-1.5 px-3 py-1.5 rounded-md bg-(--warning-ghost) hover:bg-(--warning)/15 transition-colors disabled:opacity-30 disabled:cursor-not-allowed border border-(--warning)/15"
                                            title={isDiskFull ? "Speicher voll — Dateien loeschen oder Limit erhoehen" : canPower ? "Restart server" : "No permission"}
                                        >
                                            <RotateCcw size={16} className="text-(--warning)" />
                                            <span className="text-xs font-semibold text-(--warning)">Restart</span>
                                        </button>
                                        <button
                                            onClick={() => handlePower('stop')}
                                            disabled={!canPower || isPendingSetup || powerWaiting || isServerOffline}
                                            className="flex items-center gap-1.5 px-3 py-1.5 rounded-md bg-(--error-ghost) hover:bg-(--error)/15 transition-colors disabled:opacity-30 disabled:cursor-not-allowed border border-(--error)/15"
                                            title={canPower ? "Stop server" : "No permission"}
                                        >
                                            <Square size={16} className="text-(--error)" />
                                            <span className="text-xs font-semibold text-(--error)">Stop</span>
                                        </button>
                                        <button
                                            onClick={() => setShowKillConfirm(true)}
                                            disabled={!canPower || killCooldown || isServerOffline}
                                            className="flex items-center gap-1.5 px-3 py-1.5 rounded-md bg-(--error-ghost) hover:bg-(--error)/15 transition-colors disabled:opacity-30 disabled:cursor-not-allowed border border-(--error)/15"
                                            title={canPower ? "Force kill container" : "No permission"}
                                        >
                                            <Skull size={16} className="text-(--error)" />
                                            <span className="text-xs font-semibold text-(--error)">Kill</span>
                                        </button>
                                    </div>
                                        );
                                    })()}
                                    {/* Separator */}
                                    {user?.isAdmin && (
                                        <div className="w-px h-6 bg-(--base-04)" />
                                    )}
                                    {/* Admin Buttons */}
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
                            {/* Row 2.5: Connection Info */}
                            {!isPendingSetup && (
                                (routingMode !== 'gateway' && selectedServer.nodeAddress && (selectedServer.hostPort ?? 0) > 0) ||
                                (gatewayModuleEnabled && serverRoutes.length > 0) ||
                                (routingMode === 'gateway' && gatewayModuleEnabled && serverRoutes.length === 0)
                            ) && (
                                <div className={`flex items-center gap-4 py-2 border-t flex-wrap ${
                                    routingMode === 'gateway' && gatewayModuleEnabled && serverRoutes.length === 0
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
                                    {gatewayModuleEnabled && serverRoutes.length > 0 && (
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
                                    {routingMode === 'gateway' && gatewayModuleEnabled && serverRoutes.length === 0 && (
                                        <div className="flex items-center gap-2">
                                            <AlertTriangle size={12} className="text-(--warning)" />
                                            <span className="text-xs text-(--warning) font-medium">No gateway route configured</span>
                                        </div>
                                    )}
                                </div>
                            )}
                            {/* Row 3: Tab Bar */}
                            <div className="flex space-x-1 overflow-x-auto hide-scrollbar">
                                {(() => {
                                    const isOwner = selectedServer.role !== 'invited' && selectedServer.role !== 'inherited';
                                    const perms = selectedServer.permissions;
                                    const tabDisabled = (perm: keyof TabPermissions) =>
                                        isPendingSetup || (!isOwner && perms && !perms[perm]);
                                    return [
                                        { id: 'setup', icon: 'wrench', label: 'Setup', disabled: !isOwner && perms && !perms.setup },
                                        { id: 'overview', icon: 'house', label: 'Overview', disabled: isPendingSetup },
                                        { id: 'console', icon: 'square-terminal', label: 'Console', disabled: tabDisabled('console') },
                                        { id: 'file-browser', icon: 'folder-open', label: 'Files', disabled: tabDisabled('files') },
                                        { id: 'config', icon: 'settings', label: 'Configuration', disabled: tabDisabled('config') },
                                        { id: 'network', icon: 'network', label: 'Network', disabled: tabDisabled('network') },
                                        { id: 'members', icon: 'users', label: 'Members', disabled: !isOwner && (!perms || !perms.members) },
                                    ];
                                })().map(item => (
                                    <button
                                        key={item.id}
                                        onClick={() => !item.disabled && setServerDetailView(item.id)}
                                        disabled={item.disabled}
                                        className={`flex items-center space-x-2 px-4 py-3 border-b-2 transition-all whitespace-nowrap ${
                                            item.disabled
                                                ? 'border-transparent text-(--base-06) opacity-30 cursor-not-allowed'
                                                : serverDetailView === item.id
                                                ? 'border-(--accent) text-(--accent-light) font-semibold'
                                                : 'border-transparent text-(--base-07) hover:text-(--base-09) hover:border-(--base-04)'
                                        }`}
                                    >
                                        <DynamicIcon name={item.icon} size={20} />
                                        <span>{item.label}</span>
                                    </button>
                                ))}
                            </div>
                         </div>
                    )}
                    <div className="flex-1 overflow-y-auto p-6">{renderContent()}</div>
                </main>
            </div>
            
            {/* Popups */}
            {showProfilePopup && user && <ProfilePopup currentUser={user} onClose={() => setShowProfilePopup(false)} onUpdate={handleProfileUpdate} error={popupError} success={popupSuccess} />}
            {showCreateServerModal && <CreateServerWizard isOpen={showCreateServerModal} onClose={() => { setShowCreateServerModal(false); refreshServers(); }} proxiesEnabled={proxiesEnabled} />}

            {/* Delete Confirmation Popup */}
            {showDeletePopup && selectedServer && (
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
                            <button onClick={() => setShowDeletePopup(false)} className="btn btn-secondary px-4 py-2 text-sm">
                                Cancel
                            </button>
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

            {/* Kill Confirmation Dialog */}
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
                            <button onClick={() => setShowKillConfirm(false)} className="btn btn-secondary px-4 py-2 text-sm">
                                Cancel
                            </button>
                            <button onClick={() => { handlePower('kill'); setShowKillConfirm(false); }} className="btn btn-danger px-4 py-2 text-sm">
                                Kill
                            </button>
                        </div>
                    </div>
                </div>
            )}

            {/* Edit Resources Popup */}
            {showEditResourcesPopup && selectedServer && (
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
                                <input type="number" min={256} step={256} value={editRam} onChange={e => setEditRam(Number(e.target.value))}
                                    className="input-field w-full" />
                            </div>
                            <div className="flex flex-col gap-[5px]">
                                <label className="input-label">CPU Limit (Cores)</label>
                                <input type="number" min={0} step={0.5} value={editCpuLimit} onChange={e => setEditCpuLimit(Number(e.target.value))}
                                    placeholder="0 = unlimited"
                                    className="input-field w-full" />
                                <p className="text-xs text-(--base-06)">0 = no limit. Example: 2.0 = 2 cores</p>
                            </div>
                            <div className="flex flex-col gap-[5px]">
                                <label className="input-label">Storage Limit (GB)</label>
                                <input type="number" min={0} step={1} value={editDiskLimit} onChange={e => setEditDiskLimit(Number(e.target.value))}
                                    placeholder="0 = unlimited"
                                    className="input-field w-full" />
                                <p className="text-xs text-(--base-06)">0 = unlimited</p>
                            </div>

                            {/* Port fields — admin only */}
                            {user?.isAdmin && (
                                <div className="border-t border-(--base-03) pt-4 grid grid-cols-2 gap-3">
                                    <div className="flex flex-col gap-[5px]">
                                        <label className="input-label">Host Port</label>
                                        <input type="number" min={0} max={65535} value={editHostPort} onChange={e => setEditHostPort(Number(e.target.value))}
                                            placeholder="0 = auto"
                                            className="input-field w-full" />
                                        <p className="text-xs text-(--base-06)">0 = auto from range</p>
                                    </div>
                                    <div className="flex flex-col gap-[5px]">
                                        <label className="input-label">Container Port</label>
                                        <input type="number" min={1} max={65535} value={editContainerPort} onChange={e => setEditContainerPort(Number(e.target.value))}
                                            className="input-field w-full" />
                                    </div>
                                </div>
                            )}

                            {/* Storage Path — admin only, when path info available */}
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
                                                    <select
                                                        value={storageMigrateTarget}
                                                        onChange={e => setStorageMigrateTarget(e.target.value)}
                                                        className="input-field text-sm"
                                                        disabled={storageMigrating}
                                                    >
                                                        {storagePaths
                                                            .filter(p => p.path !== storageCurrentPath)
                                                            .map(p => (
                                                                <option key={p.path} value={p.path}>
                                                                    {p.path} — {(p.free_bytes / 1073741824).toFixed(1)} GB free
                                                                </option>
                                                            ))
                                                        }
                                                    </select>
                                                </div>
                                                <button
                                                    type="button"
                                                    onClick={handleMigrateStorage}
                                                    disabled={storageMigrating || !storageMigrateTarget}
                                                    className="btn btn-secondary px-3 py-2 text-sm flex items-center gap-1.5 shrink-0"
                                                >
                                                    {storageMigrating
                                                        ? <><RefreshCw size={14} className="animate-spin" /> Moving...</>
                                                        : <><MoveHorizontal size={14} /> Migrate</>
                                                    }
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

                            {/* Advanced — CPU Pinning (admin only) */}
                            {user?.isAdmin && (
                                <div className="border-t border-(--base-03) pt-4">
                                    <button
                                        type="button"
                                        onClick={() => setEditResourcesAdvancedOpen(o => !o)}
                                        className="flex items-center gap-2 text-xs text-(--base-06) hover:text-(--base-08) transition-colors w-full"
                                    >
                                        <ChevronDown size={13} className={`transition-transform ${editResourcesAdvancedOpen ? 'rotate-180' : ''}`} />
                                        Advanced
                                    </button>
                                    {editResourcesAdvancedOpen && (
                                        <div className="mt-3 flex flex-col gap-[5px]">
                                            <label className="input-label">CPU Pinning (cpuset)</label>
                                            <input
                                                type="text"
                                                value={editCpusetCpus}
                                                onChange={e => setEditCpusetCpus(e.target.value)}
                                                placeholder="e.g. 0-7 or 0,2,4,6 — empty = no pinning"
                                                className="input-mono w-full"
                                            />
                                            <p className="text-xs text-(--base-06)">Pin this container to specific CPU cores (useful for AMD 3D V-Cache). Resets if the server is migrated to a different node.</p>
                                        </div>
                                    )}
                                </div>
                            )}
                        </div>
                        <div className="modal-footer">
                            <button onClick={() => setShowEditResourcesPopup(false)} className="btn btn-secondary px-4 py-2 text-sm">
                                Cancel
                            </button>
                            <button onClick={handleSaveResources} className="btn btn-primary px-4 py-2 text-sm">
                                Save & Restart
                            </button>
                        </div>
                    </div>
                </div>
            )}
        </div>
    );
}