"use client";

import React, { useState, useEffect, useMemo, useCallback } from 'react';
import { Server, setupServer, switchSubServer, getFiles, getLibraryFiles, deleteSubServer, createServerRoute, getServerSettings, getServerRoutes, GatewayRoute, CreateRouteRequest } from '@/lib/api';
import { uploadFiles } from '@/lib/api/files';
import { useAppData } from '@/lib/AppDataContext';
import { AlertTriangle, Trash2, RefreshCw } from 'lucide-react';
import { JAVA_IMAGES, recommendJavaForVersion, effectiveMcVersion } from './setup/JavaVersionPicker';
import { VersionEntry, compareVersionsDesc } from './setup/VersionPicker';
import SubServerSidebar from './setup/SubServerSidebar';
import SetupViewMode from './setup/SetupViewMode';
import SetupNewWizard from './setup/SetupNewWizard';
import SetupEditMode from './setup/SetupEditMode';
import RoutesModal from '@/components/RoutesModal';
import { API_URL } from '@/lib/api/core';
const DEFAULT_GC_FLAGS = '-XX:+UseG1GC -XX:MaxHeapFreeRatio=40 -XX:MinHeapFreeRatio=15 -XX:-ShrinkHeapInSteps';

const PROXY_GC_FLAGS = '-XX:+UseG1GC -XX:+ParallelRefProcEnabled -XX:MaxGCPauseMillis=200 ' +
    '-XX:+UnlockExperimentalVMOptions -XX:+DisableExplicitGC -XX:+AlwaysPreTouch ' +
    '-XX:G1NewSizePercent=30 -XX:G1MaxNewSizePercent=40 -XX:G1HeapRegionSize=8M ' +
    '-XX:G1ReservePercent=20 -XX:G1HeapWastePercent=5 -XX:G1MixedGCCountTarget=4 ' +
    '-XX:InitiatingHeapOccupancyPercent=15 -XX:G1MixedGCLiveThresholdPercent=90 ' +
    '-XX:G1RSetUpdatingPauseTimePercent=5 -XX:SurvivorRatio=32 ' +
    '-XX:+PerfDisableSharedMem -XX:MaxTenuringThreshold=1';

const nameRegex = /^[a-zA-Z0-9\-_+]+$/;
function sanitizeName(raw: string): string {
    return raw.replace(/ /g, '_').replace(/[^a-zA-Z0-9\-_+]/g, '');
}

type FormMode = 'view' | 'edit' | 'new';

interface SetupViewProps {
    server: Server;
    onSetupComplete: () => void;
    libraryEnabled?: boolean;
}

