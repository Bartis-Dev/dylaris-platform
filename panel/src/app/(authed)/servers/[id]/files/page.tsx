"use client";

import { useState } from 'react';

import { Terminal, Globe, FolderOpen, Copy, Loader2 } from 'lucide-react';
import { useAppData } from '@/lib/AppDataContext';
import { API_URL } from '@/lib/api';
import FileBrowserView from '@/views/FileBrowserView';
import { toast } from '@/components/ui/Toast';
import { useRouteId } from '@/lib/routeParams';

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

// Pulls the filename out of a Content-Disposition header, with a safe
// fallback when the relay didn't set one. Windows binaries get .exe so the
// OS knows what to do with the saved blob.
function filenameFor(platform: BeamPlatform, contentDisp: string): string {
    const m = /filename\*?=(?:UTF-8'')?"?([^";]+)"?/i.exec(contentDisp);
    if (m && m[1]) return decodeURIComponent(m[1]);
    return platform.startsWith('windows') ? `beam-${platform}.exe` : `beam-${platform}`;
}

export default function ServerFilesPage() {
    const paramId = useRouteId('servers');
    const { servers, user, fileAccessMode, beamSettings } = useAppData();
    const server = servers.find(s => s.id === Number(paramId));

    // Beam-download UI state: lives at page scope so the toast/spinner stay
    // mounted even if the file browser below re-renders.
    const [downloading, setDownloading] = useState(false);

    if (!server) return null;

    const beamEnabled = (fileAccessMode === 'beam' || fileAccessMode === 'both') && beamSettings?.enabled !== false;

    const showSftp = (fileAccessMode === 'sftp' || fileAccessMode === 'both') && server.nodeAddress;
    const hasInfoBar = showSftp || beamEnabled;

    // Errors stay up longer than successes: the failures here are the long
    // relay/platform explanations, and 3.5s is not enough to read one.
    const showToast = (msg: string, ok: boolean) => toast(msg, ok, { durationMs: ok ? 3500 : 6000 });

    // Fetch the binary through Core (which proxies to the relay) instead of
    // opening the URL in a new tab. The old <a href> approach made an error
    // response — JSON {success:false, message:...} — render as a raw JSON
    // page in a fresh tab, which is awful. With fetch we can parse the
    // structured error and surface it as a toast.
    const handleBeamDownload = async () => {
        if (downloading) return;
        setDownloading(true);
        try {
            const platform = detectBeamPlatform();
            // No headers and no credentials:'include'. This is same-origin, so
            // the cookie goes by default; asking for credentials explicitly
            // would trigger the stricter CORS path which Core deliberately does
            // not allow, and the browser then aborts with a bare "NetworkError"
            // that hides the real one.
            const res = await fetch(`${API_URL}/beam/download?platform=${platform}`);
            if (!res.ok) {
                let msg = `Download failed (HTTP ${res.status}).`;
                try {
                    const body = await res.json();
                    if (body?.message) msg = body.message;
                } catch {
                    // Response wasn't JSON — keep the generic HTTP message.
                }
                showToast(msg, false);
                return;
            }
            const blob = await res.blob();
            const filename = filenameFor(platform, res.headers.get('Content-Disposition') || '');
            const url = URL.createObjectURL(blob);
            const a = document.createElement('a');
            a.href = url;
            a.download = filename;
            document.body.appendChild(a);
            a.click();
            a.remove();
            URL.revokeObjectURL(url);
            showToast('Beam app downloaded.', true);
        } catch (e) {
            showToast(e instanceof Error ? e.message : 'Network error.', false);
        } finally {
            setDownloading(false);
        }
    };

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
                                    <span>25520</span>
                                    <span className="text-(--base-05)">·</span>
                                    <span>{user?.username}</span>
                                </div>
                            </div>
                            <button
                                onClick={() => navigator.clipboard.writeText(`sftp ${user?.username}@${server.nodeAddress} -p 25520`)}
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
                        <button
                            type="button"
                            onClick={handleBeamDownload}
                            disabled={downloading}
                            className="btn btn-secondary btn-sm ml-auto shrink-0 disabled:opacity-50"
                            title="Download the Beam Desktop app — connects directly to the relay so transfers don't hit Core"
                        >
                            {downloading
                                ? <Loader2 size={12} className="animate-spin" />
                                : <FolderOpen size={12} />}
                            {downloading ? 'Downloading…' : 'Download Beam'}
                        </button>
                    )}
                </div>
            )}
            <FileBrowserView serverUuid={server.uuid} currentServerPath={server.activeSubServer || ''} readOnly={server.role === 'demo'} />

        </div>
    );
}
