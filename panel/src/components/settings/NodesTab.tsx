"use client";

import React, { useState, useEffect } from 'react';
import {
    getNodes, Node,
    getPlacementSettings, savePlacementSettings, PlacementSettings,
    setNodePlacement, configureNode, Region,
    getNodeCpu, getNodeStorage, getNodeDeployBundle, updateNodeCpuset, type NodeCpuTopology,
    getNodeAdmission, updateNodeAdmission, addAdmissionCIDR, deleteAdmissionCIDR, resetNodePairing,
    mintEnrollToken, listEnrollTokens, revokeEnrollToken, type AdmissionCIDR, type NodeEnrollToken,
} from '@/lib/api';
import { getSystemFeatures } from '@/lib/api/featureFlags';
import { parseCpuset, compactCpuset } from '@/lib/cpuset';
import { SkeletonHeader, SkeletonCard } from '@/components/Skeleton';
import { regionLabel, regionFlag } from '@/lib/regions';
import { useAppData } from '@/lib/AppDataContext';
import {
    Network, Server, Globe, Settings as SettingsIcon, Save,
    CircleCheck, CircleAlert, Pencil, X, AlertTriangle, SlidersHorizontal, Cpu, KeyRound, Copy,
    ShieldCheck, Plus, Trash2, Ticket, RotateCcw,
} from 'lucide-react';

// Shape of GET /nodes/{id}/deploy-bundle — the secret-free node + link deploy
// ENV for an already-enrolled node (see WarpTab's mint+reveal pattern).
interface DeployBundle {
    nodeId: string;
    grpcTlsFingerprint: string;
    linkSecret: string;
    linkDiscoveryProof: string;
}

type SubTab = 'nodes' | 'placement';

const NAV_ITEMS: { id: SubTab; label: string; icon: React.ElementType }[] = [
    { id: 'nodes', label: 'Nodes', icon: Server },
    { id: 'placement', label: 'Placement', icon: SettingsIcon },
];

export default function NodesTab() {
    const [subTab, setSubTab] = useState<SubTab>('nodes');
    const [toast, setToast] = useState<{ msg: string; ok: boolean } | null>(null);

    const showToast = (msg: string, ok = true) => {
        setToast({ msg, ok });
        setTimeout(() => setToast(null), 3500);
    };

    return (
        <div className="flex gap-0 h-full">
            <nav className="w-44 shrink-0 border-r border-(--base-03) pr-4 flex flex-col gap-1 pt-1">
                {NAV_ITEMS.map(({ id, label, icon: Icon }) => (
                    <button
                        key={id}
                        onClick={() => setSubTab(id)}
                        className={`flex items-center gap-2.5 px-3 py-2 rounded-md text-sm font-medium transition-colors text-left ${
                            subTab === id
                                ? 'bg-(--accent)/10 text-(--accent-light)'
                                : 'text-(--base-07) hover:text-(--base-09) hover:bg-(--base-03)'
                        }`}
                    >
                        <Icon size={15} className={subTab === id ? 'text-(--accent-light)' : 'text-(--base-06)'} />
                        {label}
                    </button>
                ))}
            </nav>

            <div className="flex-1 pl-6 overflow-y-auto">
                {subTab === 'nodes' && <NodesPanel showToast={showToast} />}
                {subTab === 'placement' && <PlacementPanel showToast={showToast} />}
            </div>

            {toast && (
                <div className="toast-container">
                    <div className="toast">
                        <div className={`toast-bar ${toast.ok ? 'bg-(--success-light)' : 'bg-(--error-light)'}`}></div>
                        {toast.ok ? <CircleCheck size={14} /> : <CircleAlert size={14} />}
                        <span className="text-sm text-(--base-09)">{toast.msg}</span>
                    </div>
                </div>
            )}
        </div>
    );
}

// ─────────────────────────────────────────────
// Nodes list (existing UX + per-node placement edit)
// ─────────────────────────────────────────────

// A node is "external" (home/Warp node) when its tags carry the `external`
// marker — same client-side derivation the badge rendering already uses.
// External nodes can only carry traffic in gateway routing mode, so when
// Game Traffic is on IP:Port they're shown as unusable.
function isExternalNode(node: Node): boolean {
    return !!node.tags && node.tags.split(',').map(t => t.trim()).includes('external');
}

