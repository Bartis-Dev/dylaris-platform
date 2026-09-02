"use client";

import { useState } from 'react';
import { AlertTriangle, X } from 'lucide-react';
import { useRouter } from 'next/navigation';
import { getNodeServers, forceDeleteNode } from '@/lib/api';
import { isKind, NODE_KIND_DESCRIPTION, type NodeKind } from '@/lib/nodeKind';
import { NodeCard, type NodeInfo, type ServerInfo } from './InfraCards';
import { useInfra } from './context';

/**
 * One tab's worth of machines.
 *
 * The three tabs are the same component because they are the same list with a
 * different owner - the difference that matters is WHOSE hardware it is, and
 * that belongs in lib/nodeKind next to Core's own predicates, not in three
 * near-identical screens.
 */

const EMPTY: Record<NodeKind, string> = {
    platform: 'No nodes registered',
    external: 'No machines of your own outside the swarm',
    byon: 'No customer has brought a machine yet',
};

export default function NodesPanel({ kind }: { kind: NodeKind }) {
    const infra = useInfra();
    const router = useRouter();
    const [deleteModal, setDeleteModal] = useState<{ node: NodeInfo; servers: ServerInfo[] } | null>(null);
    const [confirmName, setConfirmName] = useState('');
    const [deleting, setDeleting] = useState(false);
    const [toast, setToast] = useState<string | null>(null);

    const nodes = isKind(infra.nodes, kind);

    async function openDeleteModal(node: NodeInfo) {
        try {
            const res = await getNodeServers(node.id);
            setDeleteModal({ node, servers: res?.servers || [] });
            setConfirmName('');
        } catch { setToast('Failed to load node servers'); }
    }

    async function handleForceDelete() {
        if (!deleteModal) return;
        setDeleting(true);
        try {
            const res = await forceDeleteNode(deleteModal.node.id);
            if (res?.success) {
                setToast(`Node "${deleteModal.node.name}" deleted`);
                setDeleteModal(null);
                infra.refresh(true);
            } else { setToast('Delete failed'); }
        } catch { setToast('Delete failed'); } finally { setDeleting(false); }
    }

    return (
        <>
            <p className="text-xs text-(--base-06) max-w-3xl -mt-1">{NODE_KIND_DESCRIPTION[kind]}</p>

            {nodes.length === 0 ? (
                <div className="card p-8 text-center text-(--base-06) text-sm">{EMPTY[kind]}</div>
            ) : (
                <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-3">
                    {nodes.map(node => (
                        <NodeCard
                            key={node.id}
                            node={node}
                            onDelete={openDeleteModal}
                            gatewayEnabled={infra.gatewayEnabled}
                            onNavigateToAdminDisk={(nodeId: number) => router.push(`/admin/disk?node=${nodeId}`)}
                        />
                    ))}
                </div>
            )}

            {deleteModal && (
                <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60">
                    <div className="card w-full max-w-md p-6 flex flex-col gap-4">
                        <div className="flex items-center justify-between">
                            <div className="flex items-center gap-2">
                                <AlertTriangle size={18} className="text-(--error)" />
                                <h2 className="h-section">Force Delete Node</h2>
                            </div>
                            <button onClick={() => setDeleteModal(null)} className="p-1 text-(--base-06) hover:text-(--base-09)">
                                <X size={16} />
                            </button>
                        </div>
                        <div className="rounded-md bg-(--error)/10 border border-(--error)/30 p-3">
                            <p className="text-sm text-(--error)">
                                This will permanently delete node <strong>&quot;{deleteModal.node.name}&quot;</strong>
                                {deleteModal.servers.length > 0 && (
                                    <> and all <strong>{deleteModal.servers.length}</strong> server{deleteModal.servers.length !== 1 ? 's' : ''} on it</>
                                )}. This cannot be undone.
                            </p>
                        </div>
                        {deleteModal.servers.length > 0 && (
                            <div>
                                <p className="mono-label mb-2">Servers on this node</p>
                                <div className="rounded-md border border-(--base-03) divide-y divide-(--base-03) max-h-40 overflow-y-auto">
                                    {deleteModal.servers.map(srv => (
                                        <div key={srv.id} className="px-3 py-2 flex items-center justify-between">
                                            <div>
                                                <p className="text-sm text-(--base-09)">{srv.name}</p>
                                                <p className="text-[10px] font-mono text-(--base-05)">{srv.uuid}</p>
                                            </div>
                                            <span className="text-[10px] font-mono uppercase text-(--base-06)">{srv.status}</span>
                                        </div>
                                    ))}
                                </div>
                            </div>
                        )}
                        <div>
                            <label className="mono-label block mb-1.5">
                                Type &quot;{deleteModal.node.name}&quot; to confirm
                            </label>
                            <input
                                type="text"
                                value={confirmName}
                                onChange={e => setConfirmName(e.target.value)}
                                placeholder={deleteModal.node.name}
                                className="w-full px-3 py-2 rounded-md bg-(--base-02) border border-(--base-04) text-(--base-09) text-sm focus:border-(--error) focus:shadow-[0_0_0_3px_rgba(220,38,38,0.15)] outline-none transition-all"
                            />
                        </div>
                        <div className="flex justify-end gap-2">
                            <button onClick={() => setDeleteModal(null)} className="px-4 py-2 rounded-md text-sm text-(--base-07) hover:text-(--base-09) transition-colors">
                                Cancel
                            </button>
                            <button
                                onClick={handleForceDelete}
                                disabled={confirmName !== deleteModal.node.name || deleting}
                                className="px-4 py-2 rounded-md text-sm font-semibold bg-(--error) text-white hover:opacity-90 transition-opacity disabled:opacity-40 disabled:cursor-not-allowed"
                            >
                                {deleting ? 'Deleting...' : 'Force Delete'}
                            </button>
                        </div>
                    </div>
                </div>
            )}

            {toast && (
                <div className="fixed bottom-6 right-6 z-50 px-4 py-2.5 rounded-md bg-(--base-02) border border-(--base-04) text-sm text-(--base-09) shadow-lg">
                    {toast}
                </div>
            )}
        </>
    );
}