export default function SetupView({ server, onSetupComplete, libraryEnabled }: SetupViewProps) {
    const { refreshServers } = useAppData();
    const [subServers, setSubServers] = useState<string[]>([]);
    const [maxSubServers, setMaxSubServers] = useState<number>(3);
    const [formMode, setFormMode] = useState<FormMode>('view');
    const [switchTarget, setSwitchTarget] = useState<string | null>(null);
    const [activeServerMissing, setActiveServerMissing] = useState(false);

    // Form fields
    const [subName, setSubName] = useState('');
    const [subNameError, setSubNameError] = useState('');
    const [javaImage, setJavaImage] = useState(JAVA_IMAGES[0].id);
    const [extraFlags, setExtraFlags] = useState('');
    const [installTab, setInstallTab] = useState<'online' | 'library' | 'upload'>('online');

    // Software list from API
    const [softwareCatalog, setSoftwareCatalog] = useState<{ name: string; type: string }[]>([]);
    const isProxy = server.serverType === 'proxy';
    const filteredSoftware = useMemo(() =>
        softwareCatalog.filter(s => s.type === (isProxy ? 'proxy' : 'game')).map(s => s.name),
        [softwareCatalog, isProxy]
    );

    // Version picker
    const defaultSoftware = isProxy ? 'velocity' : 'paper';
    const [software, setSoftware] = useState(defaultSoftware);
    const [allVersions, setAllVersions] = useState<VersionEntry[]>([]);
    const [selectedMajor, setSelectedMajor] = useState('');
    const [selectedBuild, setSelectedBuild] = useState('');
    const [loadingVersions, setLoadingVersions] = useState(false);

    // Auto-select Java image when MC version changes. We use the build when it
    // is a true MC patch version (Paper "1.20.11" / Vanilla "1.20.6"), and fall
    // back to the major when the build is a loader version (Fabric/Forge).
    useEffect(() => {
        if (formMode === 'view') return;
        const v = effectiveMcVersion(selectedMajor, selectedBuild);
        if (!v) return;
        const rec = recommendJavaForVersion(v);
        if (rec) setJavaImage(rec);
    }, [selectedMajor, selectedBuild, formMode]);

    // Pre-populate version when entering edit mode (handles case where software didn't change)
    useEffect(() => {
        if (formMode !== 'edit' || allVersions.length === 0) return;
        const mcVer = server.minecraftVersion || '';
        const buildNum = server.buildNumber || '';
        if (!mcVer) return;
        if (allVersions.some(v => v.major === mcVer)) {
            setSelectedMajor(mcVer);
            const buildsForMajor = allVersions.filter(v => v.major === mcVer).map(v => v.build);
            if (buildNum && buildsForMajor.includes(buildNum)) {
                setSelectedBuild(buildNum);
            } else {
                setSelectedBuild(buildsForMajor[0] || '');
            }
        }
    }, [formMode]);

    // Library
    const [libraryFiles, setLibraryFiles] = useState<any[]>([]);
    const [libraryPath, setLibraryPath] = useState('');
    const [selectedLibraryFile, setSelectedLibraryFile] = useState('');

    // Upload
    const [uploadFile, setUploadFile] = useState<File | null>(null);
    const [uploadStructure, setUploadStructure] = useState<'direct' | 'subfolder'>('direct');
    const [uploadProgress, setUploadProgress] = useState(0);
    const [uploadStatus, setUploadStatus] = useState('');

    // File size check
    const [fileTooLarge, setFileTooLarge] = useState(false);

    // Submit
    const [submitting, setSubmitting] = useState(false);
    const [error, setError] = useState('');

    // Gateway route (optional, for setup wizard)
    const [gatewayRoute, setGatewayRoute] = useState<CreateRouteRequest>({ targetPort: 25565 });
    // Routes already attached to this server. Used by the picker to
    // distinguish "domain is yours" from "domain is taken by someone
    // else" (different colour + can submit unchanged), and to suppress
    // the createServerRoute call on submit when the user picked a
    // domain that's already routed to this same server.
    const [existingRoutes, setExistingRoutes] = useState<GatewayRoute[]>([]);
    const [showRoutesModal, setShowRoutesModal] = useState(false);
    const loadRoutes = useCallback(async () => {
        try {
            const res: any = await getServerRoutes(server.id);
            if (Array.isArray(res)) setExistingRoutes(res);
            else if (res && Array.isArray(res.routes)) setExistingRoutes(res.routes);
            else setExistingRoutes([]);
        } catch { /* non-fatal: tooltip just won't know about own routes */ }
    }, [server.id]);
    useEffect(() => { loadRoutes(); }, [loadRoutes]);

    // Delete sub-server
    const [showDeleteConfirm, setShowDeleteConfirm] = useState(false);
    const [deleteCountdown, setDeleteCountdown] = useState(5);
    const [deleting, setDeleting] = useState(false);
    // Sub-server names whose backend deletion is still in flight. The
    // sidebar greys them out with a spinner so the user sees the row
    // hasn't been forgotten while the Node tears the container down +
    // removes the dir + Hub catches up. Removed once the polling loop
    // confirms the dir is gone server-side.
    const [pendingDelete, setPendingDelete] = useState<Set<string>>(new Set());

    // ---------- Data Loading ----------

    // Load software catalog + feature flags
    useEffect(() => {
        fetch(`${API_URL}/versions/software`)
            .then(r => r.json())
            .then(data => {
                if (data.success && Array.isArray(data.software)) {
                    setSoftwareCatalog(data.software);
                }
            })
            .catch(() => {});

        getServerSettings().then(res => {
            if (res.success && res.settings) setMaxSubServers(res.settings.maxSubServers);
        }).catch(() => {});
    }, []);

    useEffect(() => { loadSubServers(); }, [server.uuid]);

    const loadSubServers = async () => {
        const res = await getFiles('', server.uuid);
        if (res.success && res.files) {
            const dirs = (res.files as any[]).filter(f => f.is_dir).map(f => f.name);
            setSubServers(dirs);
            if (dirs.length === 0) {
                setActiveServerMissing(false);
                enterNewMode();
            } else {
                const activeExists = dirs.includes(server.activeSubServer || '');
                setActiveServerMissing(!activeExists && !!server.activeSubServer);
                // Don't auto-set switchTarget when the active is
                // missing -- with the new design that opens a switch
                // modal unbidden. The warning banner already prompts
                // the user; they click the Play icon to switch.
                enterViewMode();
            }
        }
    };

    useEffect(() => {
        if (installTab === 'library' && libraryEnabled) loadLibraryFiles('');
    }, [installTab, libraryEnabled]);

    const loadLibraryFiles = async (path: string) => {
        const res = await getLibraryFiles(path);
        if (res.success && res.files) { setLibraryFiles(res.files); setLibraryPath(path); }
    };

    useEffect(() => {
        if (installTab !== 'online') return;
        fetchVersions();
    }, [software, installTab]);

    const fetchVersions = async () => {
        setLoadingVersions(true);
        setAllVersions([]);
        setSelectedMajor('');
        setSelectedBuild('');
        try {
            const token = localStorage.getItem('token') || localStorage.getItem('authToken');
            const res = await fetch(`${API_URL}/versions?software=${software}`, {
                headers: token ? { Authorization: `Bearer ${token}` } : {},
            });
            const data = await res.json();
            if (data.versions && data.versions.length > 0) {
                const sorted = [...data.versions].sort(
                    (a, b) => compareVersionsDesc(a.major, b.major) || compareVersionsDesc(a.build, b.build),
                );
                setAllVersions(sorted);
                setSelectedMajor(sorted[0].major);
                setSelectedBuild(sorted[0].build);
                prefillVersionFromServer(sorted);
            }
        } catch {}
        setLoadingVersions(false);
    };

    // ---------- Mode Transitions ----------

    const enterViewMode = () => {
        setFormMode('view');
        setSubName(server.activeSubServer || '');
        setJavaImage(server.image || JAVA_IMAGES[0].id);
        setExtraFlags(server.extraJvmFlags || '');
        setSwitchTarget(null);
    };

    const enterEditMode = () => {
        setFormMode('edit');
        setSubName(server.activeSubServer || '');
        setJavaImage(server.image || JAVA_IMAGES[0].id);
        setExtraFlags(server.extraJvmFlags || '');
        const sType = server.installerType || defaultSoftware;
        if (sType === 'library') {
            setInstallTab('library');
        } else if (sType === 'upload') {
            setInstallTab('upload');
        } else {
            setSoftware(sType);
            setInstallTab('online');
        }
        setError('');
    };

    const enterNewMode = () => {
        setFormMode('new');
        setSubName('');
        setSubNameError('');
        setJavaImage(JAVA_IMAGES[0].id); // Java 21 for both game and proxy
        setExtraFlags(isProxy ? PROXY_GC_FLAGS : DEFAULT_GC_FLAGS);
        setInstallTab('online');
        setSoftware(defaultSoftware);
        setSelectedLibraryFile('');
        setUploadFile(null);
        setUploadProgress(0);
        setUploadStatus('');
        setError('');
    };

    const prefillVersionFromServer = (versions: VersionEntry[]) => {
        if (formMode !== 'edit') return;
        const mcVer = server.minecraftVersion || '';
        const buildNum = server.buildNumber || '';
        if (mcVer && versions.some(v => v.major === mcVer)) {
            setSelectedMajor(mcVer);
            if (buildNum && versions.some(v => v.major === mcVer && v.build === buildNum)) {
                setSelectedBuild(buildNum);
            } else {
                const first = versions.find(v => v.major === mcVer);
                setSelectedBuild(first?.build || '');
            }
        }
    };

    // ---------- Handlers ----------

    const handleSubNameChange = (raw: string) => {
        setSubName(raw);
        const s = sanitizeName(raw);
        setSubNameError(raw && !nameRegex.test(s) ? 'Only letters, numbers, -, _, + allowed.' : '');
    };

    const handleSwitchServer = async () => {
        if (!switchTarget) return;
        setSubmitting(true);
        setError('');
        const res = await switchSubServer(server.id, switchTarget);
        if (res.success) { setSwitchTarget(null); setActiveServerMissing(false); onSetupComplete(); }
        else setError(res.message || 'Switch failed');
        setSubmitting(false);
    };

    const handleSubmit = async () => {
        const sanitized = sanitizeName(subName);
        if (!sanitized) { setSubNameError('Server name is required.'); return; }
        setSubmitting(true);
        setError('');

        const installer: any = {};
        if (installTab === 'online') {
            installer.type = software;
            if (software === 'neoforge') {
                // NeoForge versions are self-contained — the version IS the loader,
                // the matching MC version is implicit.
                installer.loader = selectedBuild;
            } else {
                installer.version = selectedBuild;
                installer.mcVersion = selectedMajor;
            }
        } else if (installTab === 'library') {
            installer.type = 'library';
            installer.path = selectedLibraryFile;
        } else if (installTab === 'upload' && uploadFile) {
            installer.type = 'upload-zip';
            installer.structure = uploadStructure;
            setUploadStatus('Uploading...');
            try {
                const renamedFile = new File([uploadFile], '.upload.zip', { type: uploadFile.type });
                const dt = new DataTransfer();
                dt.items.add(renamedFile);
                const uploadRes = await uploadFiles(sanitized, dt.files, (p) => setUploadProgress(p), undefined, undefined, server.uuid);
                if (!uploadRes.success) {
                    setError(uploadRes.message || 'Upload failed');
                    setSubmitting(false);
                    setUploadStatus('');
                    return;
                }
            } catch {
                setError('Upload failed');
                setSubmitting(false);
                setUploadStatus('');
                return;
            }
            setUploadStatus('Installing...');
        } else {
            installer.type = 'upload';
        }

        const res = await setupServer(server.id, {
            subServerName: sanitized,
            javaImage,
            extraJvmFlags: extraFlags,
            installer,
        });

        if (res.success) {
            // Create gateway route if any domain field was filled. We surface
            // failures inline so a typo'd domain or a race-loss against
            // another tab doesn't silently drop the route -- the server is
            // already installed at this point, but the user needs to know
            // the routing piece didn't go through so they can retry from
            // the Setup tab.
            const hasDomain = !!(gatewayRoute.subdomain || gatewayRoute.customDomain || gatewayRoute.domain);
            // Effective domain for own-route detection. Mirrors the
            // picker's preview computation so the comparison sees the
            // same string that ends up in createServerRoute.
            const effectiveDomain = (gatewayRoute.subdomain && gatewayRoute.hosterDomain)
                ? `${gatewayRoute.subdomain}.${gatewayRoute.hosterDomain}`.toLowerCase()
                : (gatewayRoute.customDomain || gatewayRoute.domain || '').toLowerCase();
            const alreadyOurs = !!effectiveDomain && existingRoutes.some(r => r.domain.toLowerCase() === effectiveDomain);
            let routeError = '';
            if (hasDomain && alreadyOurs) {
                // Domain is already routed to this server; nothing to do
                // on the API side, just clear the field so the wizard
                // resets cleanly for the next round.
                setGatewayRoute({ targetPort: 25565 });
            }
            if (hasDomain && !alreadyOurs) {
                try {
                    const routeRes = await createServerRoute(server.id, gatewayRoute);
                    // fetchAPI now wraps text/plain errors as
                    // {success: false, error, message}. The success response
                    // from CreateServerRoute is {message, domain} with no
                    // explicit success flag, so we treat the absence of
                    // success:false / error as a pass.
                    if (routeRes && (routeRes.success === false || routeRes.error)) {
                        routeError = routeRes.error || routeRes.message || 'Could not create domain route';
                    } else {
                        setGatewayRoute({ targetPort: 25565 });
                    }
                } catch (e: any) {
                    routeError = e?.message || 'Could not create domain route';
                }
            }
            await loadSubServers();
            if (routeError) {
                setError(`Server installed, but domain route failed: ${routeError}`);
            } else {
                onSetupComplete();
            }
        } else setError(res.message || 'Setup failed');
        setSubmitting(false);
        setUploadStatus('');
        setUploadProgress(0);
    };

    // Delete sub-server countdown
    useEffect(() => {
        if (!showDeleteConfirm) { setDeleteCountdown(5); return; }
        if (deleteCountdown <= 0) return;
        const timer = setTimeout(() => setDeleteCountdown(c => c - 1), 1000);
        return () => clearTimeout(timer);
    }, [showDeleteConfirm, deleteCountdown]);

    const handleDeleteSubServer = async () => {
        const target = subName;
        setDeleting(true);
        const res = await deleteSubServer(server.id, target);
        setDeleting(false);
        setShowDeleteConfirm(false);
        if (!res.success) {
            setError(res.message || 'Delete failed');
            return;
        }

        // Optimistic UI: mark the row as pending-delete so the sidebar
        // greys it out + shows a spinner; the row stays visible so the
        // user knows we're working on it. We do NOT call loadSubServers
        // yet -- that would re-fetch the still-present dir from the
        // Node and "un-delete" the row in the UI.
        setPendingDelete(prev => new Set(prev).add(target));

        // Background poll: hit the file listing every 500ms until the
        // target dir is gone. Capped at 30s so a wedged delete (FS
        // refuses to release, Node crashed, etc.) doesn't leave the
        // sidebar in pending-state forever -- we fall back to a normal
        // reload after the timeout. Important: this loop never calls
        // enterNewMode / enterViewMode while formMode === 'new'; the
        // user might be filling the new-server form right now and we
        // mustn't blow away their progress because an unrelated
        // sidebar row finished deleting.
        const startedAt = Date.now();
        const maxWait = 30_000;
        const wasInNewMode = formMode === 'new';
        let confirmed = false;
        while (Date.now() - startedAt < maxWait) {
            await new Promise(r => setTimeout(r, 500));
            try {
                const lst = await getFiles('', server.uuid);
                if (lst.success && Array.isArray(lst.files)) {
                    const dirs = (lst.files as any[]).filter(f => f.is_dir).map(f => f.name);
                    if (!dirs.includes(target)) {
                        setSubServers(dirs);
                        confirmed = true;
                        if (dirs.length === 0) {
                            setActiveServerMissing(false);
                            // Only auto-enter new mode if the user
                            // isn't already in it. They could be
                            // mid-form for a different sub-server
                            // creation and getting wiped to a fresh
                            // new-mode would lose their typing.
                            if (!wasInNewMode) enterNewMode();
                        } else {
                            const activeExists = dirs.includes(server.activeSubServer || '');
                            setActiveServerMissing(!activeExists && !!server.activeSubServer);
                        }
                        break;
                    }
                }
            } catch { /* swallow & keep polling */ }
        }

        setPendingDelete(prev => {
            const next = new Set(prev);
            next.delete(target);
            return next;
        });
        // Fallback if polling timed out without the dir disappearing.
        // The Node may have been slow but eventually catches up; reload
        // so the next view reflects whatever state actually exists.
        if (!confirmed) {
            await loadSubServers();
        }
        await refreshServers();
        // Only signal "setup complete" upstream when we're not in the
        // middle of a new-sub-server flow -- otherwise the parent
        // refresh could yank the user out.
        if (!wasInNewMode) onSetupComplete();
    };

    // ---------- Shared props for install sections ----------

    const installProps = {
        installTab,
        onInstallTabChange: setInstallTab,
        libraryEnabled,
        software,
        onSoftwareChange: setSoftware,
        softwareList: filteredSoftware.length > 0 ? filteredSoftware : undefined,
        serverType: (server.serverType || 'game') as 'game' | 'proxy',
        allVersions,
        selectedMajor,
        onMajorChange: setSelectedMajor,
        selectedBuild,
        onBuildChange: setSelectedBuild,
        loadingVersions,
        libraryFiles,
        libraryPath,
        selectedLibraryFile,
        onLibraryNavigate: loadLibraryFiles,
        onLibrarySelect: setSelectedLibraryFile,
        uploadFile,
        onUploadFileChange: (f: File | null) => { setUploadFile(f); if (!f) { setUploadProgress(0); setUploadStatus(''); } },
        uploadStructure,
        onUploadStructureChange: setUploadStructure,
        uploadProgress,
        uploadStatus,
        onUploadStatusChange: setUploadStatus,
        serverId: server.id,
        onFileTooLarge: setFileTooLarge,
    };

    // ---------- Render ----------

    return (
        <div className="flex gap-6 h-full min-h-0">
            {/* Sidebar -- always rendered, including during
                pending_setup of a fresh container so the user can
                Add Server right away. Empty-state hint inside the
                sidebar orients new servers; once any sub-server
                exists the list takes over. */}
            <SubServerSidebar
                subServers={subServers}
                activeSubServer={server.activeSubServer}
                pendingDelete={pendingDelete}
                onSwitch={(name) => {
                    if (formMode !== 'view') return;
                    setSwitchTarget(name);
                }}
                onAddNew={enterNewMode}
                onEditSubServer={(name) => {
                    // Switch to the right slot first, then jump
                    // straight into edit. enterEditMode reads
                    // subName from server.activeSubServer, so we
                    // need the panel state to match the picked
                    // entry before we transition.
                    setSubName(name);
                    enterEditMode();
                }}
                onDeleteSubServer={(name) => {
                    setSubName(name);
                    setShowDeleteConfirm(true);
                }}
                submitting={submitting}
                disabled={formMode !== 'view'}
                maxSubServers={maxSubServers}
            />

            {/* Right panel - mode dependent */}
            {formMode === 'view' && (
                <SetupViewMode
                    server={server}
                    activeServerMissing={activeServerMissing}
                    onEdit={enterEditMode}
                    onAddNew={enterNewMode}
                    hasSubServers={subServers.length > 0}
                />
            )}

            {formMode === 'new' && (
                <SetupNewWizard
                    subName={subName}
                    onSubNameChange={handleSubNameChange}
                    subNameError={subNameError}
                    javaImage={javaImage}
                    onJavaChange={setJavaImage}
                    extraFlags={extraFlags}
                    onFlagsChange={setExtraFlags}
                    ramMB={server.memory}
                    {...installProps}
                    onSubmit={handleSubmit}
                    onClose={enterViewMode}
                    submitting={submitting}
                    fileTooLarge={fileTooLarge}
                    error={error}
                    hasSubServers={subServers.length > 0}
                    isFirstSetup={subServers.length === 0}
                    gatewayRoute={gatewayRoute}
                    onGatewayRouteChange={setGatewayRoute}
                    existingRoutes={existingRoutes}
                    onOpenRoutesModal={() => setShowRoutesModal(true)}
                />
            )}

            {formMode === 'edit' && (
                <SetupEditMode
                    subName={subName}
                    javaImage={javaImage}
                    onJavaChange={setJavaImage}
                    extraFlags={extraFlags}
                    onFlagsChange={setExtraFlags}
                    ramMB={server.memory}
                    {...installProps}
                    activeServerMissing={activeServerMissing}
                    activeSubServer={server.activeSubServer}
                    onSubmit={handleSubmit}
                    onClose={enterViewMode}
                    onDelete={() => setShowDeleteConfirm(true)}
                    submitting={submitting}
                    fileTooLarge={fileTooLarge}
                    error={error}
                />
            )}

            {/* Switch confirmation modal. Triggered by the Play icon
                on a non-active sub-server. Switching stops the
                currently-running container so this stays an explicit
                opt-in (not silent) -- destructive transition for any
                player connected to the live server. */}
            {switchTarget && switchTarget !== server.activeSubServer && (
                <div className="modal-overlay animate-fade-in" onClick={() => setSwitchTarget(null)}>
                    <div className="modal-panel max-w-md" onClick={e => e.stopPropagation()}>
                        <div className="modal-header">
                            <h3 className="modal-title flex items-center gap-2 text-(--warning-light)">
                                <RefreshCw size={20} /> Switch Sub-Server
                            </h3>
                        </div>
                        <div className="modal-body space-y-3">
                            <p className="text-sm text-(--base-08)">
                                Switch active sub-server to{' '}
                                <span className="font-mono font-semibold text-(--warning-light)">{switchTarget}</span>?
                            </p>
                            <p className="text-sm text-(--base-06)">
                                The currently-running container will be stopped and the new sub-server will be
                                started in its place. Any connected players will be disconnected.
                            </p>
                        </div>
                        <div className="modal-footer">
                            <button onClick={() => setSwitchTarget(null)} className="btn btn-secondary flex-1">
                                Cancel
                            </button>
                            <button
                                onClick={handleSwitchServer}
                                disabled={submitting}
                                className="btn btn-primary flex-1 bg-(--warning) border-(--warning) hover:bg-(--warning-light)"
                            >
                                {submitting
                                    ? <><RefreshCw size={14} className="animate-spin" /> Switching...</>
                                    : <><RefreshCw size={14} /> Switch</>
                                }
                            </button>
                        </div>
                    </div>
                </div>
            )}

            {/* Delete Confirmation Modal */}
            {showDeleteConfirm && (
                <div className="modal-overlay animate-fade-in" onClick={() => setShowDeleteConfirm(false)}>
                    <div className="modal-panel max-w-md" onClick={e => e.stopPropagation()}>
                        <div className="modal-header">
                            <h3 className="modal-title flex items-center gap-2 text-(--error-light)">
                                <AlertTriangle size={20} /> Delete Sub-Server
                            </h3>
                        </div>
                        <div className="modal-body space-y-4">
                            <p className="text-sm text-(--base-08)">
                                Are you sure you want to delete <span className="font-mono font-semibold text-(--error-light)">{subName}</span>?
                            </p>
                            <p className="text-sm text-(--base-06)">
                                All data will be permanently deleted. This action cannot be undone.
                            </p>
                            <div className="text-xs text-(--base-07) bg-(--base-02) border border-(--base-03) rounded-md p-2.5 leading-relaxed">
                                <span className="font-semibold text-(--accent-light)">Note:</span> Domain routes
                                belong to the server, not individual sub-servers, so they stay attached and keep
                                pointing at whichever sub-server you make active next. Manage them under{' '}
                                <span className="font-mono">Network → Routes</span>.
                            </div>
                        </div>
                        <div className="modal-footer">
                            <button onClick={() => setShowDeleteConfirm(false)} className="btn btn-secondary flex-1">
                                Cancel
                            </button>
                            <button
                                onClick={handleDeleteSubServer}
                                disabled={deleteCountdown > 0 || deleting}
                                className="btn btn-danger flex-1"
                            >
                                {deleting
                                    ? <><RefreshCw size={14} className="animate-spin" /> Deleting...</>
                                    : deleteCountdown > 0
                                    ? `Delete (${deleteCountdown}s)`
                                    : <><Trash2 size={14} /> Delete permanently</>
                                }
                            </button>
                        </div>
                    </div>
                </div>
            )}

            {/* Inline routes management. Same component the server-
                header globe icon opens; we mount it here too so the
                setup-tab globe shortcut works without round-tripping
                through the layout. Reload local existingRoutes when
                the user mutates anything so the picker's "own route"
                check stays in sync. */}
            {showRoutesModal && (
                <RoutesModal
                    serverId={server.id}
                    serverName={server.name}
                    onClose={() => { setShowRoutesModal(false); loadRoutes(); }}
                    onRoutesChanged={(rs) => setExistingRoutes(rs)}
                />
            )}
        </div>
    );
}