function NodesPanel({ showToast }: { showToast: (msg: string, ok?: boolean) => void }) {
    // Applied routing mode from the shared app context (same source WarpTab
    // gates on) — no extra fetch, no new store. regions feed the config picker.
    const { routingMode, regions } = useAppData();
    const gatewayOff = routingMode === 'ip_port';

    const [nodes, setNodes] = useState<Node[]>([]);
    const [editingPlacement, setEditingPlacement] = useState<number | null>(null);
    const [editingConfig, setEditingConfig] = useState<number | null>(null);
    const [revealed, setRevealed] = useState<DeployBundle | null>(null);
    const [revealingId, setRevealingId] = useState<number | null>(null);
    const [resettingId, setResettingId] = useState<number | null>(null);
    const [resetReveal, setResetReveal] = useState<{ nodeId: string; token: string; env: string } | null>(null);

    useEffect(() => {
        loadNodes();
        const interval = setInterval(loadNodes, 5000);
        return () => clearInterval(interval);
    }, []);

    const loadNodes = async () => {
        const res = await getNodes();
        if (res.success) setNodes(res.nodes);
    };

    const revealDeployBundle = async (nodeId: number) => {
        setRevealingId(nodeId);
        const res = await getNodeDeployBundle(nodeId);
        setRevealingId(null);
        if (res.success) {
            setRevealed({
                nodeId: res.nodeId,
                grpcTlsFingerprint: res.grpcTlsFingerprint,
                linkSecret: res.linkSecret,
                linkDiscoveryProof: res.linkDiscoveryProof,
            });
        } else {
            showToast(res.message || res.error || 'Failed to load deploy bundle', false);
        }
    };

    const resetPairing = async (node: Node) => {
        if (!window.confirm(`Reset pairing for "${node.name}"? Its current secret is invalidated immediately; the node must re-pair with the one-time recovery token you are about to receive. Server data is preserved.`)) return;
        setResettingId(node.id);
        const res = await resetNodePairing(node.id);
        setResettingId(null);
        if (res.success && res.token) {
            setResetReveal({ nodeId: node.token, token: res.token, env: res.env || `NODE_RECOVERY_TOKEN=${res.token}` });
        } else {
            showToast(res.message || 'Reset failed.', false);
        }
    };

    // Extracted so the copy buttons below can send the exact same string that
    // is rendered in the <pre> blocks, with no duplication.
    const nodeEnv = revealed ? `REDIS_ACL_ENABLED=true
GRPC_TLS_ENABLED=true
GRPC_TLS_FINGERPRINT=${revealed.grpcTlsFingerprint}
NODE_ENROLL_TOKEN=<your enroll token>
CORE_GRPC_ADDR=<core-host:25520>
NODE_MANAGES_LINK=true
LINK_IMAGE=<public link image, e.g. ghcr.io/callmebartis/dylaris/link:latest>` : '';
    const linkEnv = revealed ? `NODE_ID=${revealed.nodeId}
LINK_SECRET=${revealed.linkSecret}
LINK_DISCOVERY_PROOF=${revealed.linkDiscoveryProof}` : '';

    return (
        <div className="space-y-6">
            <div className="card border-(--accent-border) p-6">
                <h3 className="text-base font-display font-bold text-(--accent-light) mb-2 flex items-center gap-2">
                    <Network size={18} /> Auto-Discovery Active
                </h3>
                <p className="text-sm text-(--base-07)">
                    New nodes register automatically when the <code className="bg-(--base-03) px-1.5 py-0.5 rounded-sm text-(--base-08) font-mono text-xs">dylaris-platform</code> stack is deployed on a node-labeled host with the cluster secret. No manual setup needed.
                </p>
                <p className="text-xs text-(--base-06) mt-3">
                    Label a Swarm host as a node: <code className="bg-(--base-03) px-1.5 py-0.5 rounded-sm text-(--base-08) font-mono text-xs">docker node update --label-add role=node &lt;hostname&gt;</code>
                </p>
            </div>

            <div>
                <h3 className="text-base font-display font-bold text-(--base-09) mb-4">Connected Nodes</h3>
                <div className="space-y-3">
                    {nodes.length === 0 ? (
                        <div className="text-center p-8 border border-dashed border-(--base-04) rounded-lg text-(--base-06) text-sm">
                            No nodes connected. Start a node!
                        </div>
                    ) : (
                        nodes.map(node => (
                            <NodeCard
                                key={node.id}
                                node={node}
                                regions={regions}
                                gatewayRequired={isExternalNode(node) && gatewayOff}
                                isEditing={editingPlacement === node.id}
                                isConfiguring={editingConfig === node.id}
                                onEdit={() => { setEditingConfig(null); setEditingPlacement(node.id); }}
                                onCancel={() => setEditingPlacement(null)}
                                onSaved={() => { setEditingPlacement(null); loadNodes(); showToast('Placement updated'); }}
                                onConfigure={() => { setEditingPlacement(null); setEditingConfig(node.id); }}
                                onConfigCancel={() => setEditingConfig(null)}
                                onConfigSaved={() => { setEditingConfig(null); loadNodes(); showToast('Node configured'); }}
                                onCpuPoolSaved={() => { loadNodes(); showToast('Container CPU pool updated'); }}
                                onError={msg => showToast(msg, false)}
                                onRevealDeployBundle={() => revealDeployBundle(node.id)}
                                revealingDeployBundle={revealingId === node.id}
                                onResetPairing={() => resetPairing(node)}
                                resettingPairing={resettingId === node.id}
                            />
                        ))
                    )}
                </div>
            </div>

            <AdmissionCard showToast={showToast} />
            <EnrollTokensSection showToast={showToast} />

            {resetReveal && (
                <div className="modal-overlay animate-fade-in" onClick={() => setResetReveal(null)}>
                    <div className="modal-panel max-w-lg" onClick={e => e.stopPropagation()}>
                        <div className="modal-header"><h3 className="modal-title text-(--accent-light)">Recovery token — {resetReveal.nodeId.slice(0, 8)}</h3></div>
                        <div className="modal-body space-y-3">
                            <p className="text-sm text-(--base-07)">The node&apos;s secret is now invalidated. Deliver this token to the node and restart it. Shown once.</p>
                            <div className="space-y-1">
                                <label className="mono-label">Recovery ENV</label>
                                <pre className="p-3 rounded-md bg-(--base-02) border border-(--base-04) font-mono text-xs whitespace-pre-wrap break-all">{resetReveal.env}</pre>
                                <button onClick={() => { navigator.clipboard.writeText(resetReveal.env); showToast('Recovery ENV copied.', true); }} className="btn btn-secondary btn-sm">
                                    <Copy size={12} /> Copy recovery ENV
                                </button>
                            </div>
                        </div>
                        <div className="modal-footer"><button onClick={() => setResetReveal(null)} className="btn btn-primary">Done</button></div>
                    </div>
                </div>
            )}

            {revealed && (
                <div className="modal-overlay animate-fade-in" onClick={() => setRevealed(null)}>
                    <div className="modal-panel max-w-xl" onClick={e => e.stopPropagation()}>
                        <div className="modal-header"><h3 className="modal-title text-(--accent-light)">Deploy bundle — {revealed.nodeId.slice(0, 8)}</h3></div>
                        <div className="modal-body space-y-3">
                            <div className="space-y-1">
                                <label className="mono-label">Node deploy ENV (secret-free)</label>
                                <pre className="p-3 rounded-md bg-(--base-02) border border-(--base-04) font-mono text-xs whitespace-pre-wrap break-all">{nodeEnv}</pre>
                                <button onClick={() => { navigator.clipboard.writeText(nodeEnv); showToast('Node deploy ENV copied.', true); }} className="btn btn-secondary btn-sm">
                                    <Copy size={12} /> Copy node ENV
                                </button>
                            </div>
                            <div className="space-y-1">
                                <label className="mono-label">Link deploy ENV (manual / DC only)</label>
                                <p className="text-xs text-(--base-06)">A secret-free node auto-provisions its own Link sidecar (set LINK_IMAGE + NODE_MANAGES_LINK). Use this only for a manually deployed / DC Link.</p>
                                <pre className="p-3 rounded-md bg-(--base-02) border border-(--base-04) font-mono text-xs whitespace-pre-wrap break-all">{linkEnv}</pre>
                                <button onClick={() => { navigator.clipboard.writeText(linkEnv); showToast('Link deploy ENV copied.', true); }} className="btn btn-secondary btn-sm">
                                    <Copy size={12} /> Copy link ENV
                                </button>
                            </div>
                        </div>
                        <div className="modal-footer"><button onClick={() => setRevealed(null)} className="btn btn-primary">Done</button></div>
                    </div>
                </div>
            )}
        </div>
    );
}

interface NodeCardProps {
    node: Node;
    regions: Region[];
    // External node + gateway not active — its servers can't receive player
    // traffic or file access until routing mode is switched to Gateway/Both.
    gatewayRequired: boolean;
    isEditing: boolean;
    isConfiguring: boolean;
    onEdit: () => void;
    onCancel: () => void;
    onSaved: () => void;
    onConfigure: () => void;
    onConfigCancel: () => void;
    onConfigSaved: () => void;
    onCpuPoolSaved: () => void;
    onError: (msg: string) => void;
    onRevealDeployBundle: () => void;
    revealingDeployBundle: boolean;
    onResetPairing: () => void;
    resettingPairing: boolean;
}

