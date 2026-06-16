"use client";

import React, { useState, useEffect } from 'react';
import {
    getNodes, Node,
    getPlacementSettings, savePlacementSettings, PlacementSettings,
    setNodePlacement, configureNode, Region,
} from '@/lib/api';
import { SkeletonHeader, SkeletonCard } from '@/components/Skeleton';
import { regionLabel, regionFlag } from '@/lib/regions';
import { useAppData } from '@/lib/AppDataContext';
import {
    Network, Server, Globe, Settings as SettingsIcon, Save,
    CircleCheck, CircleAlert, Pencil, X, AlertTriangle, SlidersHorizontal,
} from 'lucide-react';

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
                                onError={msg => showToast(msg, false)}
                            />
                        ))
                    )}
                </div>
            </div>
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
    onError: (msg: string) => void;
}

function NodeCard({ node, regions, gatewayRequired, isEditing, isConfiguring, onEdit, onCancel, onSaved, onConfigure, onConfigCancel, onConfigSaved, onError }: NodeCardProps) {
    const [cpuRatio, setCpuRatio] = useState(node.cpuOvercommitRatio ?? 1.0);
    const [ramRatio, setRamRatio] = useState(node.ramOvercommitRatio ?? 1.0);
    const [saving, setSaving] = useState(false);

    useEffect(() => {
        setCpuRatio(node.cpuOvercommitRatio ?? 1.0);
        setRamRatio(node.ramOvercommitRatio ?? 1.0);
    }, [node.cpuOvercommitRatio, node.ramOvercommitRatio, isEditing]);

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
                            {node.token || node.name}
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
            <div className="mt-3 pt-3 border-t border-(--base-03) grid grid-cols-2 md:grid-cols-4 gap-3 text-xs">
                <Stat label="Total CPU" value={node.totalCpu ? `${node.totalCpu.toFixed(1)} cores` : '—'} />
                <Stat label="Total RAM" value={node.totalRamMb ? `${(node.totalRamMb / 1024).toFixed(1)} GB` : '—'} />
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
        </div>
    );
}

// NodeConfigForm lets an admin adopt an auto-discovered node by setting its
// display name, region and tags. Saving persists to the DB (PATCH
// /nodes/{id}/config); from then on the node's heartbeat env no longer
// overwrites these fields.
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
    const [region, setRegion] = useState(node.region || '');
    const [tags, setTags] = useState(node.tags && node.tags !== 'auto-discovered' ? node.tags : '');
    const [saving, setSaving] = useState(false);

    const handleSave = async () => {
        if (!region) { onError('Please select a region'); return; }
        setSaving(true);
        const res = await configureNode(node.id, { name: name.trim(), region, tags: tags.trim() });
        setSaving(false);
        if (res.success) onSaved();
        else onError(res.message || res.error || 'Save failed');
    };

    return (
        <div className="mt-3 pt-3 border-t border-(--base-03) space-y-3">
            <div className="grid grid-cols-1 md:grid-cols-3 gap-3">
                <div className="flex flex-col gap-[5px]">
                    <label className="input-label">Display Name</label>
                    <input
                        value={name}
                        onChange={e => setName(e.target.value)}
                        className="input-field text-sm"
                        placeholder={node.token}
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
                Saving adopts this node: its name, region and tags are managed here from now on and the node&apos;s env values no longer overwrite them. Keep the <code className="font-mono bg-(--base-03) px-1 py-0.5 rounded text-(--base-08)">external</code> tag if this is a home/Warp node.
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
