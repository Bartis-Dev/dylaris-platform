"use client";

import { useParams } from 'next/navigation';
import { Terminal, Globe, FolderOpen, Copy } from 'lucide-react';
import { useAppData } from '@/lib/AppDataContext';
import FileBrowserView from '@/views/FileBrowserView';

// Platform slugs match gateway/beam/relay/binaries.go validPlatforms.
type BeamPlatform = 'windows-amd64' | 'linux-amd64' | 'linux-arm64' | 'darwin-amd64' | 'darwin-arm64';

function detectBeamPlatform(): BeamPlatform {
    if (typeof navigator === 'undefined') return 'windows-amd64';
    const ua = navigator.userAgent;
    const platform = (navigator as Navigator & { userAgentData?: { platform?: string; architecture?: string } }).userAgentData?.platform ?? '';
    const arch = (navigator as Navigator & { userAgentData?: { platform?: string; architecture?: string } }).userAgentData?.architecture ?? '';
    const isArm = /aarch64|arm64|arm/i.test(ua + ' ' + arch);

    if (/Mac|Darwin/i.test(ua) || /macOS/i.test(platform)) {
        return isArm ? 'darwin-arm64' : 'darwin-amd64';
    }
    if (/Linux/i.test(ua)) {
        return isArm ? 'linux-arm64' : 'linux-amd64';
    }
    return 'windows-amd64';
}

export default function ServerFilesPage() {
    const params = useParams();
    const { servers, user, fileAccessMode, beamSettings } = useAppData();
    const server = servers.find(s => s.id === Number(params?.id));
    if (!server) return null;

    const API_URL = process.env.NEXT_PUBLIC_API_URL || 'http://localhost:25500/api';
    const beamDownloadUrl = `${API_URL}/beam/download?platform=${detectBeamPlatform()}`;
    const beamEnabled = (fileAccessMode === 'beam' || fileAccessMode === 'both') && beamSettings?.enabled !== false;

    const showSftp = (fileAccessMode === 'sftp' || fileAccessMode === 'both') && server.nodeAddress;
    const hasInfoBar = showSftp || beamEnabled;

    return (
        <div className="flex flex-col gap-3 h-full">
            {hasInfoBar && (
                <div className="card px-4 py-3 flex flex-wrap items-center gap-4 shrink-0">
                    {showSftp && (
                        <div className="flex items-center gap-3 min-w-0">
                            <Terminal size={14} className="text-(--base-06) shrink-0" />
                            <div className="min-w-0">
                                <div className="input-label mb-0.5">SFTP</div>
                                <div className="flex items-center gap-2 font-mono text-xs text-(--base-08)">
                                    <span>{server.nodeAddress}</span>
                                    <span className="text-(--base-05)">:</span>
                                    <span>2222</span>
                                    <span className="text-(--base-05)">·</span>
                                    <span>{user?.username}</span>
                                </div>
                            </div>
                            <button
                                onClick={() => navigator.clipboard.writeText(`sftp ${user?.username}@${server.nodeAddress} -p 2222`)}
                                className="text-(--base-05) hover:text-(--base-09) transition-colors shrink-0 p-1 rounded"
                                title="Copy SFTP command"
                            >
                                <Copy size={13} />
                            </button>
                        </div>
                    )}
                    {fileAccessMode === 'both' && beamEnabled && (
                        <div className="w-px h-8 bg-(--base-03) hidden sm:block" />
                    )}
                    {beamEnabled && (
                        <div className="flex items-center gap-3 min-w-0">
                            <Globe size={14} className="text-(--accent-light) shrink-0" />
                            <div className="min-w-0">
                                <div className="input-label mb-0.5">Beam Desktop</div>
                                <div className="text-xs text-(--base-08)">High-speed transfers via the Beam app</div>
                            </div>
                        </div>
                    )}
                    {beamEnabled && (
                        <a
                            href={beamDownloadUrl}
                            download
                            className="btn btn-secondary btn-sm ml-auto shrink-0"
                            title="Download the Beam Desktop app — connects directly to the relay so transfers don't hit Core"
                        >
                            <FolderOpen size={12} />
                            Download Beam
                        </a>
                    )}
                </div>
            )}
            <FileBrowserView serverUuid={server.uuid} currentServerPath={server.activeSubServer || ''} />
        </div>
    );
}