function NodeCard({ node, regions, gatewayRequired, isEditing, isConfiguring, onEdit, onCancel, onSaved, onConfigure, onConfigCancel, onConfigSaved, onCpuPoolSaved, onError, onRevealDeployBundle, revealingDeployBundle, onResetPairing, resettingPairing }: NodeCardProps) {
    const [cpuRatio, setCpuRatio] = useState(node.cpuOvercommitRatio ?? 1.0);
    const [ramRatio, setRamRatio] = useState(node.ramOvercommitRatio ?? 1.0);
    const [saving, setSaving] = useState(false);
    // Best-effort CPU topology for the P/E breakdown chip. Non-fatal: the
    // cores stat falls back to node.totalCpu when the node hasn't reported.
    const [cpuTopology, setCpuTopology] = useState<NodeCpuTopology | null>(null);
    // The node's allowed container core pool ("" = all cores). Drives the pool
    // editor below; refetched whenever the node prop changes (the tab polls).
    const [nodeCpuset, setNodeCpuset] = useState('');
    // Best-effort per-path storage summary for the Storage stat. Non-fatal:
    // falls back to '—' when the node hasn't reported (offline / no heartbeat).
    const [nodeStorage, setNodeStorage] = useState<{ total_bytes: number; free_bytes: number }[] | null>(null);

    useEffect(() => {
        setCpuRatio(node.cpuOvercommitRatio ?? 1.0);
        setRamRatio(node.ramOvercommitRatio ?? 1.0);
    }, [node.cpuOvercommitRatio, node.ramOvercommitRatio, isEditing]);

    useEffect(() => {
        let cancelled = false;
        getNodeCpu(node.id).then(res => {
            if (!cancelled && res?.success) {
                setCpuTopology(res.topology ?? null);
                setNodeCpuset(res.nodeCpuset ?? '');
            }
        }).catch(() => { /* non-fatal */ });
        return () => { cancelled = true; };
    }, [node.id]);

    useEffect(() => {
        let cancelled = false;
        getNodeStorage(node.id).then(res => {
            if (!cancelled && res?.success) setNodeStorage(res.storage ?? []);
        }).catch(() => { /* non-fatal */ });
        return () => { cancelled = true; };
    }, [node.id]);

    // "8P + 8E" when the node reports a hybrid layout, else null.
    const peBreakdown = cpuTopology?.hybrid
        ? (() => {
            const p = cpuTopology.cores.filter(c => c.type === 'P').length;
            const e = cpuTopology.cores.filter(c => c.type === 'E').length;
            return `${p}P + ${e}E`;
        })()
        : null;

    const handleSave = async () => {
        if (cpuRatio <= 0 || ramRatio <= 0) {
            onError('Overcommit ratios must be > 0');
            return;
        }
        setSaving(true);
        const res = await setNodePlacement(node.id, { cpuOvercommitRatio: cpuRatio, ramOvercommitRatio: ramRatio });
        setSaving(false);
        if (res.success) onSaved();
        else onError(res.message || 'Save failed');
    };

    return (
        <div className="card p-3 transition-colors hover:border-(--base-05)">
            <div className="flex flex-col md:flex-row md:items-center justify-between gap-4">
                <div className="flex items-center gap-4 min-w-0">
                    <div className={`status-dot shrink-0 ${node.status === 'online' ? 'bg-(--success-light) shadow-[0_0_8px_var(--success-light)]' : 'bg-(--error-light)'}`} title={node.status}></div>
                    <div className="w-10 h-10 bg-(--accent-ghost) text-(--accent-light) rounded-md flex items-center justify-center border border-(--accent-border) shrink-0">
                        <Server size={24} />
                    </div>
                    <div className="flex flex-col md:flex-row md:items-center gap-2 md:gap-3 min-w-0">
                        <div className="font-medium text-sm text-(--base-09) whitespace-nowrap truncate">
                            {node.displayName || node.name || (node.token ? node.token.slice(0, 8) : '')}
                        </div>
                        <div className="h-4 w-px bg-(--base-04) hidden md:block"></div>
                        <div className="text-xs font-mono text-(--base-06) flex items-center bg-(--base-01) px-2 py-1 rounded-sm border border-(--base-04) w-fit whitespace-nowrap">
                            <Globe size={14} className="mr-1.5 opacity-70" />
                            {node.address}
                        </div>
                    </div>
                </div>

                <div className="flex items-center gap-3 shrink-0">
                    {node.region && (
                        <span className="inline-flex items-center gap-1.5 px-2 py-0.5 rounded-md bg-(--accent-ghost) border border-(--accent-border) text-(--accent-light) text-xs font-medium" title="Region (DYLARIS_REGION env)">
                            <span>{regionFlag(node.region)}</span>
                            <span>{regionLabel(node.region)}</span>
                        </span>
                    )}
                    {node.tags && node.tags !== 'auto-discovered' && (
                        <div className="flex flex-wrap gap-1.5">
                            {node.tags.split(',').filter(tag => tag.trim() !== 'external').map(tag => {
                                const t = tag.trim();
                                if (!t) return null;
                                return <span key={t} className="badge badge-accent">{t}</span>;
                            })}
                        </div>
                    )}
                    {node.tags && node.tags.split(',').map(t => t.trim()).includes('external') && (
                        <span className="badge badge-accent" title="External / home node — forces gateway+beam">external</span>
                    )}
                    {node.needsConfiguration && (
                        <span
                            className="badge badge-warning inline-flex items-center gap-1"
                            title="This node booted with only a cluster secret and has no region. Configure its name, region and tags so it can be used for placement."
                        >
                            <AlertTriangle size={11} />
                            Needs configuration
                        </span>
                    )}
                    {gatewayRequired && (
                        <span
                            className="badge badge-warning inline-flex items-center gap-1"
                            title="This external node needs Gateway routing. Switch Game Traffic to Gateway or Both in the Gateway tab — otherwise its servers can't receive player traffic or file access."
                        >
                            <AlertTriangle size={11} />
                            Requires gateway
                        </span>
                    )}
                    {!isConfiguring && (
                        <button
                            onClick={onConfigure}
                            className={`text-xs inline-flex items-center gap-1 transition-colors ${
                                node.needsConfiguration
                                    ? 'text-(--accent-light) hover:text-(--accent)'
                                    : 'text-(--base-06) hover:text-(--accent-light)'
                            }`}
                            title="Configure name, region and tags"
                        >
                            <SlidersHorizontal size={11} />
                            Configure
                        </button>
                    )}
                    {!isEditing && (
                        <button
                            onClick={onEdit}
                            className="text-xs text-(--base-06) hover:text-(--accent-light) inline-flex items-center gap-1 transition-colors"
                            title="Edit placement"
                        >
                            <Pencil size={11} />
                            Placement
                        </button>
                    )}
                    <button
                        onClick={onRevealDeployBundle}
                        disabled={revealingDeployBundle}
                        className="text-xs text-(--base-06) hover:text-(--accent-light) inline-flex items-center gap-1 transition-colors disabled:opacity-40"
                        title="Reveal this node's secret-free node + link deploy ENV"
                    >
                        <KeyRound size={11} />
                        {revealingDeployBundle ? 'Loading…' : 'Deploy bundle'}
                    </button>
                    <button
                        onClick={onResetPairing}
                        disabled={resettingPairing}
                        className="text-xs text-(--base-06) hover:text-(--error-light) inline-flex items-center gap-1 transition-colors disabled:opacity-40"
                        title="Invalidate this node's secret and mint a one-time recovery token"
                    >
                        <RotateCcw size={11} />
                        {resettingPairing ? 'Resetting…' : 'Reset pairing'}
                    </button>
                </div>
            </div>

            {/* Configuration editor (name / region / tags) */}
            {isConfiguring && (
                <NodeConfigForm
                    node={node}
                    regions={regions}
                    onSaved={onConfigSaved}
                    onCancel={onConfigCancel}
                    onError={onError}
                />
            )}

            {/* Placement summary / editor */}
            <div className="mt-3 pt-3 border-t border-(--base-03) grid grid-cols-2 md:grid-cols-6 gap-3 text-xs">
                <Stat label="Total CPU" value={node.totalCpu ? `${node.totalCpu.toFixed(1)} cores` : '—'} />
                <Stat
                    label="CPU cores"
                    value={node.totalCpu ? (
                        <span className="inline-flex items-center gap-1.5">
                            {Math.round(node.totalCpu)}
                            {peBreakdown && (
                                <span className="badge badge-accent text-[9px]" title="Performance + Efficiency cores">{peBreakdown}</span>
                            )}
                        </span>
                    ) : '—'}
                />
                <Stat label="Total RAM" value={node.totalRamMb ? `${(node.totalRamMb / 1024).toFixed(1)} GB` : '—'} />
                <Stat
                    label="Storage"
                    value={nodeStorage && nodeStorage.length > 0
                        ? `${(nodeStorage.reduce((a, s) => a + s.free_bytes, 0) / 1e9).toFixed(0)} / ${(nodeStorage.reduce((a, s) => a + s.total_bytes, 0) / 1e9).toFixed(0)} GB free`
                        : '—'}
                />
                <Stat
                    label="CPU Overcommit"
                    value={isEditing ? (
                        <div className="relative inline-block w-20">
                            <input
                                type="number"
                                step={10}
                                min={10}
                                value={Math.round(cpuRatio * 100)}
                                onChange={e => setCpuRatio(Math.max(0.1, Number(e.target.value) / 100))}
                                className="input-mono w-full pr-5 text-center text-xs [appearance:textfield] [&::-webkit-outer-spin-button]:appearance-none [&::-webkit-inner-spin-button]:appearance-none"
                            />
                            <span className="absolute right-1.5 top-1/2 -translate-y-1/2 text-[10px] text-(--base-06) font-mono pointer-events-none">%</span>
                        </div>
                    ) : `${Math.round((node.cpuOvercommitRatio ?? 1.0) * 100)}%`}
                />
                <Stat
                    label="RAM Overcommit"
                    value={isEditing ? (
                        <div className="relative inline-block w-20">
                            <input
                                type="number"
                                step={10}
                                min={10}
                                value={Math.round(ramRatio * 100)}
                                onChange={e => setRamRatio(Math.max(0.1, Number(e.target.value) / 100))}
                                className="input-mono w-full pr-5 text-center text-xs [appearance:textfield] [&::-webkit-outer-spin-button]:appearance-none [&::-webkit-inner-spin-button]:appearance-none"
                            />
                            <span className="absolute right-1.5 top-1/2 -translate-y-1/2 text-[10px] text-(--base-06) font-mono pointer-events-none">%</span>
                        </div>
                    ) : `${Math.round((node.ramOvercommitRatio ?? 1.0) * 100)}%`}
                />
            </div>

            {isEditing && (
                <div className="mt-3 flex items-center gap-2 justify-end">
                    <button onClick={onCancel} className="btn btn-secondary btn-sm">
                        <X size={12} /> Cancel
                    </button>
                    <button onClick={handleSave} disabled={saving} className="btn btn-primary btn-sm disabled:opacity-40">
                        <Save size={12} /> {saving ? 'Saving…' : 'Save'}
                    </button>
                </div>
            )}

            <NodeCpuPoolEditor
                node={node}
                topology={cpuTopology}
                nodeCpuset={nodeCpuset}
                onSaved={onCpuPoolSaved}
                onError={onError}
            />
        </div>
    );
}

