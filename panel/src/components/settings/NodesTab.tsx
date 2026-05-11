"use client";

import React, { useState, useEffect } from 'react';
import {
    getNodes, Node,
    getPlacementSettings, savePlacementSettings, PlacementSettings,
    setNodePlacement,
} from '@/lib/api';
import {
    Network, Server, Globe, Settings as SettingsIcon, Save, RefreshCw,
    CircleCheck, CircleAlert, Pencil, X,
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

function NodesPanel({ showToast }: { showToast: (msg: string, ok?: boolean) => void }) {
    const [nodes, setNodes] = useState<Node[]>([]);
    const [editingPlacement, setEditingPlacement] = useState<number | null>(null);

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
                                isEditing={editingPlacement === node.id}
                                onEdit={() => setEditingPlacement(node.id)}
                                onCancel={() => setEditingPlacement(null)}
                                onSaved={() => { setEditingPlacement(null); loadNodes(); showToast('Placement updated'); }}
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
    isEditing: boolean;
    onEdit: () => void;
    onCancel: () => void;
    onSaved: () => void;
    onError: (msg: string) => void;
}

function NodeCard({ node, isEditing, onEdit, onCancel, onSaved, onError }: NodeCardProps) {
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
                    {node.tags && node.tags !== 'auto-discovered' && (
                        <div className="flex flex-wrap gap-1.5">
                            {node.tags.split(',').map(tag => {
                                const t = tag.trim();
                                if (!t) return null;
                                return <span key={t} className="badge badge-accent">{t}</span>;
                            })}
                        </div>
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

            {/* Placement summary / editor */}
            <div className="mt-3 pt-3 border-t border-(--base-03) grid grid-cols-2 md:grid-cols-4 gap-3 text-xs">
                <Stat label="Total CPU" value={node.totalCpu ? `${node.totalCpu.toFixed(1)} cores` : '—'} />
                <Stat label="Total RAM" value={node.totalRamMb ? `${(node.totalRamMb / 1024).toFixed(1)} GB` : '—'} />
                <Stat
                    label="CPU Overcommit"
                    value={isEditing ? (
                        <input
                            type="number"
                            step="0.1"
                            min={0.1}
                            value={cpuRatio}
                            onChange={e => setCpuRatio(Number(e.target.value))}
                            className="input-mono w-20 text-center text-xs"
                        />
                    ) : `${(node.cpuOvercommitRatio ?? 1.0).toFixed(2)}x`}
                />
                <Stat
                    label="RAM Overcommit"
                    value={isEditing ? (
                        <input
                            type="number"
                            step="0.1"
                            min={0.1}
                            value={ramRatio}
                            onChange={e => setRamRatio(Number(e.target.value))}
                            className="input-mono w-20 text-center text-xs"
                        />
                    ) : `${(node.ramOvercommitRatio ?? 1.0).toFixed(2)}x`}
                />
            </div>

            {isEditing && (
                <div className="mt-3 flex items-center gap-2 justify-end">
                    <button onClick={onCancel} className="btn btn-secondary px-3 py-1.5 text-xs">
                        <X size={12} /> Cancel
                    </button>
                    <button onClick={handleSave} disabled={saving} className="btn btn-primary px-3 py-1.5 text-xs disabled:opacity-50">
                        <Save size={12} /> {saving ? 'Saving…' : 'Save'}
                    </button>
                </div>
            )}
        </div>
    );
}

function Stat({ label, value }: { label: string; value: React.ReactNode }) {
    return (
        <div>
            <div className="font-mono text-[10px] uppercase tracking-[0.08em] text-(--base-06)">{label}</div>
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

    if (loading) return <div className="flex items-center justify-center h-40 text-(--base-07)"><RefreshCw size={24} className="animate-spin" /></div>;

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
                    <h3 className="font-mono text-[10px] uppercase tracking-[0.08em] text-(--base-06) mb-3">Overcommit Defaults</h3>
                    <p className="text-xs text-(--base-06) mb-4">
                        Ratio of how much more allocation is allowed than physical capacity.
                        <span className="font-mono"> 1.0x</span> = no overcommit, <span className="font-mono">2.0x</span> = double-book.
                        Applied to new nodes — existing nodes keep their per-node value.
                    </p>

                    <div className="grid grid-cols-2 gap-4">
                        <div className="flex flex-col gap-[5px]">
                            <label className="input-label">CPU Overcommit</label>
                            <div className="relative">
                                <input
                                    type="number"
                                    step="0.1"
                                    min={0.1}
                                    value={settings.cpuOvercommitDefault}
                                    onChange={e => setSettings(s => ({ ...s, cpuOvercommitDefault: Number(e.target.value) }))}
                                    className="input-field w-full"
                                />
                                <span className="absolute right-3 top-[9px] text-(--base-06) font-mono text-sm pointer-events-none">x</span>
                            </div>
                            <p className="text-xs text-(--base-06)">CPU is time-shared, 2.0x is conservative.</p>
                        </div>
                        <div className="flex flex-col gap-[5px]">
                            <label className="input-label">RAM Overcommit</label>
                            <div className="relative">
                                <input
                                    type="number"
                                    step="0.1"
                                    min={0.1}
                                    value={settings.ramOvercommitDefault}
                                    onChange={e => setSettings(s => ({ ...s, ramOvercommitDefault: Number(e.target.value) }))}
                                    className="input-field w-full"
                                />
                                <span className="absolute right-3 top-[9px] text-(--base-06) font-mono text-sm pointer-events-none">x</span>
                            </div>
                            <p className="text-xs text-(--base-06)">RAM can&apos;t be reclaimed — 1.0x is safest, increase only for idle-heavy workloads.</p>
                        </div>
                    </div>
                </div>

                <div className="border-t border-(--base-03) pt-5">
                    <h3 className="font-mono text-[10px] uppercase tracking-[0.08em] text-(--base-06) mb-3">Disk Buffer</h3>
                    <div className="flex flex-col gap-[5px]">
                        <label className="input-label">Disk Buffer (GB)</label>
                        <div className="relative">
                            <input
                                type="number"
                                min={0}
                                value={settings.diskBufferGb}
                                onChange={e => setSettings(s => ({ ...s, diskBufferGb: Number(e.target.value) }))}
                                className="input-field w-32"
                            />
                            <span className="absolute right-3 top-[9px] text-(--base-06) font-mono text-sm pointer-events-none">GB</span>
                        </div>
                        <p className="text-xs text-(--base-06)">Reserved free space the scheduler must leave on every node, on top of the server&apos;s own disk request.</p>
                    </div>
                </div>

                <div className="border-t border-(--base-03) pt-5">
                    <h3 className="font-mono text-[10px] uppercase tracking-[0.08em] text-(--base-06) mb-3">Auto Rebalance (Phase B)</h3>
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
                <button onClick={handleSave} disabled={saving} className="btn btn-primary px-6 py-2 text-sm disabled:opacity-50">
                    <Save size={14} />
                    {saving ? 'Saving...' : 'Save Settings'}
                </button>
            </div>
        </div>
    );
}
