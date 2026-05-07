"use client";

import React, { Suspense, useState, useEffect, useCallback } from 'react';
import { useSearchParams } from 'next/navigation';
import { AlertTriangle, RefreshCw } from 'lucide-react';
import { getNodes, Node } from '@/lib/api';
import { DiskAnalysisPanel } from '@/components/admin/DiskAnalysisPanel';

function AdminDiskInner() {
    const searchParams = useSearchParams();
    const focusNodeId = searchParams.get('node');

    const [nodes, setNodes] = useState<Node[]>([]);
    const [loading, setLoading] = useState(true);
    const [autoExpandNodeId, setAutoExpandNodeId] = useState<number | null>(
        focusNodeId ? Number(focusNodeId) : null,
    );
    const [refreshKey, setRefreshKey] = useState(0);

    const refresh = useCallback(() => setRefreshKey(k => k + 1), []);

    useEffect(() => {
        setLoading(true);
        getNodes().then(res => {
            setNodes(res.success ? (res.nodes ?? []) : []);
            setLoading(false);
        });
    }, [refreshKey]);

    return (
        <div className="flex flex-col gap-3 h-full">
            <div className="flex items-start justify-between gap-2">
                <p className="text-sm text-(--base-06)">
                    Compare UUID folders on each node's disk against database records. Click a node to run the analysis.
                </p>
                <button onClick={refresh} className="btn btn-secondary px-3 py-1.5 text-sm flex items-center gap-1.5 shrink-0">
                    <RefreshCw size={13} />
                    Refresh
                </button>
            </div>
            <div className="flex items-start gap-2 bg-(--warning-ghost) border border-(--warning-border) rounded-md px-3 py-2.5 text-xs text-(--warning)">
                <AlertTriangle size={13} className="shrink-0 mt-0.5" />
                <span>Deleting orphaned folders or DB entries is permanent. Verify before deleting.</span>
            </div>
            <div className="space-y-2 pb-4 overflow-auto">
                {loading ? (
                    <p className="text-sm text-(--base-05) text-center py-8">Loading nodes…</p>
                ) : nodes.length === 0 ? (
                    <p className="text-sm text-(--base-05) text-center py-8">No nodes registered</p>
                ) : (
                    nodes.map(n => (
                        <DiskAnalysisPanel
                            key={n.id}
                            node={n}
                            onOrphanDeleted={refresh}
                            autoLoad={autoExpandNodeId === n.id}
                            onAutoLoadConsumed={() => setAutoExpandNodeId(null)}
                        />
                    ))
                )}
            </div>
        </div>
    );
}

export default function AdminDiskPage() {
    return (
        <Suspense fallback={<div className="text-sm text-(--base-06) p-4">Loading…</div>}>
            <AdminDiskInner />
        </Suspense>
    );
}