// NodeCpuPoolEditor lets an admin restrict which host cores this node may place
// containers on (the node's container CPU pool). Empty pool = all cores allowed.
// The restricted pool then narrows per-server CPU pinning (see CpuPinningControl).
function NodeCpuPoolEditor({
    node, topology, nodeCpuset, onSaved, onError,
}: {
    node: Node;
    topology: NodeCpuTopology | null;
    nodeCpuset: string;
    onSaved: () => void;
    onError: (msg: string) => void;
}) {
    // 'all' = no restriction; 'restrict' = pin the node to a subset of cores.
    const [restrict, setRestrict] = useState(nodeCpuset.trim() !== '');
    const [selected, setSelected] = useState<Set<number>>(() => new Set(parseCpuset(nodeCpuset)));
    const [saving, setSaving] = useState(false);

    // Re-sync from the latest server value whenever it changes (the tab polls
    // every 5s) so a fresh save elsewhere doesn't get clobbered by stale state.
    useEffect(() => {
        setRestrict(nodeCpuset.trim() !== '');
        setSelected(new Set(parseCpuset(nodeCpuset)));
    }, [nodeCpuset]);

    const toggleCore = (id: number) => {
        setSelected(prev => {
            const next = new Set(prev);
            if (next.has(id)) next.delete(id);
            else next.add(id);
            return next;
        });
    };

    const pendingCpuset = restrict ? compactCpuset([...selected]) : '';
    const dirty = pendingCpuset !== nodeCpuset.trim();

    const handleSave = async () => {
        if (restrict && selected.size === 0) {
            onError('Select at least one core, or choose "All cores".');
            return;
        }
        setSaving(true);
        const res = await updateNodeCpuset(node.id, pendingCpuset);
        setSaving(false);
        if (res?.success !== false && !res?.error) onSaved();
        else onError(res.message || res.error || 'Save failed');
    };

    return (
        <div className="mt-3 pt-3 border-t border-(--base-03) space-y-3">
            <div className="flex items-center gap-2">
                <Cpu size={13} className="text-(--base-06)" />
                <span className="mono-label">Container CPU pool</span>
            </div>

            <div className="flex bg-(--base-03) p-0.5 rounded-md w-fit">
                {([
                    { id: 'all', label: 'All cores' },
                    { id: 'restrict', label: 'Restrict to cores' },
                ] as const).map(({ id, label }) => {
                    const active = (id === 'restrict') === restrict;
                    return (
                        <button
                            key={id}
                            type="button"
                            onClick={() => setRestrict(id === 'restrict')}
                            className={`px-2.5 py-1 text-[11px] rounded-sm transition-colors ${
                                active ? 'bg-(--accent) text-white' : 'text-(--base-07) hover:text-(--base-09)'
                            }`}
                        >
                            {label}
                        </button>
                    );
                })}
            </div>

            {!restrict ? (
                <p className="text-[11px] text-(--base-06)">Containers may use any host core.</p>
            ) : topology ? (
                <div className="flex flex-col gap-2">
                    <div className="flex flex-wrap gap-1.5">
                        {topology.cores.map(core => {
                            const isSel = selected.has(core.id);
                            return (
                                <button
                                    key={core.id}
                                    type="button"
                                    onClick={() => toggleCore(core.id)}
                                    title={`Core ${core.id}${topology.hybrid ? ` (${core.type === 'P' ? 'Performance' : core.type === 'E' ? 'Efficiency' : 'standard'})` : ''}`}
                                    className={`w-10 h-10 rounded-md border flex flex-col items-center justify-center transition-colors ${
                                        isSel
                                            ? 'border-(--accent) bg-(--accent-ghost) text-(--accent-light)'
                                            : 'border-(--base-04) text-(--base-07) hover:border-(--base-05) hover:text-(--base-09)'
                                    }`}
                                >
                                    <span className="font-mono text-xs leading-none">{core.id}</span>
                                    {topology.hybrid && core.type !== 'standard' && (
                                        <span className={`font-mono text-[8px] leading-none mt-0.5 ${core.type === 'P' ? 'text-(--accent-light)' : 'text-(--base-06)'}`}>
                                            {core.type}
                                        </span>
                                    )}
                                </button>
                            );
                        })}
                    </div>
                    <p className="text-[11px] text-(--base-06)">
                        {selected.size === 0
                            ? 'No cores selected. Pick at least one.'
                            : `Pool: ${compactCpuset([...selected])}`}
                    </p>
                </div>
            ) : (
                <p className="text-[11px] text-(--base-06)">This node has not reported its CPU layout yet.</p>
            )}

            <div className="flex items-center gap-2 justify-end">
                <button
                    onClick={handleSave}
                    disabled={saving || !dirty}
                    className="btn btn-secondary btn-sm disabled:opacity-40"
                >
                    <Save size={12} /> {saving ? 'Saving…' : 'Save pool'}
                </button>
            </div>
        </div>
    );
}

