"use client";

import React, { useMemo } from 'react';
import { X, Rocket, RefreshCw, Globe } from 'lucide-react';
import { CreateRouteRequest } from '@/lib/api';
import { useAppData } from '@/lib/AppDataContext';
import JavaVersionPicker, { recommendJavaForVersion } from './JavaVersionPicker';
import JvmFlagsSection from './JvmFlagsSection';
import VersionPicker, { VersionEntry } from './VersionPicker';
import LibraryPicker from './LibraryPicker';
import UploadSection from './UploadSection';
import RouteDomainPicker from '@/components/RouteDomainPicker';

const nameRegex = /^[a-zA-Z0-9\-_+]+$/;
function sanitizeName(raw: string): string {
    return raw.replace(/ /g, '_').replace(/[^a-zA-Z0-9\-_+]/g, '');
}

interface SetupNewWizardProps {
    // Name
    subName: string;
    onSubNameChange: (name: string) => void;
    subNameError: string;
    // Java
    javaImage: string;
    onJavaChange: (id: string) => void;
    // Flags
    extraFlags: string;
    onFlagsChange: (flags: string) => void;
    ramMB: number;
    // Install tab
    installTab: 'online' | 'library' | 'upload';
    onInstallTabChange: (tab: 'online' | 'library' | 'upload') => void;
    libraryEnabled?: boolean;
    // Server type
    serverType?: 'game' | 'proxy';
    // Version picker
    software: string;
    onSoftwareChange: (s: string) => void;
    softwareList?: string[];
    allVersions: VersionEntry[];
    selectedMajor: string;
    onMajorChange: (m: string) => void;
    selectedBuild: string;
    onBuildChange: (b: string) => void;
    loadingVersions: boolean;
    // Library
    libraryFiles: any[];
    libraryPath: string;
    selectedLibraryFile: string;
    onLibraryNavigate: (path: string) => void;
    onLibrarySelect: (file: string) => void;
    // Upload
    uploadFile: File | null;
    onUploadFileChange: (file: File | null) => void;
    uploadStructure: 'direct' | 'subfolder';
    onUploadStructureChange: (s: 'direct' | 'subfolder') => void;
    uploadProgress: number;
    uploadStatus: string;
    onUploadStatusChange: (s: string) => void;
    serverId: number;
    onFileTooLarge?: (tooLarge: boolean) => void;
    // Gateway route (optional)
    gatewayRoute?: CreateRouteRequest;
    onGatewayRouteChange?: (next: CreateRouteRequest) => void;
    // Actions
    onSubmit: () => void;
    onClose: () => void;
    submitting: boolean;
    fileTooLarge?: boolean;
    error: string;
    hasSubServers: boolean;
    isFirstSetup: boolean;
}

