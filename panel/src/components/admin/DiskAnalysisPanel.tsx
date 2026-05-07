"use client";

import React, { useState, useEffect } from 'react';
import {
    Trash2, AlertTriangle, ChevronDown, ChevronRight, Loader2, Check,
} from 'lucide-react';
import {
    getAdminDiskAnalysis, deleteOrphanedFolder, deleteServer,
    DiskAnalysis, Node,
} from '@/lib/api';

interface DiskAnalysisPanelProps {
    node: Node;
    onOrphanDeleted: () => void;
    autoLoad?: boolean;
    onAutoLoadConsumed?: () => void;
}

export function DiskAnalysisPanel({
    node, onOrphanDeleted, autoLoad, onAutoLoadConsumed,
}: DiskAnalysisPanelProps) {
    const [data, setData] = useState<DiskAnalysis | null>(null);
    const [loading, setLoading] = useState(false);
    const [deleting, setDeleting] = useState<string | null>(null);
    const [deletingDbId, setDeletingDbId] = useState<number | null>(null);
    const [expanded, setExpanded] = useState(false);

    const load = async () => {
        setLoading(true);
        const res = await getAdminDiskAnalysis(node.id);
        setLoading(false);
        if (res.success) {
            setData({
                nodeOnline: res.nodeOnline,
                matched: res.matched ?? [],
                orphaned: res.orphaned ?? [],
                missing: res.missing ?? [],
            });
            setExpanded(true);
        }
    };

    useEffect(() => {
        if (autoLoad && !data && !loading) {
            load();
            onAutoLoadConsumed?.();
        }
    }, [autoLoad]); // eslint-disable-line react-hooks/exhaustive-deps

    const handleDelete = async (orphanUUID: string) => {
        if (!confirm(`Delete orphaned folder ${orphanUUID} from node "${node.name}"? This cannot be undone.`)) return;
        setDeleting(orphanUUID);
        const res = await deleteOrphanedFolder(node.id, orphanUUID);
        setDeleting(null);
        if (res.success) {
            setData(prev => prev ? { ...prev, orphaned: prev.orphaned.filter(o => o.uuid !== orphanUUID) } : prev);
            onOrphanDeleted();
        } else {
            alert(res.message ?? 'Delete failed');
        }
    };

    const handleDeleteDbLeiche = async (m: { id: number; serverName: string }) => {
        if (!confirm(`"${m.serverName}" aus DB entfernen? Der Disk-Folder existiert nicht mehr — der DB-Eintrag ist eine Leiche.`)) return;
        setDeletingDbId(m.id);
        const res = await deleteServer(m.id);
        setDeletingDbId(null);
        if (res.success) {
            setData(prev => prev ? { ...prev, missing: prev.missing.filter(x => x.id !== m.id) } : prev);
            onOrphanDeleted();
        } else {
            alert(res.message ?? 'Delete failed');
        }
    };

    const orphanCount = data?.orphaned.length ?? 0;

    return (
        <div className="border border-(--base-03) rounded-lg overflow-hidden">
            <button
                onClick={() => expanded ? setExpanded(false) : (data ? setExpanded(true) : load())}
                className="w-full flex items-center justify-between px-4 py-3 bg-(--base-02) hover:bg-(--base-03) transition-colors text-left"
            >
                <div className="flex items-center gap-2.5">
                    <div className={`w-1.5 h-1.5 rounded-full ${node.status === 'online' ? 'bg-(--success-light)' : 'bg-(--error)'}`} />
                    <span className="text-sm font-medium text-(--base-09)">{node.name}</span>
                    {orphanCount > 0 && (
                        <span className="badge bg-(--warning-ghost) text-(--warning) border border-(--warning-border) text-[10px]">
                            {orphanCount} orphaned
                        </span>
                    )}
                    {data && data.matched.length > 0 && (
                        <span className="text-[11px] text-(--base-06)">{data.matched.length} matched · {data.missing.length} missing</span>
                    )}
                </div>
                <div className="flex items-center gap-2">
                    {loading && <Loader2 size={14} className="animate-spin text-(--base-05)" />}
                    {!data && !loading && (
                        <span className="text-[11px] text-(--accent-light)">Run analysis</span>
                    )}
                    {data && (expanded ? <ChevronDown size={14} className="text-(--base-05)" /> : <ChevronRight size={14} className="text-(--base-05)" />)}
                </div>
            </button>

            {expanded && data && (
                <div className="p-4 space-y-4">
                    {data.orphaned.length > 0 && (
                        <div>
                            <p className="text-[10px] font-mono uppercase tracking-[0.08em] text-(--warning) mb-2 flex items-center gap-1.5">
                                <AlertTriangle size={10} />
                                Orphaned (disk only — no DB record)
                            </p>
                            <div className="space-y-1">
                                {data.orphaned.map(o => (
                                    <div key={o.uuid} className="flex items-center justify-between px-3 py-2 bg-(--base-02) rounded-md border border-(--warning-border)/30">
                                        <span className="font-mono text-xs text-(--base-07) truncate">{o.uuid}</span>
                                        <button
                                            onClick={() => handleDelete(o.uuid)}
                                            disabled={deleting === o.uuid}
                                            className="flex items-center gap-1.5 text-[11px] text-(--error-light) hover:bg-(--error-ghost) px-2 py-1 rounded transition-colors ml-3 shrink-0"
                                        >
                                            {deleting === o.uuid ? <Loader2 size={12} className="animate-spin" /> : <Trash2 size={12} />}
                                            Delete
                                        </button>
                                    </div>
                                ))}
                            </div>
                        </div>
                    )}

                    {data.nodeOnline === false && (
                        <div className="flex items-start gap-2 bg-(--warning-ghost) border border-(--warning-border) rounded-md px-3 py-2.5 text-xs text-(--warning)">
                            <AlertTriangle size={13} className="shrink-0 mt-0.5" />
                            <span>Node ist offline / kein Heartbeat — DB-Leichen können nicht zuverlässig erkannt werden.</span>
                        </div>
                    )}

                    {data.nodeOnline !== false && data.missing.length > 0 && (
                        <div>
                            <p className="text-[10px] font-mono uppercase tracking-[0.08em] text-(--base-06) mb-2">
                                Missing from disk (DB-Leichen)
                            </p>
                            <div className="space-y-1">
                                {data.missing.map(m => (
                                    <div key={m.uuid} className="flex items-center justify-between px-3 py-2 bg-(--base-02) rounded-md border border-(--base-03)">
                                        <div className="min-w-0">
                                            <span className="text-sm text-(--base-07)">{m.serverName}</span>
                                            <span className="font-mono text-[10px] text-(--base-05) ml-2">{m.uuid}</span>
                                        </div>
                                        <button
                                            onClick={() => handleDeleteDbLeiche(m)}
                                            disabled={deletingDbId === m.id}
                                            className="flex items-center gap-1.5 text-[11px] text-(--error-light) hover:bg-(--error-ghost) px-2 py-1 rounded transition-colors ml-3 shrink-0"
                                        >
                                            {deletingDbId === m.id ? <Loader2 size={12} className="animate-spin" /> : <Trash2 size={12} />}
                                            Aus DB entfernen
                                        </button>
                                    </div>
                                ))}
                            </div>
                        </div>
                    )}

                    {data.nodeOnline !== false && data.orphaned.length === 0 && data.missing.length === 0 && (
                        <p className="text-xs text-(--success-light) flex items-center gap-1.5">
                            <Check size={13} />
                            All {data.matched.length} server folders match DB records
                        </p>
                    )}
                </div>
            )}
        </div>
    );
}
