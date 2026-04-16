"use client";

import React, { useState, useEffect } from 'react';
import { getNodes, Node } from '@/lib/api';
import { Network, Server, Globe } from 'lucide-react';

export default function NodesTab() {
    const [nodes, setNodes] = useState<Node[]>([]);

    useEffect(() => {
        loadNodes();
        const interval = setInterval(loadNodes, 5000);
        return () => clearInterval(interval);
    }, []);

    const loadNodes = async () => {
        const res = await getNodes();
        if (res.success) setNodes(res.nodes);
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
                        </div>
                    ))
                )}
            </div>
        </div>
    );
}