// NodeConfigForm lets an admin adopt an auto-discovered node by setting its
// name, region and tags, plus an optional human display name. Saving persists
// to the DB (PATCH /nodes/{id}/config); from then on the node's heartbeat env
// no longer overwrites name/region/tags.
function NodeConfigForm({
    node, regions, onSaved, onCancel, onError,
}: {
    node: Node;
    regions: Region[];
    onSaved: () => void;
    onCancel: () => void;
    onError: (msg: string) => void;
}) {
    const [name, setName] = useState(node.name || node.token || '');
    const [displayName, setDisplayName] = useState(node.displayName || '');
    const [region, setRegion] = useState(node.region || '');
    const [tags, setTags] = useState(node.tags && node.tags !== 'auto-discovered' ? node.tags : '');
    const [saving, setSaving] = useState(false);

    const handleSave = async () => {
        if (!region) { onError('Please select a region'); return; }
        setSaving(true);
        const res = await configureNode(node.id, { name: name.trim(), region, tags: tags.trim(), displayName: displayName.trim() });
        setSaving(false);
        if (res.success) onSaved();
        else onError(res.message || res.error || 'Save failed');
    };

    return (
        <div className="mt-3 pt-3 border-t border-(--base-03) space-y-3">
            <div className="grid grid-cols-1 md:grid-cols-4 gap-3">
                <div className="flex flex-col gap-[5px]">
                    <label className="input-label">Node Name</label>
                    <input
                        value={name}
                        onChange={e => setName(e.target.value)}
                        className="input-field text-sm"
                        placeholder={node.token}
                    />
                </div>
                <div className="flex flex-col gap-[5px]">
                    <label className="input-label">Display Name</label>
                    <input
                        value={displayName}
                        onChange={e => setDisplayName(e.target.value)}
                        className="input-field text-sm"
                        placeholder={node.name || node.token}
                    />
                </div>
                <div className="flex flex-col gap-[5px]">
                    <label className="input-label">Region</label>
                    <select
                        value={region}
                        onChange={e => setRegion(e.target.value)}
                        className="input-field text-sm"
                    >
                        <option value="">Select region…</option>
                        {regions.map(rg => (
                            <option key={rg.id} value={rg.id}>{rg.displayName}</option>
                        ))}
                    </select>
                </div>
                <div className="flex flex-col gap-[5px]">
                    <label className="input-label">Tags</label>
                    <input
                        value={tags}
                        onChange={e => setTags(e.target.value)}
                        className="input-field text-sm"
                        placeholder="e.g. premium, ssd"
                    />
                </div>
            </div>
            <p className="text-xs text-(--base-06)">
                Saving adopts this node: its name, region and tags are managed here from now on and the node&apos;s env values no longer overwrite them. Display name is a purely cosmetic label shown on the card. Keep the <code className="font-mono bg-(--base-03) px-1 py-0.5 rounded text-(--base-08)">external</code> tag if this is a home/Warp node.
            </p>
            <div className="flex items-center gap-2 justify-end">
                <button onClick={onCancel} className="btn btn-secondary btn-sm">
                    <X size={12} /> Cancel
                </button>
                <button onClick={handleSave} disabled={saving} className="btn btn-primary btn-sm disabled:opacity-40">
                    <Save size={12} /> {saving ? 'Saving…' : 'Save'}
                </button>
            </div>
        </div>
    );
}

function Stat({ label, value }: { label: string; value: React.ReactNode }) {
    return (
        <div>
            <div className="mono-label">{label}</div>
            <div className="text-sm text-(--base-09) mt-0.5">{value}</div>
        </div>
    );
}

// ─────────────────────────────────────────────
// Placement (global defaults + rebalance toggle)
// ─────────────────────────────────────────────

