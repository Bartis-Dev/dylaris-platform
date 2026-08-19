"use client";

import { useState } from 'react';
import { HardDrive, Plus, Info } from 'lucide-react';
import { nodeLabel } from '@/lib/nodeLabel';
import { nodeConnectivity, dotFor } from '@/lib/connectivity';
import { SkeletonCard } from '@/components/Skeleton';
import AddNodeModal from '@/components/AddNodeModal';

// ---------------------------------------------------------------------------
// The OPERATOR's own machines outside the swarm.
//
// A separate tab because it is a different set of machines with a different
// owner, not a different view of the same list. Until this existed, an admin
// opening "my machines" got the entire fleet - every swarm host included - next
// to the customers' hardware, because the node list was only ever scoped for
// non-admins.
//
// Core enforces the same rule on ?scope=external, so this being admin-only is
// not a matter of which tabs the panel draws.
// ---------------------------------------------------------------------------

export interface ExternalNode {
    id: number;
    name: string;
    displayName?: string;
    status: string;
    lastSeenAt?: string;
    serverCount?: number;
    region?: string;
}

export default function ExternalNodesPanel({ nodes, loading, readAt }: {
    nodes: ExternalNode[];
    loading: boolean;
    /** When the list was read, so "how long since the last heartbeat" has a clock
        that does not change just because React re-rendered. */
    readAt: number;
}) {
    const [showAdd, setShowAdd] = useState(false);

    return (
        <section className="card p-5 space-y-4">
            <div className="flex flex-wrap items-start justify-between gap-3">
                <div className="min-w-0">
                    <h2 className="text-sm font-display font-semibold text-(--accent-light) flex items-center gap-2">
                        <HardDrive size={15} /> External nodes
                    </h2>
                    <p className="text-sm text-(--base-07) mt-1 max-w-2xl">
                        Machines you run yourself that reach the platform over the warp overlay rather
                        than sitting in the cluster. Hosts that are part of the swarm are not listed
                        here — they belong to the fleet, under Infrastructure.
                    </p>
                </div>
                <button type="button" onClick={() => setShowAdd(true)} className="btn btn-secondary btn-sm shrink-0">
                    <Plus size={13} /> Add a machine
                </button>
            </div>

            {loading ? (
                <SkeletonCard height="h-16" />
            ) : nodes.length === 0 ? (
                <div className="space-y-2">
                    <p className="text-sm text-(--base-06)">No external machine connected.</p>
                    {/* Worth saying plainly: a node only lands here once it has
                        reported the tag, and nodes did not report it at all before
                        2026-08-19. An operator with external machines that predate
                        that sees an empty tab until they restart. */}
                    <p className="flex items-start gap-1.5 text-xs text-(--base-06)">
                        <Info size={12} className="mt-0.5 shrink-0" />
                        <span>
                            A machine appears here once it has sent a heartbeat with{' '}
                            <code className="input-mono text-(--base-08)">NODE_EXTERNAL=true</code>. One that
                            has been running since before this tab existed reports it after its next
                            restart.
                        </span>
                    </p>
                </div>
            ) : (
                <div className="space-y-2">
                    {nodes.map(n => {
                        const { tier } = nodeConnectivity(n.status, n.lastSeenAt, readAt);
                        return (
                            <div
                                key={n.id}
                                className="flex items-center justify-between gap-3 rounded-md bg-(--base-02) border border-(--base-03) px-3 py-2.5"
                            >
                                <div className="flex items-center gap-2.5 min-w-0">
                                    <div className={`w-2 h-2 rounded-full shrink-0 ${dotFor(tier, 'bg-(--success-light)')}`} />
                                    <div className="min-w-0">
                                        <div className="text-sm text-(--base-09) truncate">{nodeLabel(n)}</div>
                                        <div className="mono-label">
                                            {n.status}
                                            {n.region && ` · ${n.region}`}
                                            {typeof n.serverCount === 'number' &&
                                                ` · ${n.serverCount} server${n.serverCount === 1 ? '' : 's'}`}
                                        </div>
                                    </div>
                                </div>
                            </div>
                        );
                    })}
                </div>
            )}

            {showAdd && <AddNodeModal onClose={() => setShowAdd(false)} />}
        </section>
    );
}
