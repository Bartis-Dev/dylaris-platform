"use client";

import React, { useState, useEffect } from 'react';
import { getNodes, updateNode, Node } from '@/lib/api';
import { Network, Server, Globe, Cpu, Check, Info, HardDrive, ChevronDown } from 'lucide-react';

interface StorageInfo {
    path: string;
    total_bytes: number;
    free_bytes: number;
    used_bytes: number;
    server_count: number;
}

async function getNodeStorage(nodeId: number): Promise<{ success: boolean; storage: StorageInfo[] }> {
    try {
        const token = localStorage.getItem('authToken') || localStorage.getItem('token');
        const API_URL = process.env.NEXT_PUBLIC_API_URL || 'http://localhost:25500/api';
        const res = await fetch(`${API_URL}/nodes/${nodeId}/storage`, {
            headers: { Authorization: `Bearer ${token}` },
        });
        return await res.json();
    } catch {
        return { success: false, storage: [] };
    }
}

function formatStorageSize(bytes: number): string {
    if (bytes === 0) return '0 B';
    const gb = bytes / (1024 * 1024 * 1024);
    if (gb >= 1) return `${gb.toFixed(1)} GB`;
    const mb = bytes / (1024 * 1024);
    return `${mb.toFixed(0)} MB`;
}

export default function NodesTab() {
    const [nodes, setNodes] = useState<Node[]>([]);
    const [editingCpuset, setEditingCpuset] = useState<Record<number, string>>({});
    const [savingCpuset, setSavingCpuset] = useState<number | null>(null);
    const [expandedStorage, setExpandedStorage] = useState<Set<number>>(new Set());
    const [storageData, setStorageData] = useState<Record<number, StorageInfo[]>>({});

    useEffect(() => {
        loadNodes();
        const interval = setInterval(loadNodes, 5000);
        return () => clearInterval(interval);
    }, []);

    const loadNodes = async () => {
        const res = await getNodes();
        if (res.success) setNodes(res.nodes);
    };

    const handleCpusetSave = async (nodeId: number) => {
        const value = editingCpuset[nodeId];
        if (value === undefined) return;
        setSavingCpuset(nodeId);
        await updateNode(nodeId, { cpusetCpus: value });
        setEditingCpuset(prev => { const next = { ...prev }; delete next[nodeId]; return next; });
        setSavingCpuset(null);
        loadNodes();
    };

    const getCpusetValue = (node: Node) => {
        return editingCpuset[node.id] !== undefined ? editingCpuset[node.id] : (node.cpusetCpus || '');
    };

    return (
        <div>
            <div className="card border-(--accent-border) p-6 mb-8">
                <h3 className="text-base font-display font-bold text-(--accent-light) mb-2 flex items-center gap-2">
                    <Network size={18} /> Auto-Discovery Active
                </h3>
                <p className="text-sm text-(--base-07) mb-4">
                    New nodes automatically connect to the Core when started with the correct Cluster Secret.
                </p>

                <div className="bg-(--base-01) p-4 rounded-md border border-(--base-04) font-mono text-sm mb-4">
                    <div className="input-label mb-1">Your Cluster Secret</div>
                    <div className="flex justify-between items-center text-(--success-light)">
                        <span className="select-all">dylaris-cluster-secret</span>
                        <span className="text-[10px] text-(--base-06)">(See Core .env)</span>
                    </div>
                </div>

                <div className="text-sm text-(--base-07)">
                    Start Node:<br/>
                    <code className="bg-(--base-03) px-2 py-1 rounded-sm text-(--base-08) font-mono text-xs mt-2 inline-block">
                        ./dylaris-node -secret &quot;dylaris-cluster-secret&quot;
                    </code>
                </div>
            </div>

            <h3 className="text-base font-display font-bold text-(--base-09) mb-4">Connected Nodes</h3>
            <div className="space-y-3">
                {nodes.length === 0 ? (
                    <div className="text-center p-8 border border-dashed border-(--base-04) rounded-lg text-(--base-06) text-sm">
                        No nodes connected. Start a node!
                    </div>
                ) : (
                    nodes.map(node => (
                        <div key={node.id} className="card p-3 flex flex-col gap-3 transition-colors hover:border-(--base-05)">
                            <div className="flex flex-col md:flex-row md:items-center justify-between gap-4">
                                {/* Left side: Status, Logo, ID & IP */}
                                <div className="flex items-center gap-4">
                                    <div className={`status-dot shrink-0 ${node.status === 'online' ? 'bg-(--success-light) shadow-[0_0_8px_var(--success-light)]' : 'bg-(--error-light)'}`} title={node.status}></div>
                                    <div className="w-10 h-10 bg-(--accent-ghost) text-(--accent-light) rounded-md flex items-center justify-center border border-(--accent-border) shrink-0">
                                        <Server size={24} />
                                    </div>
                                    <div className="flex flex-col md:flex-row md:items-center gap-2 md:gap-3">
                                        <div className="font-medium text-sm text-(--base-09) whitespace-nowrap">
                                            {node.token || <span className="text-(--error-light) text-xs italic">API hidden (Fix types.go)</span>}
                                        </div>
                                        <div className="h-4 w-px bg-(--base-04) hidden md:block"></div>
                                        <div className="text-xs font-mono text-(--base-06) flex items-center bg-(--base-01) px-2 py-1 rounded-sm border border-(--base-04) w-fit whitespace-nowrap">
                                            <Globe size={14} className="mr-1.5 opacity-70" />
                                            {node.address}
                                        </div>
                                    </div>
                                </div>

                                {/* Right side: Tags */}
                                {node.tags && node.tags !== 'auto-discovered' && (
                                    <div className="flex flex-wrap gap-1.5 md:justify-end">
                                        {node.tags.split(',').map(tag => {
                                            const trimmedTag = tag.trim();
                                            if (!trimmedTag) return null;
                                            return (
                                                <span key={trimmedTag} className="badge badge-accent">
                                                    {trimmedTag}
                                                </span>
                                            );
                                        })}
                                    </div>
                                )}
                            </div>

                            {/* Storage Info */}
                            <div className="pl-[72px]">
                                <button
                                    onClick={async () => {
                                        const newSet = new Set(expandedStorage);
                                        if (newSet.has(node.id)) {
                                            newSet.delete(node.id);
                                        } else {
                                            newSet.add(node.id);
                                            if (!storageData[node.id]) {
                                                const res = await getNodeStorage(node.id);
                                                if (res.success) setStorageData(prev => ({ ...prev, [node.id]: res.storage }));
                                            }
                                        }
                                        setExpandedStorage(newSet);
                                    }}
                                    className="flex items-center gap-1.5 text-xs text-(--base-06) hover:text-(--base-08) transition-colors"
                                >
                                    <HardDrive size={13} />
                                    <span className="input-label mb-0!">Storage</span>
                                    <ChevronDown size={11} className={`transition-transform ${expandedStorage.has(node.id) ? 'rotate-180' : ''}`} />
                                </button>

                                {expandedStorage.has(node.id) && (
                                    <div className="mt-2 space-y-2">
                                        {(!storageData[node.id] || storageData[node.id].length === 0) ? (
                                            <p className="text-xs text-(--base-05) italic">No storage info available (node offline or legacy version)</p>
                                        ) : (
                                            storageData[node.id].map((s, i) => {
                                                const pct = s.total_bytes > 0 ? (s.used_bytes / s.total_bytes) * 100 : 0;
                                                const barColor = pct > 90 ? 'bg-(--error-light)' : pct > 70 ? 'bg-(--warning)' : 'bg-(--accent)';
                                                return (
                                                    <div key={i} className="bg-(--base-01) rounded-sm p-2.5 border border-(--base-04)">
                                                        <div className="flex justify-between items-center mb-1.5">
                                                            <span className="font-mono text-[11px] text-(--base-07)">{s.path}</span>
                                                            <span className="text-[10px] text-(--base-06)">{s.server_count} server{s.server_count !== 1 ? 's' : ''}</span>
                                                        </div>
                                                        <div className="w-full h-1.5 bg-(--base-03) rounded-full overflow-hidden">
                                                            <div className={`h-full rounded-full transition-all ${barColor}`} style={{ width: `${Math.min(pct, 100)}%` }} />
                                                        </div>
                                                        <div className="flex justify-between mt-1">
                                                            <span className="text-[10px] text-(--base-06)">{formatStorageSize(s.used_bytes)} / {formatStorageSize(s.total_bytes)}</span>
                                                            <span className="text-[10px] text-(--base-06)">{formatStorageSize(s.free_bytes)} free</span>
                                                        </div>
                                                    </div>
                                                );
                                            })
                                        )}
                                    </div>
                                )}
                            </div>

                            {/* CPU Pinning */}
                            <div className="flex items-center gap-2 pl-[72px]">
                                <div className="relative group/cpuset flex items-center gap-1.5">
                                    <Cpu size={13} className="text-(--base-06)" />
                                    <span className="input-label mb-0!">CPU Pinning</span>
                                    <Info size={11} className="text-(--base-05) cursor-help" />
                                    <div className="absolute bottom-full left-0 mb-1.5 hidden group-hover/cpuset:block z-10 w-64 p-3 rounded-md bg-(--base-02) border border-(--base-04) shadow-lg">
                                        <p className="text-xs text-(--base-09) font-medium mb-1">CPU Core Pinning (cpuset)</p>
                                        <p className="text-xs text-(--base-07)">
                                            Pin MC containers to specific CPU cores. For AMD 3D V-Cache CPUs, pin to the V-Cache CCD (e.g. 0-7).
                                        </p>
                                        <p className="text-xs text-(--base-06) mt-1.5">Format: 0-7, 0,2,4,6, or empty for no pinning</p>
                                    </div>
                                </div>
                                <input
                                    type="text"
                                    value={getCpusetValue(node)}
                                    onChange={e => setEditingCpuset(prev => ({ ...prev, [node.id]: e.target.value }))}
                                    placeholder="e.g. 0-7"
                                    className="input-mono w-28 py-0.5! px-2! text-xs"
                                />
                                {editingCpuset[node.id] !== undefined && editingCpuset[node.id] !== (node.cpusetCpus || '') && (
                                    <button
                                        onClick={() => handleCpusetSave(node.id)}
                                        disabled={savingCpuset === node.id}
                                        className="flex items-center gap-1 px-2 py-0.5 rounded-sm bg-(--accent-ghost) border border-(--accent-border) text-(--accent-light) text-xs hover:bg-(--accent-border) transition-colors disabled:opacity-50"
                                    >
                                        <Check size={11} />
                                        Save
                                    </button>
                                )}
                            </div>
                        </div>
                    ))
                )}
            </div>
        </div>
    );
}
