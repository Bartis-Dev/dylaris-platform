"use client";

import { EdgeCard, SpliceVersionSummary } from './InfraCards';
import { useInfra } from './context';

export default function EdgesPanel() {
    const { edges } = useInfra();

    if (edges.length === 0) {
        return (
            <div className="card p-8 text-center text-(--base-06) text-sm">
                No edges registered — edges auto-discover via Redis
            </div>
        );
    }

    return (
        <>
            <SpliceVersionSummary edges={edges} />
            <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-3">
                {edges.map(edge => <EdgeCard key={edge.edge_id} edge={edge} />)}
            </div>
        </>
    );
}
