"use client";

import { useParams } from 'next/navigation';
import { Terminal, Globe, FolderOpen, Copy } from 'lucide-react';
import { useAppData } from '@/lib/AppDataContext';
import FileBrowserView from '@/views/FileBrowserView';

export default function ServerFilesPage() {
    const params = useParams();
    const { servers, user, fileAccessMode, beamSettings } = useAppData();
    const server = servers.find(s => s.id === Number(params?.id));
    if (!server) return null;

    const showSftp = (fileAccessMode === 'sftp' || fileAccessMode === 'both') && server.nodeAddress;
    const showBeam = (fileAccessMode === 'beam' || fileAccessMode === 'both') && beamSettings?.relayAddress;
    const hasInfoBar = showSftp || showBeam || beamSettings?.downloadLink;

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
                    {fileAccessMode === 'both' && beamSettings?.relayAddress && (
                        <div className="w-px h-8 bg-(--base-03) hidden sm:block" />
                    )}
                    {showBeam && (
                        <div className="flex items-center gap-3 min-w-0">
                            <Globe size={14} className="text-(--accent-light) shrink-0" />
                            <div className="min-w-0">
                                <div className="input-label mb-0.5">Beam Relay</div>
                                <div className="font-mono text-xs text-(--base-08)">{beamSettings!.relayAddress}</div>
                            </div>
                        </div>
                    )}
                    {beamSettings?.downloadLink && (
                        <a
                            href={beamSettings.downloadLink}
                            target="_blank"
                            rel="noopener noreferrer"
                            className="btn btn-secondary text-xs px-3 py-1.5 ml-auto shrink-0"
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