export default function SetupNewWizard(props: SetupNewWizardProps) {
    const sanitized = sanitizeName(props.subName);
    const recommendedJava = useMemo(() => recommendJavaForVersion(props.selectedMajor), [props.selectedMajor]);

    // Domain row only matters when the gateway actually handles traffic.
    // In ip_port mode players connect via Node IP + port, so showing a
    // domain input here is confusing — hide it.
    const { routingMode } = useAppData();
    const showDomainField = routingMode !== 'ip_port';

    return (
        <div className="flex-1 card flex flex-col overflow-hidden min-w-0">
            {/* Header */}
            <div className="modal-header flex items-center justify-between shrink-0">
                <h3 className="modal-title">
                    {props.isFirstSetup ? 'Server Setup' : 'Add New Server'}
                </h3>
                {props.hasSubServers && (
                    <button type="button" onClick={props.onClose} className="text-(--base-07) hover:text-(--error-light) transition-colors">
                        <X size={20} />
                    </button>
                )}
            </div>

            {/* Body */}
            <div className="flex-1 overflow-y-auto p-6 space-y-5 min-h-0">
                {/* Row 1: Name */}
                <div className="flex flex-col gap-[5px]">
                    <label className="input-label">Server Slot Name</label>
                    <input
                        type="text"
                        value={props.subName}
                        onChange={e => props.onSubNameChange(e.target.value)}
                        placeholder="e.g. Survival"
                        className={`input-mono w-full md:w-1/2 ${props.subNameError ? 'input-field-error' : ''}`}
                        autoFocus
                    />
                    {props.subNameError && <p className="text-xs text-(--error-light)">{props.subNameError}</p>}
                    {props.subName && !props.subNameError && (
                        <p className="text-xs text-(--base-07) font-mono">
                            Stored as: <span className="text-(--primary-light)">/data/{sanitized}/</span>
                        </p>
                    )}
                </div>

                {/* Row 2: Optional gateway route (only when gateway routes traffic) */}
                {showDomainField && (
                    <div className="flex flex-col gap-[5px]">
                        <label className="input-label flex items-center gap-1.5">
                            <Globe size={12} className="text-(--accent-light)" /> Domain Route
                            <span className="text-[9px] font-mono uppercase tracking-[0.08em] text-(--base-06) bg-(--base-03) px-1 py-0.5 rounded-sm ml-1">Optional</span>
                        </label>
                        <RouteDomainPicker
                            value={props.gatewayRoute || { targetPort: 25565 }}
                            onChange={next => props.onGatewayRouteChange?.(next)}
                            portChildren={
                                <select
                                    value={props.gatewayRoute?.targetPort || 25565}
                                    onChange={e => props.onGatewayRouteChange?.({ ...(props.gatewayRoute || {}), targetPort: Number(e.target.value) })}
                                    className="input-field text-sm w-28"
                                >
                                    <option value={25565}>MC (25565)</option>
                                    <option value={80}>HTTP (80)</option>
                                    <option value={443}>HTTPS (443)</option>
                                </select>
                            }
                        />
                        <p className="text-xs text-(--base-06)">Can also be configured later in the Setup tab.</p>
                    </div>
                )}

                {/* Row 2: Java Version (full width) */}
                <JavaVersionPicker
                    value={props.javaImage}
                    onChange={props.onJavaChange}
                    serverType={props.serverType}
                    recommended={recommendedJava ?? undefined}
                />

                {/* Install tabs */}
                <div>
                    <label className="input-label mb-2 block">Installation Method</label>
                    <div className="flex bg-(--base-03) p-1 rounded-md max-w-md">
                        <button type="button" onClick={() => props.onInstallTabChange('online')}
                            className={`btn flex-1 py-2 text-sm border-0 rounded-md ${props.installTab === 'online' ? 'bg-(--accent) text-white' : 'bg-transparent text-(--base-07) hover:text-(--base-09)'}`}>
                            Online
                        </button>
                        {props.libraryEnabled && (
                            <button type="button" onClick={() => props.onInstallTabChange('library')}
                                className={`btn flex-1 py-2 text-sm border-0 rounded-md ${props.installTab === 'library' ? 'bg-(--accent) text-white' : 'bg-transparent text-(--base-07) hover:text-(--base-09)'}`}>
                                Library
                            </button>
                        )}
                        <button type="button" onClick={() => props.onInstallTabChange('upload')}
                            className={`btn flex-1 py-2 text-sm border-0 rounded-md ${props.installTab === 'upload' ? 'bg-(--accent) text-white' : 'bg-transparent text-(--base-07) hover:text-(--base-09)'}`}>
                            Upload / SFTP
                        </button>
                    </div>
                </div>

                {/* Tab content */}
                {props.installTab === 'online' && (
                    <div className="min-h-[220px]">
                        <VersionPicker
                            software={props.software}
                            onSoftwareChange={props.onSoftwareChange}
                            softwareList={props.softwareList}
                            allVersions={props.allVersions}
                            selectedMajor={props.selectedMajor}
                            onMajorChange={props.onMajorChange}
                            selectedBuild={props.selectedBuild}
                            onBuildChange={props.onBuildChange}
                            loading={props.loadingVersions}
                        />
                    </div>
                )}

                {props.installTab === 'library' && props.libraryEnabled && (
                    <LibraryPicker
                        files={props.libraryFiles}
                        path={props.libraryPath}
                        selectedFile={props.selectedLibraryFile}
                        onNavigate={props.onLibraryNavigate}
                        onSelect={props.onLibrarySelect}
                    />
                )}

                {props.installTab === 'upload' && (
                    <UploadSection
                        uploadFile={props.uploadFile}
                        onFileChange={props.onUploadFileChange}
                        uploadStructure={props.uploadStructure}
                        onStructureChange={props.onUploadStructureChange}
                        uploadProgress={props.uploadProgress}
                        uploadStatus={props.uploadStatus}
                        onStatusChange={props.onUploadStatusChange}
                        serverId={props.serverId}
                        onFileTooLarge={props.onFileTooLarge}
                    />
                )}

                {/* JVM Flags (collapsed by default) */}
                <JvmFlagsSection
                    extraFlags={props.extraFlags}
                    onChange={props.onFlagsChange}
                    ramMB={props.ramMB}
                />

                {props.error && <p className="text-(--error-light) text-sm font-medium">{props.error}</p>}
            </div>

            {/* Footer */}
            <div className="modal-footer flex gap-2">
                <button
                    type="button"
                    onClick={props.onSubmit}
                    disabled={props.submitting || !sanitized || props.fileTooLarge}
                    className="btn btn-primary btn-lg flex-1"
                >
                    {props.submitting
                        ? <><RefreshCw size={16} className="animate-spin" /> Working...</>
                        : <><Rocket size={16} /> Install & Start Server</>
                    }
                </button>
            </div>
        </div>
    );
}
