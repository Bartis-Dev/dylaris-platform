"use client";

import React, { useState, useEffect, useMemo } from 'react';
import { Server, setupServer, switchSubServer, getFiles, getLibraryFiles, deleteSubServer, createServerRoute, getServerSettings } from '@/lib/api';
import { uploadFiles } from '@/lib/api/files';
import { AlertTriangle, Trash2, RefreshCw } from 'lucide-react';
import { JAVA_IMAGES, recommendJavaForVersion } from './setup/JavaVersionPicker';
import { VersionEntry } from './setup/VersionPicker';
import SubServerSidebar from './setup/SubServerSidebar';
import SetupViewMode from './setup/SetupViewMode';
import SetupNewWizard from './setup/SetupNewWizard';
import SetupEditMode from './setup/SetupEditMode';
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

    // Auto-select Java version based on selected major version (e.g. "1.20")
    useEffect(() => {
        if (!selectedMajor || formMode === 'view') return;
        const rec = recommendJavaForVersion(selectedMajor);
        if (rec) setJavaImage(rec);
    }, [selectedMajor, formMode]);

    // Auto-select Java version based on selected build (e.g. "1.20.6" for vanilla)
    useEffect(() => {
        if (!selectedBuild || formMode === 'view') return;
        const rec = recommendJavaForVersion(selectedBuild);
        if (rec) setJavaImage(rec);
    }, [selectedBuild, formMode]);

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

    // Gateway domain (optional, for setup wizard)
    const [gatewayDomain, setGatewayDomain] = useState('');
    const [gatewayPort, setGatewayPort] = useState(25565);

    // Delete sub-server
    const [showDeleteConfirm, setShowDeleteConfirm] = useState(false);
    const [deleteCountdown, setDeleteCountdown] = useState(5);
    const [deleting, setDeleting] = useState(false);

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
                if (!activeExists && dirs.length > 0) setSwitchTarget(dirs[0]);
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
                setAllVersions(data.versions);
                setSelectedMajor(data.versions[0].major);
                setSelectedBuild(data.versions[0].build);
                prefillVersionFromServer(data.versions);
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
            installer.version = selectedBuild;
            installer.mcVersion = selectedMajor;
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
            // Create gateway route if domain was specified
            if (gatewayDomain.trim()) {
                try {
                    await createServerRoute(server.id, { domain: gatewayDomain.trim().toLowerCase(), targetPort: gatewayPort });
                } catch { /* non-fatal */ }
                setGatewayDomain('');
                setGatewayPort(25565);
            }
            await loadSubServers();
            onSetupComplete();
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
        setDeleting(true);
        const res = await deleteSubServer(server.id, subName);
        setDeleting(false);
        setShowDeleteConfirm(false);
        if (res.success) {
            await loadSubServers();
            onSetupComplete();
        } else {
            setError(res.message || 'Delete failed');
        }
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
            {/* Sidebar */}
            {subServers.length > 0 && (
                <SubServerSidebar
                    subServers={subServers}
                    activeSubServer={server.activeSubServer}
                    switchTarget={switchTarget}
                    onSwitchSelect={s => formMode === 'view' ? setSwitchTarget(s) : undefined}
                    onSwitchConfirm={handleSwitchServer}
                    onAddNew={enterNewMode}
                    submitting={submitting}
                    disabled={formMode !== 'view'}
                    maxSubServers={maxSubServers}
                />
            )}

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
                    gatewayDomain={gatewayDomain}
                    onGatewayDomainChange={setGatewayDomain}
                    gatewayPort={gatewayPort}
                    onGatewayPortChange={setGatewayPort}
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

            {/* Delete Confirmation Modal */}
            {showDeleteConfirm && (
                <div className="modal-overlay" onClick={() => setShowDeleteConfirm(false)}>
                    <div className="modal-panel max-w-md" onClick={e => e.stopPropagation()}>
                        <div className="modal-header">
                            <h3 className="modal-title flex items-center gap-2 text-(--error-light)">
                                <AlertTriangle size={20} /> Delete Sub-Server
                            </h3>
                        </div>
                        <div className="p-6 space-y-4">
                            <p className="text-sm text-(--base-08)">
                                Are you sure you want to delete <span className="font-mono font-semibold text-(--error-light)">{subName}</span>?
                            </p>
                            <p className="text-sm text-(--base-06)">
                                All data will be permanently deleted. This action cannot be undone.
                            </p>
                        </div>
                        <div className="modal-footer flex gap-2">
                            <button onClick={() => setShowDeleteConfirm(false)} className="btn btn-secondary flex-1 py-2.5 text-sm">
                                Cancel
                            </button>
                            <button
                                onClick={handleDeleteSubServer}
                                disabled={deleteCountdown > 0 || deleting}
                                className="btn btn-danger flex-1 py-2.5 text-sm"
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
        </div>
    );
}