function PlacementPanel({ showToast }: { showToast: (msg: string, ok?: boolean) => void }) {
    const [settings, setSettings] = useState<PlacementSettings>({
        cpuOvercommitDefault: 2.0,
        ramOvercommitDefault: 1.0,
        diskBufferGb: 5,
        rebalanceEnabled: false,
        rebalanceThreshold: 90,
        portMode: 'sequential',
        containerPort: 25565,
        pidsLimit: 0,
        ioWeight: 0,
    });
    const [loading, setLoading] = useState(true);
    const [saving, setSaving] = useState(false);

    useEffect(() => {
        getPlacementSettings().then(res => {
            if (res.success && res.settings) setSettings(res.settings);
            setLoading(false);
        });
    }, []);

    const handleSave = async () => {
        setSaving(true);
        const res = await savePlacementSettings(settings);
        setSaving(false);
        showToast(res.success ? 'Placement settings saved.' : (res.message || 'Save failed.'), res.success);
    };

    if (loading) return (
        <div className="space-y-6">
            <SkeletonHeader />
            <SkeletonCard height="h-96" />
        </div>
    );

    return (
        <div className="space-y-6">
            <div>
                <h2 className="text-base font-display font-bold text-(--base-09) mb-1">Placement</h2>
                <p className="text-sm text-(--base-07)">
                    Global defaults for how new servers are placed on nodes. Per-node overrides live on the Nodes tab.
                </p>
            </div>

            <div className="card p-5 space-y-5">
                <div>
                    <h3 className="mono-label mb-3">Overcommit Defaults</h3>
                    <p className="text-xs text-(--base-06) mb-4">
                        How much allocation is allowed relative to physical capacity.
                        <span className="font-mono"> 100%</span> = no overcommit, <span className="font-mono">200%</span> = double-book.
                        Applied to new nodes — existing nodes keep their per-node value.
                    </p>

                    <div className="grid grid-cols-2 gap-4">
                        <div className="flex flex-col gap-[5px]">
                            <label className="input-label">CPU Overcommit</label>
                            <div className="relative w-32">
                                <input
                                    type="number"
                                    step={10}
                                    min={10}
                                    value={Math.round(settings.cpuOvercommitDefault * 100)}
                                    onChange={e => setSettings(s => ({ ...s, cpuOvercommitDefault: Math.max(0.1, Number(e.target.value) / 100) }))}
                                    className="input-field input-mono w-full pr-7 text-center [appearance:textfield] [&::-webkit-outer-spin-button]:appearance-none [&::-webkit-inner-spin-button]:appearance-none"
                                />
                                <span className="absolute right-3 top-[9px] text-(--base-06) font-mono text-sm pointer-events-none">%</span>
                            </div>
                            <p className="text-xs text-(--base-06)">CPU is time-shared — 200% is conservative.</p>
                        </div>
                        <div className="flex flex-col gap-[5px]">
                            <label className="input-label">RAM Overcommit</label>
                            <div className="relative w-32">
                                <input
                                    type="number"
                                    step={10}
                                    min={10}
                                    value={Math.round(settings.ramOvercommitDefault * 100)}
                                    onChange={e => setSettings(s => ({ ...s, ramOvercommitDefault: Math.max(0.1, Number(e.target.value) / 100) }))}
                                    className="input-field input-mono w-full pr-7 text-center [appearance:textfield] [&::-webkit-outer-spin-button]:appearance-none [&::-webkit-inner-spin-button]:appearance-none"
                                />
                                <span className="absolute right-3 top-[9px] text-(--base-06) font-mono text-sm pointer-events-none">%</span>
                            </div>
                            <p className="text-xs text-(--base-06)">RAM can&apos;t be reclaimed — 100% is safest, increase only for idle-heavy workloads.</p>
                        </div>
                    </div>
                </div>

                <div className="border-t border-(--base-03) pt-5">
                    <h3 className="mono-label mb-3">Disk Buffer</h3>
                    <div className="flex flex-col gap-[5px]">
                        <label className="input-label">Disk Buffer</label>
                        <div className="relative w-32">
                            <input
                                type="number"
                                min={0}
                                value={settings.diskBufferGb}
                                onChange={e => setSettings(s => ({ ...s, diskBufferGb: Number(e.target.value) }))}
                                className="input-field input-mono w-full pr-9 text-center [appearance:textfield] [&::-webkit-outer-spin-button]:appearance-none [&::-webkit-inner-spin-button]:appearance-none"
                            />
                            <span className="absolute right-3 top-[9px] text-(--base-06) font-mono text-sm pointer-events-none">GB</span>
                        </div>
                        <p className="text-xs text-(--base-06)">Reserved free space the scheduler must leave on every node, on top of the server&apos;s own disk request.</p>
                    </div>
                </div>

                <div className="border-t border-(--base-03) pt-5">
                    <h3 className="mono-label mb-3">Process Limit (anti fork-bomb)</h3>
                    <div className="flex flex-col gap-[5px]">
                        <label className="input-label">Max processes / threads per server</label>
                        <div className="relative w-32">
                            <input
                                type="number"
                                min={0}
                                value={settings.pidsLimit}
                                onChange={e => setSettings(s => ({ ...s, pidsLimit: Math.max(0, Number(e.target.value)) }))}
                                className="input-field input-mono w-full text-center [appearance:textfield] [&::-webkit-outer-spin-button]:appearance-none [&::-webkit-inner-spin-button]:appearance-none"
                            />
                        </div>
                        <p className="text-xs text-(--base-06)">
                            Caps the cgroup PID count per server container (applies on next (re)start). <span className="font-mono">0</span> = unlimited. This counts <strong>threads too</strong>, so set it generously (e.g. <span className="font-mono">4096</span>) — a value too low will throttle or crash heavy modded servers.
                        </p>
                    </div>
                </div>

                <div className="border-t border-(--base-03) pt-5">
                    <h3 className="mono-label mb-3">Disk I/O Fair-Share</h3>
                    <div className="flex flex-col gap-[5px]">
                        <label className="input-label">blkio weight per server (10–1000, 0 = off)</label>
                        <div className="relative w-32">
                            <input
                                type="number"
                                min={0}
                                max={1000}
                                value={settings.ioWeight}
                                onChange={e => setSettings(s => ({ ...s, ioWeight: Math.max(0, Math.min(1000, Number(e.target.value))) }))}
                                className="input-field input-mono w-full text-center [appearance:textfield] [&::-webkit-outer-spin-button]:appearance-none [&::-webkit-inner-spin-button]:appearance-none"
                            />
                        </div>
                        <p className="text-xs text-(--base-06)">
                            Relative disk-I/O priority between server containers (applies on next (re)start), so one noisy server can&apos;t starve the others. <span className="font-mono">0</span> = off; valid range <span className="font-mono">10–1000</span>. This is a <strong>relative weight, not a hard cap</strong>, and only takes effect with a blkio-weight I/O scheduler (BFQ/CFQ); it is a harmless no-op otherwise.
                        </p>
                    </div>
                </div>

                <div className="border-t border-(--base-03) pt-5">
                    <h3 className="mono-label mb-3">Host-Port Allocation</h3>
                    <p className="text-xs text-(--base-06) mb-4">
                        How nodes pick a host port from their range when a server is created. Applies cluster-wide;
                        per-node port range stays in the node&apos;s <code className="font-mono bg-(--base-03) px-1.5 py-0.5 rounded text-(--base-08)">PORT_RANGE</code> env.
                    </p>
                    <div className="grid grid-cols-2 gap-4">
                        <div className="flex flex-col gap-[5px]">
                            <label className="input-label">Port Mode</label>
                            <select
                                value={settings.portMode}
                                onChange={e => setSettings(s => ({ ...s, portMode: e.target.value as 'sequential' | 'random' }))}
                                className="input-field w-44"
                            >
                                <option value="sequential">Sequential</option>
                                <option value="random">Random</option>
                            </select>
                            <p className="text-xs text-(--base-06)">
                                Sequential = first-free port. Random = uniform over the range — useful when you want to obscure server adjacency.
                            </p>
                        </div>
                        <div className="flex flex-col gap-[5px]">
                            <label className="input-label">Default Container Port</label>
                            <input
                                type="number"
                                min={1}
                                max={65535}
                                value={settings.containerPort}
                                onChange={e => setSettings(s => ({ ...s, containerPort: Number(e.target.value) }))}
                                className="input-field input-mono w-28 text-center [appearance:textfield] [&::-webkit-outer-spin-button]:appearance-none [&::-webkit-inner-spin-button]:appearance-none"
                            />
                            <p className="text-xs text-(--base-06)">MC default is 25565. Change only when your server image binds elsewhere.</p>
                        </div>
                    </div>
                </div>

                <div className="border-t border-(--base-03) pt-5">
                    <h3 className="mono-label mb-3">Auto Rebalance (Phase B)</h3>
                    <div className="flex items-center justify-between">
                        <div>
                            <p className="text-sm text-(--base-09)">Migrate idle auto-move servers off overloaded nodes</p>
                            <p className="text-xs text-(--base-06)">Background worker checks node load every 60s. Migration logic ships in Phase B.</p>
                        </div>
                        <button
                            type="button"
                            role="switch"
                            aria-checked={settings.rebalanceEnabled}
                            onClick={() => setSettings(s => ({ ...s, rebalanceEnabled: !s.rebalanceEnabled }))}
                            className={`toggle-track ${settings.rebalanceEnabled ? 'toggle-track-on' : 'toggle-track-off'}`}
                        >
                            <span className={`toggle-knob ${settings.rebalanceEnabled ? 'toggle-knob-on' : 'toggle-knob-off'}`} />
                        </button>
                    </div>
                    {settings.rebalanceEnabled && (
                        <div className="flex flex-col gap-[5px] mt-3">
                            <label className="input-label">Overload Threshold</label>
                            <div className="flex items-center gap-3">
                                <input
                                    type="range"
                                    min={50}
                                    max={100}
                                    value={settings.rebalanceThreshold}
                                    onChange={e => setSettings(s => ({ ...s, rebalanceThreshold: Number(e.target.value) }))}
                                    className="flex-1"
                                />
                                <span className="font-mono text-sm text-(--base-09) w-12 text-right">{settings.rebalanceThreshold}%</span>
                            </div>
                            <p className="text-xs text-(--base-06)">A node is &quot;overloaded&quot; when its allocation exceeds this percentage of its overcommit-adjusted capacity.</p>
                        </div>
                    )}
                </div>
            </div>

            <div className="flex gap-3 pt-2">
                <button onClick={handleSave} disabled={saving} className="btn btn-primary disabled:opacity-40">
                    <Save size={14} />
                    {saving ? 'Saving...' : 'Save Settings'}
                </button>
            </div>
        </div>
    );
}

