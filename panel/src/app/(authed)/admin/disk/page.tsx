"use client";

import React, { Suspense, useState, useEffect, useCallback } from 'react';
import { useSearchParams } from 'next/navigation';
import { AlertTriangle, RefreshCw } from 'lucide-react';
import { getNodes, Node } from '@/lib/api';
import { DiskAnalysisPanel } from '@/components/admin/DiskAnalysisPanel';
import { SkeletonHeader, SkeletonCard } from '@/components/Skeleton';

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
                <button onClick={refresh} className="btn btn-secondary btn-sm shrink-0">
                    <RefreshCw size={13} />
                    Refresh
                </button>
            </div>
            <div className="alert alert-warning text-(--warning) text-xs">
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
        <Suspense fallback={
            <div className="p-4 space-y-3">
                <SkeletonHeader />
                <SkeletonCard height="h-40" />
            </div>
        }>
            <AdminDiskInner />
        </Suspense>
    );
}