// ─────────────────────────────────────────────
// Node Admission (P0b-5): join-mode + IP allowlist gate on a node's FIRST
// registration. Always available regardless of BYON — this guards the
// existing auto-discovery flow too.
// ─────────────────────────────────────────────

function AdmissionCard({ showToast }: { showToast: (msg: string, ok?: boolean) => void }) {
    const [joinMode, setJoinMode] = useState<string>('open');
    const [ipMode, setIpMode] = useState<string>('allow');
    const [cidrs, setCidrs] = useState<AdmissionCIDR[]>([]);
    const [loading, setLoading] = useState(true);
    const [saving, setSaving] = useState(false);
    const [newCidr, setNewCidr] = useState('');
    const [newLabel, setNewLabel] = useState('');

    const load = async () => {
        const res = await getNodeAdmission();
        if (res.success) {
            setJoinMode(res.joinMode || 'open');
            setIpMode(res.ipMode || 'allow');
            setCidrs(res.cidrs || []);
        }
        setLoading(false);
    };
    useEffect(() => { load(); }, []);

    const save = async (nextJoin: string, nextIp: string) => {
        setSaving(true);
        const res = await updateNodeAdmission({ joinMode: nextJoin, ipMode: nextIp });
        setSaving(false);
        if (res.success) {
            setJoinMode(nextJoin);
            setIpMode(nextIp);
            showToast('Admission updated.');
        } else {
            showToast(res.message || 'Save failed.', false);
        }
    };

    const addCidr = async () => {
        const cidr = newCidr.trim();
        if (!cidr) return;
        const res = await addAdmissionCIDR({ cidr, label: newLabel.trim() });
        if (res.success) {
            setNewCidr(''); setNewLabel('');
            showToast('CIDR added.');
            load();
        } else {
            showToast(res.message || 'Invalid CIDR.', false);
        }
    };

    const removeCidr = async (id: string) => {
        const res = await deleteAdmissionCIDR(id);
        if (res.success) { showToast('CIDR removed.'); load(); }
        else showToast(res.message || 'Remove failed.', false);
    };

    if (loading) return <SkeletonCard />;

    const selectCls = "w-full bg-(--base-02) border border-(--base-04) rounded-md px-3 py-2 text-sm text-(--base-09) focus:border-(--accent) focus:shadow-[0_0_0_3px_rgba(112,72,200,0.15)] outline-none";
    const inputCls = "flex-1 bg-(--base-02) border border-(--base-04) rounded-md px-3 py-2 text-sm text-(--base-09) focus:border-(--accent) focus:shadow-[0_0_0_3px_rgba(112,72,200,0.15)] outline-none";

    return (
        <div className="card p-6 space-y-5">
            <div className="flex items-center gap-2">
                <ShieldCheck size={18} className="text-(--accent-light)" />
                <h3 className="text-base font-display font-bold text-(--base-09)">Node Admission</h3>
            </div>
            <p className="text-sm text-(--base-06)">
                Gates a node&apos;s FIRST registration (network + join), before any auth. Known nodes always reconnect. Inert defaults: join <code className="font-mono text-xs">open</code>, IP <code className="font-mono text-xs">allow</code>.
            </p>

            <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
                <div className="space-y-1">
                    <label className="mono-label">Join mode</label>
                    <select value={joinMode} disabled={saving} onChange={e => save(e.target.value, ipMode)} className={selectCls}>
                        <option value="open">open — any new node may join</option>
                        <option value="one-shot">one-shot — admit exactly one, then disable</option>
                        <option value="disabled">disabled — reject all new nodes</option>
                    </select>
                </div>
                <div className="space-y-1">
                    <label className="mono-label">IP mode</label>
                    <select value={ipMode} disabled={saving} onChange={e => save(joinMode, e.target.value)} className={selectCls}>
                        <option value="allow">allow — CIDR list advisory (any IP)</option>
                        <option value="deny">deny — only IPs inside a listed CIDR</option>
                    </select>
                </div>
            </div>

            <div className="space-y-2">
                <label className="mono-label">Allowed CIDRs {ipMode === 'allow' && <span className="text-(--base-06) normal-case">(advisory while IP mode is allow)</span>}</label>
                {cidrs.length === 0 ? (
                    <div className="text-sm text-(--base-06)">No CIDRs configured.</div>
                ) : (
                    <div className="space-y-2">
                        {cidrs.map(c => (
                            <div key={c.id} className="flex items-center justify-between bg-(--base-02) border border-(--base-04) rounded-md px-3 py-2">
                                <div className="flex items-center gap-3">
                                    <code className="font-mono text-xs text-(--base-09)">{c.cidr}</code>
                                    {c.label && <span className="text-xs text-(--base-06)">{c.label}</span>}
                                </div>
                                <button onClick={() => removeCidr(c.id)} className="text-(--base-06) hover:text-(--error-light) transition-colors" title="Remove CIDR">
                                    <Trash2 size={14} />
                                </button>
                            </div>
                        ))}
                    </div>
                )}
                <div className="flex items-center gap-2 pt-1">
                    <input value={newCidr} onChange={e => setNewCidr(e.target.value)} placeholder="10.0.0.0/24" className={`${inputCls} font-mono`} />
                    <input value={newLabel} onChange={e => setNewLabel(e.target.value)} placeholder="label (optional)" className={inputCls} />
                    <button onClick={addCidr} className="btn btn-secondary btn-sm shrink-0">
                        <Plus size={14} /> Add
                    </button>
                </div>
            </div>
        </div>
    );
}

// ─────────────────────────────────────────────
// Enroll tokens (P0b-5): single-use tokens authorizing a NEW node's first
// pairing. BYON-gated on the backend (byonActive) — this section renders
// visibly disabled with a "Requires BYON" note when the byon flag is off.
// ─────────────────────────────────────────────

function EnrollTokensSection({ showToast }: { showToast: (msg: string, ok?: boolean) => void }) {
    const [tokens, setTokens] = useState<NodeEnrollToken[]>([]);
    const [label, setLabel] = useState('');
    const [expiresDays, setExpiresDays] = useState(7);
    const [minting, setMinting] = useState(false);
    const [revealed, setRevealed] = useState<{ token: string } | null>(null);
    // The enroll-token backend is BYON-gated (byonActive). Read the byon flag and
    // render this section visibly disabled when BYON is off.
    const [byonEnabled, setByonEnabled] = useState(false);

    const load = async () => {
        const res = await listEnrollTokens();
        if (res.success) setTokens(res.tokens || []);
    };
    useEffect(() => {
        getSystemFeatures().then(res => {
            const on = !!(res.success && res.features && res.features.byon);
            setByonEnabled(on);
            if (on) load();
        });
    }, []);

    const mint = async () => {
        setMinting(true);
        const res = await mintEnrollToken({ label: label.trim(), expiresDays });
        setMinting(false);
        if (res.success && res.token) {
            setRevealed({ token: res.token });
            setLabel('');
            load();
        } else {
            showToast(res.message || 'Mint failed.', false);
        }
    };

    const revoke = async (id: string) => {
        const res = await revokeEnrollToken(id);
        if (res.success) { showToast('Token revoked.'); load(); }
        else showToast(res.message || 'Revoke failed.', false);
    };

    const fmt = (s?: string) => (s ? new Date(s).toLocaleString() : '—');
    const inputCls = "w-full bg-(--base-02) border border-(--base-04) rounded-md px-3 py-2 text-sm text-(--base-09) focus:border-(--accent) focus:shadow-[0_0_0_3px_rgba(112,72,200,0.15)] outline-none";

    return (
        <div className="card p-6 space-y-5">
            <div className="flex items-center gap-2">
                <Ticket size={18} className="text-(--accent-light)" />
                <h3 className="text-base font-display font-bold text-(--base-09)">Enroll tokens</h3>
            </div>
            <p className="text-sm text-(--base-06)">
                Single-use tokens that authorize a NEW node&apos;s first pairing. Shown once at mint time; start the node with <code className="font-mono text-xs">NODE_ENROLL_TOKEN</code> set.
            </p>

            {!byonEnabled ? (
                <p className="text-sm text-(--base-06)">Requires BYON — enable it in Settings → Features to mint node enroll tokens.</p>
            ) : (
              <>
            <div className="flex items-end gap-2">
                <div className="flex-1 space-y-1">
                    <label className="mono-label">Label</label>
                    <input value={label} onChange={e => setLabel(e.target.value)} placeholder="e.g. home-pc" className={inputCls} />
                </div>
                <div className="w-28 space-y-1">
                    <label className="mono-label">Expires (days)</label>
                    <input type="number" min={1} value={expiresDays} onChange={e => setExpiresDays(parseInt(e.target.value) || 7)} className={inputCls} />
                </div>
                <button onClick={mint} disabled={minting} className="btn btn-primary btn-sm shrink-0 disabled:opacity-40">
                    <Plus size={14} /> {minting ? 'Minting…' : 'Mint token'}
                </button>
            </div>

            {tokens.length === 0 ? (
                <div className="text-sm text-(--base-06)">No enroll tokens.</div>
            ) : (
                <div className="space-y-2">
                    {tokens.map(t => (
                        <div key={t.id} className="flex items-center justify-between bg-(--base-02) border border-(--base-04) rounded-md px-3 py-2">
                            <div className="flex flex-col">
                                <span className="text-sm text-(--base-09)">{t.label || <span className="text-(--base-06)">(no label)</span>}</span>
                                <span className="text-xs text-(--base-06)">
                                    created {fmt(t.createdAt)} · expires {fmt(t.expiresAt)} · {t.consumedAt ? `consumed ${fmt(t.consumedAt)}` : 'unused'}
                                </span>
                            </div>
                            <button onClick={() => revoke(t.id)} className="text-(--base-06) hover:text-(--error-light) transition-colors" title="Revoke token">
                                <Trash2 size={14} />
                            </button>
                        </div>
                    ))}
                </div>
            )}
              </>
            )}

            {revealed && (
                <div className="modal-overlay animate-fade-in" onClick={() => setRevealed(null)}>
                    <div className="modal-panel max-w-lg" onClick={e => e.stopPropagation()}>
                        <div className="modal-header"><h3 className="modal-title text-(--accent-light)">Enroll token</h3></div>
                        <div className="modal-body space-y-3">
                            <p className="text-sm text-(--base-07)">Shown once. Copy it now — it cannot be retrieved later.</p>
                            <div className="space-y-1">
                                <label className="mono-label">NODE_ENROLL_TOKEN</label>
                                <pre className="p-3 rounded-md bg-(--base-02) border border-(--base-04) font-mono text-xs whitespace-pre-wrap break-all">{revealed.token}</pre>
                                <button onClick={() => { navigator.clipboard.writeText(revealed.token); showToast('Enroll token copied.', true); }} className="btn btn-secondary btn-sm">
                                    <Copy size={12} /> Copy token
                                </button>
                            </div>
                        </div>
                        <div className="modal-footer"><button onClick={() => setRevealed(null)} className="btn btn-primary">Done</button></div>
                    </div>
                </div>
            )}
        </div>
    );
}
