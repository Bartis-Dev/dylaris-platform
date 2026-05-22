"use client";

import React, { useState, useEffect, useRef } from 'react';
import {
    getGatewaySettings, saveGatewaySettings, GatewaySettings, HosterDomain, HosterValidation,
    getRoutingMode, saveRoutingMode, getRoutingMigrationStatus,
    bulkDeleteRoutesBySuffix,
    RoutingMode, FileAccessMode,
} from '@/lib/api';
import { RefreshCw, Save, CircleCheck, CircleAlert, Router, AlertTriangle, EyeOff, Radio, Globe, Plus, Trash2, X, Shield } from 'lucide-react';
import LoadingState from '@/components/LoadingState';
import Spinner from '@/components/Spinner';
import { useUnsavedChanges, useUnsavedChangesState, UnsavedDialog } from '@/components/settings/UnsavedChanges';

// ─────────────────────────────────────────────
// Beam settings
// ─────────────────────────────────────────────

interface BeamSettings {
    relayAddress: string;
    bwLimit: number;
    enabled: boolean;
    downloadLink: string;
}

const BW_UNITS = [
    { label: 'MB/s', multiplier: 1024 * 1024 },
    { label: 'Gbit/s', multiplier: 125 * 1024 * 1024 },
];

function bwToDisplay(bytesPerSec: number): { value: number; unit: string } {
    if (bytesPerSec === 0) return { value: 0, unit: 'MB/s' };
    if (bytesPerSec >= 125 * 1024 * 1024 && bytesPerSec % (125 * 1024 * 1024) === 0) {
        return { value: bytesPerSec / (125 * 1024 * 1024), unit: 'Gbit/s' };
    }
    return { value: Math.round(bytesPerSec / (1024 * 1024)), unit: 'MB/s' };
}

function displayToBw(value: number, unit: string): number {
    if (value === 0) return 0;
    const u = BW_UNITS.find(u => u.label === unit);
    return value * (u?.multiplier || 1);
}

async function getBeamSettings(): Promise<{ success: boolean; settings?: BeamSettings }> {
    try {
        const token = localStorage.getItem('authToken') || localStorage.getItem('token');
        const API_URL = process.env.NEXT_PUBLIC_API_URL || 'http://localhost:25500/api';
        const res = await fetch(`${API_URL}/settings/beam`, {
            headers: { Authorization: `Bearer ${token}` },
        });
        return await res.json();
    } catch {
        return { success: false };
    }
}

async function saveBeamSettings(settings: BeamSettings): Promise<{ success: boolean; message?: string }> {
    try {
        const token = localStorage.getItem('authToken') || localStorage.getItem('token');
        const API_URL = process.env.NEXT_PUBLIC_API_URL || 'http://localhost:25500/api';
        const res = await fetch(`${API_URL}/settings/beam`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${token}` },
            body: JSON.stringify(settings),
        });
        return await res.json();
    } catch {
        return { success: false, message: 'Network error' };
    }
}

// ─────────────────────────────────────────────
// Gateway settings
// ─────────────────────────────────────────────

type LimitKey = 'global' | 'userDefault' | 'perServer' | 'portMc' | 'portHttps' | 'portHttp';
type ModeOption<T extends string> = { value: T; label: string; desc: string };
type SubTab = 'gateway' | 'beam' | 'xdp';

const ROUTING_OPTIONS: ModeOption<RoutingMode>[] = [
    { value: 'ip_port', label: 'IP : Port', desc: 'Direct host port binding — players connect via Node IP + port' },
    { value: 'both', label: 'Both', desc: 'Allow both IP:Port and Gateway routes simultaneously' },
    { value: 'gateway', label: 'Gateway', desc: 'Route-only mode — no host ports exposed, traffic via Gate → Link' },
];

const FILE_OPTIONS: ModeOption<FileAccessMode>[] = [
    { value: 'sftp', label: 'SFTP', desc: 'Users access server files via SFTP on the Node IP' },
    { value: 'both', label: 'Both', desc: 'Allow both SFTP and Beam file access' },
    { value: 'beam', label: 'Beam', desc: 'File access only via Beam relay — no direct Node IP needed' },
];

const NAV_ITEMS: { id: SubTab; label: string; icon: React.ElementType }[] = [
    { id: 'gateway', label: 'Gateway', icon: Router },
    { id: 'beam', label: 'Beam', icon: Radio },
    { id: 'xdp', label: 'DDoS Protection', icon: Shield },
];

// ─────────────────────────────────────────────
// Beam panel
// ─────────────────────────────────────────────

// Editable Beam fields compared for dirty detection. bwLimit is the resolved
// bytes value (derived from the unlimited toggle + value/unit inputs).
interface BeamEditableSnapshot {
    relayAddress: string;
    downloadLink: string;
    enabled: boolean;
    bwLimit: number;
}

function beamSnapshot(s: BeamSettings, unlimited: boolean, bwValue: number, bwUnit: string): BeamEditableSnapshot {
    return {
        relayAddress: s.relayAddress,
        downloadLink: s.downloadLink,
        enabled: s.enabled,
        bwLimit: unlimited ? 0 : displayToBw(bwValue, bwUnit),
    };
}

function BeamPanel({ showToast }: { showToast: (msg: string, ok?: boolean) => void }) {
    const [settings, setSettings] = useState<BeamSettings>({ relayAddress: '', bwLimit: 0, enabled: true, downloadLink: '' });
    const [loading, setLoading] = useState(true);
    const [saving, setSaving] = useState(false);
    const [bwValue, setBwValue] = useState(0);
    const [bwUnit, setBwUnit] = useState('MB/s');
    const [unlimited, setUnlimited] = useState(true);

    // Snapshot of last-saved editable fields for dirty detection.
    const snapshotRef = useRef<BeamEditableSnapshot | null>(null);

    useEffect(() => {
        getBeamSettings().then(res => {
            if (res.success && res.settings) {
                setSettings(res.settings);
                const isUnlimited = res.settings.bwLimit === 0;
                setUnlimited(isUnlimited);
                let loadedValue = 0;
                let loadedUnit = 'MB/s';
                if (!isUnlimited) {
                    const d = bwToDisplay(res.settings.bwLimit);
                    loadedValue = d.value;
                    loadedUnit = d.unit;
                    setBwValue(d.value);
                    setBwUnit(d.unit);
                }
                snapshotRef.current = beamSnapshot(res.settings, isUnlimited, loadedValue, loadedUnit);
            }
            setLoading(false);
        });
    }, []);

    const handleSave = async () => {
        setSaving(true);
        const bwLimit = unlimited ? 0 : displayToBw(bwValue, bwUnit);
        const res = await saveBeamSettings({ ...settings, bwLimit });
        showToast(res.success ? 'Beam settings saved.' : (res.message || 'Save failed.'), res.success);
        if (res.success) {
            snapshotRef.current = beamSnapshot(settings, unlimited, bwValue, bwUnit);
        }
        setSaving(false);
    };

    const handleDiscard = () => {
        const snap = snapshotRef.current;
        if (!snap) return;
        setSettings(s => ({ ...s, relayAddress: snap.relayAddress, downloadLink: snap.downloadLink, enabled: snap.enabled }));
        const isUnlimited = snap.bwLimit === 0;
        setUnlimited(isUnlimited);
        if (!isUnlimited) {
            const d = bwToDisplay(snap.bwLimit);
            setBwValue(d.value);
            setBwUnit(d.unit);
        } else {
            setBwValue(0);
            setBwUnit('MB/s');
        }
    };

    const dirty =
        snapshotRef.current !== null &&
        JSON.stringify(beamSnapshot(settings, unlimited, bwValue, bwUnit)) !== JSON.stringify(snapshotRef.current);

    useUnsavedChanges({ dirty, save: handleSave, discard: handleDiscard, saving });

    if (loading) return <LoadingState />;

    return (
        <div className="space-y-6">
            <div>
                <h2 className="text-base font-display font-bold text-(--base-09) mb-1">Beam File Transfer</h2>
                <p className="text-sm text-(--base-07)">Configure the Beam desktop file transfer service. Users can download the Beam app to manage server files directly.</p>
            </div>

            <div className="card p-5 space-y-4">
                <h3 className="text-sm font-display font-semibold text-(--accent-light) mb-2">General</h3>
                <div className="flex items-center justify-between">
                    <div>
                        <label className="input-label">Beam Enabled</label>
                        <p className="text-xs text-(--base-06) mt-0.5">Allow users to connect via Beam desktop app</p>
                    </div>
                    <button
                        onClick={() => setSettings(s => ({ ...s, enabled: !s.enabled }))}
                        className={`toggle-track ${settings.enabled ? 'toggle-track-on' : 'toggle-track-off'}`}
                        role="switch"
                        aria-checked={settings.enabled}
                    >
                        <span className={`toggle-knob ${settings.enabled ? 'toggle-knob-on' : 'toggle-knob-off'}`} />
                    </button>
                </div>
                <div className="flex flex-col gap-[5px]">
                    <label className="input-label">Relay Address</label>
                    <p className="text-xs text-(--base-06) mb-1">Public address of the Beam Relay service (e.g. beam.example.com:9095)</p>
                    <input
                        type="text"
                        value={settings.relayAddress}
                        onChange={e => setSettings(s => ({ ...s, relayAddress: e.target.value }))}
                        placeholder="beam.example.com:9095"
                        className="input-field"
                    />
                </div>
                <div className="flex flex-col gap-[5px]">
                    <label className="input-label">Beam Download Link</label>
                    <input
                        type="text"
                        value={settings.downloadLink}
                        onChange={e => setSettings(s => ({ ...s, downloadLink: e.target.value }))}
                        placeholder="https://releases.example.com/beam/latest"
                        className="input-field"
                    />
                    <p className="text-xs text-(--base-06) mt-0.5">When set, a download button appears in the Files tab for all users.</p>
                </div>
            </div>

            <div className="card p-5 space-y-4">
                <h3 className="text-sm font-display font-semibold text-(--base-08) mb-2">Bandwidth Limit</h3>
                <p className="text-xs text-(--base-06)">Global bandwidth cap shared across all Beam transfers on each node. Fair sharing is automatic.</p>
                <div className="flex items-center justify-between">
                    <label className="input-label">Unlimited</label>
                    <button
                        onClick={() => setUnlimited(!unlimited)}
                        className={`toggle-track ${unlimited ? 'toggle-track-on' : 'toggle-track-off'}`}
                        role="switch"
                        aria-checked={unlimited}
                    >
                        <span className={`toggle-knob ${unlimited ? 'toggle-knob-on' : 'toggle-knob-off'}`} />
                    </button>
                </div>
                {!unlimited && (
                    <div className="flex gap-2">
                        <input
                            type="number"
                            min={1}
                            value={bwValue}
                            onChange={e => setBwValue(Math.max(1, parseInt(e.target.value) || 1))}
                            className="input-field w-28 text-right"
                        />
                        <select value={bwUnit} onChange={e => setBwUnit(e.target.value)} className="input-field w-28">
                            {BW_UNITS.map(u => <option key={u.label} value={u.label}>{u.label}</option>)}
                        </select>
                    </div>
                )}
            </div>
        </div>
    );
}

// ─────────────────────────────────────────────
// Gateway panel
// ─────────────────────────────────────────────

function GatewayPanel({ showToast }: { showToast: (msg: string, ok?: boolean) => void }) {
    const [settings, setSettings] = useState<GatewaySettings>({
        limits: {
            global: -1, userDefault: -1, perServer: -1,
            portMc: -1, portMcEnabled: true,
            portHttps: -1, portHttpsEnabled: true,
            portHttp: -1, portHttpEnabled: false,
        },
        hosterDomains: [],
        customDomainsEnabled: false,
        cnameTarget: '',
    });
    const [loading, setLoading] = useState(true);
    const [saving, setSaving] = useState(false);

    const [routingMode, setRoutingMode] = useState<RoutingMode>('ip_port');
    const [fileMode, setFileMode] = useState<FileAccessMode>('sftp');
    const [origRoutingMode, setOrigRoutingMode] = useState<RoutingMode>('ip_port');
    const [origFileMode, setOrigFileMode] = useState<FileAccessMode>('sftp');
    const [confirmModal, setConfirmModal] = useState(false);
    const [savingRouting, setSavingRouting] = useState(false);
    const [migration, setMigration] = useState<{ running: boolean; total: number; done: number; failed: number } | null>(null);
    const pollRef = useRef<ReturnType<typeof setInterval> | null>(null);

    // Snapshot of last-saved gateway settings for dirty detection. The routing
    // mode has its own explicit "Apply Routing" flow and is tracked separately.
    const snapshotRef = useRef<GatewaySettings | null>(null);

    useEffect(() => {
        Promise.all([getGatewaySettings(), getRoutingMode()]).then(([gwRes, rmRes]) => {
            if (gwRes.success && gwRes.settings) {
                const loaded: GatewaySettings = {
                    ...gwRes.settings,
                    hosterDomains: gwRes.settings.hosterDomains || [],
                    customDomainsEnabled: !!gwRes.settings.customDomainsEnabled,
                    cnameTarget: gwRes.settings.cnameTarget || '',
                };
                setSettings(loaded);
                snapshotRef.current = loaded;
            }
            if (rmRes.success) {
                const m: RoutingMode = rmRes.mode || 'ip_port';
                const f: FileAccessMode = rmRes.fileMode || 'sftp';
                setRoutingMode(m); setOrigRoutingMode(m);
                setFileMode(f); setOrigFileMode(f);
            }
        }).finally(() => setLoading(false));
    }, []);

    const startPolling = () => {
        if (pollRef.current) return;
        pollRef.current = setInterval(async () => {
            const res = await getRoutingMigrationStatus();
            if (res.success) {
                setMigration({ running: res.running, total: res.total, done: res.done, failed: res.failed });
                if (!res.running) { clearInterval(pollRef.current!); pollRef.current = null; }
            }
        }, 3000);
    };

    const handleSaveRouting = async () => {
        setSavingRouting(true);
        setConfirmModal(false);
        const res = await saveRoutingMode({ mode: routingMode, fileMode });
        if (res.success) {
            setOrigRoutingMode(routingMode);
            setOrigFileMode(fileMode);
            showToast(`Routing mode saved.${res.serversQueued > 0 ? ` Redeploying ${res.serversQueued} servers...` : ''}`);
            if (res.serversQueued > 0) {
                setMigration({ running: true, total: res.serversQueued, done: 0, failed: 0 });
                startPolling();
            }
        } else {
            showToast(res.message || 'Failed to save routing mode.', false);
        }
        setSavingRouting(false);
    };

    const handleSave = async () => {
        setSaving(true);
        const res = await saveGatewaySettings(settings);
        showToast(res.success ? 'Gateway settings saved.' : (res.message || 'Save failed.'), res.success);
        if (res.success) snapshotRef.current = settings;
        setSaving(false);
    };

    const handleDiscard = () => {
        if (snapshotRef.current) setSettings(snapshotRef.current);
    };

    const setLimit = (key: LimitKey, value: number) =>
        setSettings(prev => ({ ...prev, limits: { ...prev.limits, [key]: value } }));
    const isUnlimited = (key: LimitKey) => settings.limits[key] === -1;
    const toggleUnlimited = (key: LimitKey) => setLimit(key, isUnlimited(key) ? 0 : -1);
    const routingChanged = routingMode !== origRoutingMode || fileMode !== origFileMode;

    const addHoster = () => {
        setSettings(prev => ({
            ...prev,
            hosterDomains: [...prev.hosterDomains, { domain: '', validation: 'alphanumeric' }],
        }));
    };

    // Removing a hoster domain — when the entry has a real domain value we
    // route through a confirm modal with an optional cascade-delete-routes
    // checkbox. Blank/unsaved entries (just-added rows) skip the prompt.
    const [removeTarget, setRemoveTarget] = useState<{ idx: number; domain: string } | null>(null);
    const [removeCascade, setRemoveCascade] = useState(false);
    const [removeCountdown, setRemoveCountdown] = useState(0);
    const [removeBusy, setRemoveBusy] = useState(false);

    useEffect(() => {
        if (!removeTarget || !removeCascade) {
            setRemoveCountdown(0);
            return;
        }
        setRemoveCountdown(5);
        const id = setInterval(() => {
            setRemoveCountdown(c => (c <= 1 ? 0 : c - 1));
        }, 1000);
        return () => clearInterval(id);
    }, [removeTarget, removeCascade]);

    const removeHoster = (idx: number) => {
        const target = settings.hosterDomains[idx];
        if (!target || !target.domain.trim()) {
            // Brand-new empty row — drop immediately, no confirm.
            setSettings(prev => ({
                ...prev,
                hosterDomains: prev.hosterDomains.filter((_, i) => i !== idx),
            }));
            return;
        }
        setRemoveCascade(false);
        setRemoveCountdown(0);
        setRemoveTarget({ idx, domain: target.domain });
    };

    const confirmRemoveHoster = async () => {
        if (!removeTarget) return;
        if (removeCascade && removeCountdown > 0) return; // timer still running
        setRemoveBusy(true);
        let cascadeMessage = '';
        if (removeCascade) {
            const res = await bulkDeleteRoutesBySuffix(removeTarget.domain);
            if (res.success) {
                cascadeMessage = ` (${res.deleted} route${res.deleted !== 1 ? 's' : ''} deleted)`;
            } else {
                cascadeMessage = ' (route cascade failed)';
            }
        }
        setSettings(prev => ({
            ...prev,
            hosterDomains: prev.hosterDomains.filter((_, i) => i !== removeTarget.idx),
        }));
        setRemoveBusy(false);
        setRemoveTarget(null);
        showToast(`Removed ${removeTarget.domain}${cascadeMessage}. Click Save to persist.`);
    };
    const updateHoster = (idx: number, patch: Partial<HosterDomain>) => {
        setSettings(prev => ({
            ...prev,
            hosterDomains: prev.hosterDomains.map((h, i) => i === idx ? { ...h, ...patch } : h),
        }));
    };

    const VALIDATION_LABELS: Record<HosterValidation, string> = {
        letters: 'Letters only',
        alphanumeric: 'Letters + numbers',
        dns: 'Full DNS (a-z, 0-9, -)',
    };

    const allocationFields: { key: LimitKey; label: string; desc: string }[] = [
        { key: 'global', label: 'Global Max Routes', desc: 'Total routes across all users and servers' },
        { key: 'userDefault', label: 'Default Per-User Max', desc: 'Default limit for users without a custom override' },
        { key: 'perServer', label: 'Per-Server Max', desc: 'Max routes per individual MC server' },
    ];

    const dirty =
        snapshotRef.current !== null &&
        JSON.stringify(settings) !== JSON.stringify(snapshotRef.current);

    useUnsavedChanges({ dirty, save: handleSave, discard: handleDiscard, saving });

    if (loading) return <LoadingState />;

    return (
        <div className="space-y-6">
            <div>
                <h2 className="text-base font-display font-bold text-(--base-09) mb-1">Gateway Configuration</h2>
                <p className="text-sm text-(--base-07)">Manage gateway routing, link defaults and route limits for gates and links.</p>
            </div>

            {/* Routing Mode */}
            <div className="card p-5 space-y-5">
                <div className="flex items-center gap-3">
                    <div className="w-9 h-9 rounded-md bg-(--base-03) flex items-center justify-center">
                        <Router size={18} className="text-(--accent-light)" />
                    </div>
                    <div>
                        <div className="font-medium text-sm text-(--base-09)">Traffic Routing</div>
                        <div className="text-xs text-(--base-06)">How game traffic and file access are routed to servers</div>
                    </div>
                </div>

                <div>
                    <h3 className="mono-label mb-3">Game Traffic</h3>
                    <div className="grid grid-cols-3 gap-2">
                        {ROUTING_OPTIONS.map(opt => (
                            <button key={opt.value} type="button" onClick={() => setRoutingMode(opt.value)}
                                className={`p-3 rounded-md border text-left transition-colors ${routingMode === opt.value ? 'border-(--accent) bg-(--accent)/10' : 'border-(--base-03) bg-(--base-02) hover:border-(--base-05)'}`}>
                                <div className={`text-sm font-medium ${routingMode === opt.value ? 'text-(--accent-light)' : 'text-(--base-09)'}`}>{opt.label}</div>
                                <div className="text-xs text-(--base-06) mt-0.5">{opt.desc}</div>
                            </button>
                        ))}
                    </div>
                </div>

                <div>
                    <h3 className="mono-label mb-3">File Access</h3>
                    <div className="grid grid-cols-3 gap-2">
                        {FILE_OPTIONS.map(opt => (
                            <button key={opt.value} type="button" onClick={() => setFileMode(opt.value)}
                                className={`p-3 rounded-md border text-left transition-colors ${fileMode === opt.value ? 'border-(--accent) bg-(--accent)/10' : 'border-(--base-03) bg-(--base-02) hover:border-(--base-05)'}`}>
                                <div className={`text-sm font-medium ${fileMode === opt.value ? 'text-(--accent-light)' : 'text-(--base-09)'}`}>{opt.label}</div>
                                <div className="text-xs text-(--base-06) mt-0.5">{opt.desc}</div>
                            </button>
                        ))}
                    </div>
                </div>

                <div className={`flex items-start gap-3 p-3 rounded-md border ${routingMode === 'gateway' && fileMode === 'beam' ? 'border-(--success)/30 bg-(--success)/5' : 'border-(--base-04) bg-(--base-02)'}`}>
                    <EyeOff size={15} className={`mt-0.5 shrink-0 ${routingMode === 'gateway' && fileMode === 'beam' ? 'text-(--success-light)' : 'text-(--base-06)'}`} />
                    <p className="text-xs text-(--base-07)">
                        The public Node IP is only fully hidden when both <span className="text-(--base-09) font-medium">Game Traffic</span> is set to <span className="text-(--base-09) font-medium">Gateway</span> and <span className="text-(--base-09) font-medium">File Access</span> is set to <span className="text-(--base-09) font-medium">Beam</span>.
                        {routingMode === 'gateway' && fileMode === 'beam' && <span className="text-(--success-light) font-medium ml-1">Node IPs are currently fully hidden.</span>}
                    </p>
                </div>

                {migration && (
                    <div className="p-3 rounded-md bg-(--base-02) border border-(--base-04) space-y-2">
                        <div className="flex items-center justify-between">
                            <div className="flex items-center gap-2">
                                {migration.running ? <Spinner size="xs" className="text-(--accent-light)" /> : <RefreshCw size={13} className="text-(--accent-light)" />}
                                <span className="text-xs text-(--base-09)">{migration.running ? 'Redeploying servers...' : 'Redeploy complete'}</span>
                            </div>
                            <span className="font-mono text-xs text-(--base-06)">{migration.done} / {migration.total} done{migration.failed > 0 ? ` · ${migration.failed} failed` : ''}</span>
                        </div>
                        <div className="h-1.5 rounded-full bg-(--base-03) overflow-hidden">
                            <div className={`h-full rounded-full transition-all duration-300 ${migration.failed > 0 ? 'bg-(--error-light)' : 'bg-(--accent)'}`}
                                style={{ width: migration.total > 0 ? `${Math.round((migration.done / migration.total) * 100)}%` : '0%' }} />
                        </div>
                    </div>
                )}

                <div className="flex items-center gap-3 pt-1 border-t border-(--base-03)">
                    <button onClick={() => setConfirmModal(true)} disabled={!routingChanged || savingRouting}
                        className="btn btn-primary disabled:opacity-40">
                        <Save size={14} />
                        {savingRouting ? 'Applying...' : 'Apply Routing'}
                    </button>
                    {routingChanged && (
                        <span className="text-xs text-(--base-06) flex items-center gap-1.5">
                            <AlertTriangle size={12} className="text-(--warning-light)" />
                            Changing routing mode will trigger a server redeploy
                        </span>
                    )}
                </div>
            </div>

            {/* Hoster Domains + Custom Domains */}
            <div className="card p-5 space-y-5">
                <div className="flex items-center gap-3">
                    <div className="w-9 h-9 rounded-md bg-(--base-03) flex items-center justify-center">
                        <Globe size={18} className="text-(--accent-light)" />
                    </div>
                    <div>
                        <div className="font-medium text-sm text-(--base-09)">Domains</div>
                        <div className="text-xs text-(--base-06)">Hoster domains users pick from + optional custom-domain support</div>
                    </div>
                </div>

                <div>
                    <h3 className="mono-label mb-3">Hoster Domains</h3>
                    <p className="text-xs text-(--base-06) mb-3">
                        Users only enter a subdomain — these base domains appear as a dropdown next to the input. Pick which characters are allowed in the subdomain per domain.
                    </p>
                    <div className="space-y-2">
                        {settings.hosterDomains.length === 0 && (
                            <p className="text-xs text-(--base-05) italic px-3 py-3 rounded-md bg-(--base-02)">
                                No hoster domains configured. Add one to enable the subdomain picker.
                            </p>
                        )}
                        {settings.hosterDomains.map((hd, idx) => (
                            <div key={idx} className="flex items-center gap-2 p-2 rounded-md bg-(--base-02)">
                                <input
                                    type="text"
                                    value={hd.domain}
                                    onChange={e => updateHoster(idx, { domain: e.target.value.toLowerCase().trim() })}
                                    placeholder="dylaris.com"
                                    className="input-field input-mono flex-1 text-sm"
                                />
                                <select
                                    value={hd.validation}
                                    onChange={e => updateHoster(idx, { validation: e.target.value as HosterValidation })}
                                    className="input-field text-sm w-52"
                                >
                                    {(Object.keys(VALIDATION_LABELS) as HosterValidation[]).map(k => (
                                        <option key={k} value={k}>{VALIDATION_LABELS[k]}</option>
                                    ))}
                                </select>
                                <button
                                    type="button"
                                    onClick={() => removeHoster(idx)}
                                    className="text-(--base-06) hover:text-(--error-light) transition-colors p-2"
                                    title="Remove"
                                >
                                    <Trash2 size={14} />
                                </button>
                            </div>
                        ))}
                    </div>
                    <button
                        type="button"
                        onClick={addHoster}
                        className="btn btn-secondary btn-sm mt-3"
                    >
                        <Plus size={12} /> Add domain
                    </button>
                </div>

                <div className="border-t border-(--base-03) pt-5 space-y-4">
                    <div className="flex items-center justify-between gap-4">
                        <div>
                            <h3 className="mono-label">Custom Domains</h3>
                            <p className="text-xs text-(--base-06) mt-1">Allow users to bring their own domain via a CNAME record.</p>
                        </div>
                        <button
                            type="button"
                            role="switch"
                            aria-checked={settings.customDomainsEnabled}
                            onClick={() => setSettings(prev => ({ ...prev, customDomainsEnabled: !prev.customDomainsEnabled }))}
                            className={`toggle-track ${settings.customDomainsEnabled ? 'toggle-track-on' : 'toggle-track-off'}`}
                        >
                            <span className={`toggle-knob ${settings.customDomainsEnabled ? 'toggle-knob-on' : 'toggle-knob-off'}`} />
                        </button>
                    </div>
                    {settings.customDomainsEnabled && (
                        <div className="flex flex-col gap-[5px]">
                            <label className="input-label">CNAME Target</label>
                            <input
                                type="text"
                                value={settings.cnameTarget}
                                onChange={e => setSettings(prev => ({ ...prev, cnameTarget: e.target.value }))}
                                placeholder="route.dylaris.com"
                                className="input-field input-mono text-sm"
                            />
                            <p className="text-xs text-(--base-06)">Shown to users as the CNAME record they need to point their domain at.</p>
                        </div>
                    )}
                </div>
            </div>

            {/* Route Limits */}
            <div className="card p-5 space-y-5">
                <div className="flex items-center gap-3">
                    <div className="w-9 h-9 rounded-md bg-(--base-03) flex items-center justify-center">
                        <Router size={18} className="text-(--accent-light)" />
                    </div>
                    <div>
                        <div className="font-medium text-sm text-(--base-09)">Route Limits</div>
                        <div className="text-xs text-(--base-06)">Control maximum route allocations and port access</div>
                    </div>
                </div>

                <div>
                    <h3 className="mono-label mb-3">Route Allocation</h3>
                    <div className="space-y-3">
                        {allocationFields.map(({ key, label, desc }) => (
                            <div key={key} className="flex items-center justify-between gap-4 p-3 rounded-md bg-(--base-02)">
                                <div className="flex-1 min-w-0">
                                    <p className="text-sm text-(--base-09)">{label}</p>
                                    <p className="text-xs text-(--base-06)">{desc}</p>
                                </div>
                                <div className="flex items-center gap-3 shrink-0">
                                    {!isUnlimited(key) && (
                                        <input type="number" min={0} value={settings.limits[key]}
                                            onChange={e => setLimit(key, Number(e.target.value))}
                                            className="input-mono w-20 text-center" />
                                    )}
                                    <label className="flex items-center gap-1.5 cursor-pointer select-none">
                                        <button type="button" role="switch" aria-checked={isUnlimited(key)} onClick={() => toggleUnlimited(key)}
                                            className={`toggle-track ${isUnlimited(key) ? 'toggle-track-on' : 'toggle-track-off'}`}>
                                            <span className={`toggle-knob ${isUnlimited(key) ? 'toggle-knob-on' : 'toggle-knob-off'}`} />
                                        </button>
                                        <span className="text-[10px] font-mono uppercase text-(--base-06)">Unlimited</span>
                                    </label>
                                </div>
                            </div>
                        ))}
                    </div>
                    <p className="text-xs text-(--base-05) mt-2">0 = zero routes allowed. Per-user overrides can be set in user settings.</p>
                </div>

                <div className="border-t border-(--base-03) pt-5">
                    <h3 className="mono-label mb-3">Port Configuration</h3>
                    <div className="flex items-start gap-2 p-3 rounded-md bg-(--warning)/5 border border-(--warning)/20 mb-3">
                        <AlertTriangle size={13} className="text-(--warning-light) mt-0.5 shrink-0" />
                        <p className="text-xs text-(--base-07)">Use HTTP (port 80) only when behind a reverse proxy (nginx, Traefik, Caddy). Exposing HTTP directly is insecure.</p>
                    </div>
                    <div className="space-y-3">
                        {/* MC Port */}
                        <div className={`p-3 rounded-md bg-(--base-02) ${!settings.limits.portMcEnabled ? 'opacity-60' : ''}`}>
                            <div className="flex items-center justify-between mb-2">
                                <div className="flex items-center gap-2">
                                    <span className="text-sm font-semibold text-(--base-09)">Minecraft</span>
                                    <span className="font-mono text-[10px] px-1.5 py-0.5 rounded bg-(--base-03) text-(--base-06)">25565</span>
                                </div>
                                <button type="button" role="switch" aria-checked={settings.limits.portMcEnabled}
                                    onClick={() => setSettings(prev => ({ ...prev, limits: { ...prev.limits, portMcEnabled: !prev.limits.portMcEnabled } }))}
                                    className={`toggle-track ${settings.limits.portMcEnabled ? 'toggle-track-on' : 'toggle-track-off'}`}>
                                    <span className={`toggle-knob ${settings.limits.portMcEnabled ? 'toggle-knob-on' : 'toggle-knob-off'}`} />
                                </button>
                            </div>
                            {settings.limits.portMcEnabled && (
                                <div className="flex items-center justify-between mt-2 pt-2 border-t border-(--base-03)">
                                    <span className="text-xs text-(--base-06)">Max routes on this port</span>
                                    <div className="flex items-center gap-3">
                                        {!isUnlimited('portMc') && (
                                            <input type="number" min={0} value={settings.limits.portMc}
                                                onChange={e => setLimit('portMc', Number(e.target.value))}
                                                className="input-mono w-20 text-center" />
                                        )}
                                        <label className="flex items-center gap-1.5 cursor-pointer select-none">
                                            <button type="button" role="switch" aria-checked={isUnlimited('portMc')} onClick={() => toggleUnlimited('portMc')}
                                                className={`toggle-track ${isUnlimited('portMc') ? 'toggle-track-on' : 'toggle-track-off'}`}>
                                                <span className={`toggle-knob ${isUnlimited('portMc') ? 'toggle-knob-on' : 'toggle-knob-off'}`} />
                                            </button>
                                            <span className="text-[10px] font-mono uppercase text-(--base-06)">Unlimited</span>
                                        </label>
                                    </div>
                                </div>
                            )}
                        </div>

                        {/* HTTP Port */}
                        <div className={`p-3 rounded-md bg-(--base-02) ${!settings.limits.portHttpEnabled ? 'opacity-60' : ''}`}>
                            <div className="flex items-center justify-between mb-2">
                                <div className="flex items-center gap-2">
                                    <span className="text-sm font-semibold text-(--base-09)">HTTP</span>
                                    <span className="font-mono text-[10px] px-1.5 py-0.5 rounded bg-(--base-03) text-(--base-06)">80</span>
                                </div>
                                <button type="button" role="switch" aria-checked={settings.limits.portHttpEnabled}
                                    onClick={() => setSettings(prev => ({ ...prev, limits: { ...prev.limits, portHttpEnabled: !prev.limits.portHttpEnabled } }))}
                                    className={`toggle-track ${settings.limits.portHttpEnabled ? 'toggle-track-on' : 'toggle-track-off'}`}>
                                    <span className={`toggle-knob ${settings.limits.portHttpEnabled ? 'toggle-knob-on' : 'toggle-knob-off'}`} />
                                </button>
                            </div>
                            {settings.limits.portHttpEnabled && (
                                <div className="flex items-center justify-between mt-2 pt-2 border-t border-(--base-03)">
                                    <span className="text-xs text-(--base-06)">Max routes on this port</span>
                                    <div className="flex items-center gap-3">
                                        {!isUnlimited('portHttp') && (
                                            <input type="number" min={0} value={settings.limits.portHttp}
                                                onChange={e => setLimit('portHttp', Number(e.target.value))}
                                                className="input-mono w-20 text-center" />
                                        )}
                                        <label className="flex items-center gap-1.5 cursor-pointer select-none">
                                            <button type="button" role="switch" aria-checked={isUnlimited('portHttp')} onClick={() => toggleUnlimited('portHttp')}
                                                className={`toggle-track ${isUnlimited('portHttp') ? 'toggle-track-on' : 'toggle-track-off'}`}>
                                                <span className={`toggle-knob ${isUnlimited('portHttp') ? 'toggle-knob-on' : 'toggle-knob-off'}`} />
                                            </button>
                                            <span className="text-[10px] font-mono uppercase text-(--base-06)">Unlimited</span>
                                        </label>
                                    </div>
                                </div>
                            )}
                        </div>

                        {/* HTTPS Port */}
                        <div className={`p-3 rounded-md bg-(--base-02) ${!settings.limits.portHttpsEnabled ? 'opacity-60' : ''}`}>
                            <div className="flex items-center justify-between mb-2">
                                <div className="flex items-center gap-2">
                                    <span className="text-sm font-semibold text-(--base-09)">HTTPS</span>
                                    <span className="font-mono text-[10px] px-1.5 py-0.5 rounded bg-(--base-03) text-(--base-06)">443</span>
                                </div>
                                <button type="button" role="switch" aria-checked={settings.limits.portHttpsEnabled}
                                    onClick={() => setSettings(prev => ({ ...prev, limits: { ...prev.limits, portHttpsEnabled: !prev.limits.portHttpsEnabled } }))}
                                    className={`toggle-track ${settings.limits.portHttpsEnabled ? 'toggle-track-on' : 'toggle-track-off'}`}>
                                    <span className={`toggle-knob ${settings.limits.portHttpsEnabled ? 'toggle-knob-on' : 'toggle-knob-off'}`} />
                                </button>
                            </div>
                            {settings.limits.portHttpsEnabled && (
                                <div className="flex items-center justify-between mt-2 pt-2 border-t border-(--base-03)">
                                    <span className="text-xs text-(--base-06)">Max routes on this port</span>
                                    <div className="flex items-center gap-3">
                                        {!isUnlimited('portHttps') && (
                                            <input type="number" min={0} value={settings.limits.portHttps}
                                                onChange={e => setLimit('portHttps', Number(e.target.value))}
                                                className="input-mono w-20 text-center" />
                                        )}
                                        <label className="flex items-center gap-1.5 cursor-pointer select-none">
                                            <button type="button" role="switch" aria-checked={isUnlimited('portHttps')} onClick={() => toggleUnlimited('portHttps')}
                                                className={`toggle-track ${isUnlimited('portHttps') ? 'toggle-track-on' : 'toggle-track-off'}`}>
                                                <span className={`toggle-knob ${isUnlimited('portHttps') ? 'toggle-knob-on' : 'toggle-knob-off'}`} />
                                            </button>
                                            <span className="text-[10px] font-mono uppercase text-(--base-06)">Unlimited</span>
                                        </label>
                                    </div>
                                </div>
                            )}
                        </div>
                    </div>
                    <p className="text-xs text-(--base-05) mt-2">Disabled ports block all route creation on that port.</p>
                </div>
            </div>

            {/* Routing confirmation modal */}
            {confirmModal && (
                <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 backdrop-blur-sm">
                    <div className="card p-6 max-w-md w-full mx-4 space-y-4">
                        <div className="flex items-start gap-3">
                            <div className="w-9 h-9 rounded-md bg-(--warning)/10 border border-(--warning)/20 flex items-center justify-center shrink-0">
                                <AlertTriangle size={18} className="text-(--warning-light)" />
                            </div>
                            <div>
                                <h3 className="font-display font-bold text-(--base-09) text-base">Confirm Routing Change</h3>
                                <p className="text-xs text-(--base-06) mt-0.5">This action will redeploy all active servers</p>
                            </div>
                        </div>
                        <div className="space-y-2 text-sm text-(--base-07)">
                            {routingMode === 'gateway' && origRoutingMode !== 'gateway' && (
                                <p>Switching to <span className="text-(--base-09) font-medium">Gateway</span> mode: all host port bindings will be removed and servers will be redeployed without exposed ports.</p>
                            )}
                            {routingMode !== 'gateway' && origRoutingMode === 'gateway' && (
                                <p>Switching away from <span className="text-(--base-09) font-medium">Gateway</span> mode: new host ports will be assigned to all servers during redeploy.</p>
                            )}
                            {routingMode === 'both' && origRoutingMode !== 'both' && origRoutingMode !== 'gateway' && (
                                <p>Switching to <span className="text-(--base-09) font-medium">Both</span> mode: servers will keep or receive host ports while also supporting gateway routes.</p>
                            )}
                            {fileMode !== origFileMode && (
                                <p>File access mode is changing to <span className="text-(--base-09) font-medium">{FILE_OPTIONS.find(o => o.value === fileMode)?.label}</span>.</p>
                            )}
                            <p className="text-(--base-06) text-xs pt-1">Servers are redeployed in batches of 4 with 15s between batches. Each container has a 60s timeout before a force-kill is issued.</p>
                        </div>
                        <div className="flex gap-3 pt-2">
                            <button onClick={handleSaveRouting} className="btn btn-primary flex-1">Confirm & Apply</button>
                            <button onClick={() => setConfirmModal(false)} className="btn px-5 py-2 text-sm flex-1">Cancel</button>
                        </div>
                    </div>
                </div>
            )}

            {/* Hoster-domain remove confirmation (optional cascade) */}
            {removeTarget && (
                <div className="modal-overlay animate-fade-in">
                    <div className="modal-panel w-full max-w-md">
                        <div className="modal-header flex items-center justify-between">
                            <h3 className="modal-title flex items-center gap-2 text-(--error-light)">
                                <AlertTriangle size={18} />
                                Remove Hoster Domain
                            </h3>
                            <button onClick={() => setRemoveTarget(null)} className="text-(--base-06) hover:text-(--error-light)" disabled={removeBusy}>
                                <X size={18} />
                            </button>
                        </div>
                        <div className="modal-body space-y-3">
                            <p className="text-sm text-(--base-08)">
                                Remove <code className="font-mono text-(--base-09) bg-(--base-02) px-1.5 py-0.5 rounded">{removeTarget.domain}</code> from the hoster-domain list?
                            </p>
                            <p className="text-xs text-(--base-06)">
                                Users will no longer be able to register new subdomains under it. Existing routes pointing to this domain stay active by default.
                            </p>

                            <label className="flex items-start gap-2 cursor-pointer pt-1">
                                <input
                                    type="checkbox"
                                    checked={removeCascade}
                                    onChange={e => setRemoveCascade(e.target.checked)}
                                    className="mt-0.5"
                                />
                                <span className="text-sm text-(--base-08)">
                                    Also delete all related routes ending in <code className="font-mono">.{removeTarget.domain}</code>
                                </span>
                            </label>
                            {removeCascade && (
                                <div className="alert alert-error text-xs">
                                    <AlertTriangle size={14} className="shrink-0 mt-0.5" />
                                    <span>
                                        Cascade is permanent. Every server route under this domain will be deleted from the gateway. The confirm button is locked for {removeCountdown}s so you can re-read this.
                                    </span>
                                </div>
                            )}
                        </div>
                        <div className="modal-footer">
                            <button
                                onClick={() => setRemoveTarget(null)}
                                disabled={removeBusy}
                                className="btn btn-secondary"
                            >
                                Cancel
                            </button>
                            <button
                                onClick={confirmRemoveHoster}
                                disabled={removeBusy || (removeCascade && removeCountdown > 0)}
                                className={`btn disabled:opacity-40 ${removeCascade ? 'btn-danger' : 'btn-primary'}`}
                            >
                                {removeBusy
                                    ? <><Spinner size="xs" /> Removing…</>
                                    : (removeCascade && removeCountdown > 0)
                                        ? `Confirm cascade (${removeCountdown}s)`
                                        : removeCascade
                                            ? <><Trash2 size={13} /> Remove + delete routes</>
                                            : 'Remove domain'}
                            </button>
                        </div>
                    </div>
                </div>
            )}
        </div>
    );
}

// ─────────────────────────────────────────────
// XDP / DDoS Protection panel
// ─────────────────────────────────────────────

interface XDPConfig {
    enabled: boolean;
    host_mode: boolean;
    interface?: string;
    protected_ports: string;
    rate_limit: number;
    rate_window_ms: number;
    ban_duration_min: number;
    mc_malformed_limit: number;
    mc_malformed_window_min: number;
    mc_invalid_host_limit: number;
    mc_invalid_host_window_min: number;
    mc_ban_duration_min: number;
    whitelist?: string;
}

const XDP_DEFAULTS: XDPConfig = {
    enabled: false,
    host_mode: false,
    interface: '',
    protected_ports: '25565,80,443',
    rate_limit: 1000,
    rate_window_ms: 1000,
    ban_duration_min: 30,
    mc_malformed_limit: 20,
    mc_malformed_window_min: 2,
    mc_invalid_host_limit: 100,
    mc_invalid_host_window_min: 2,
    mc_ban_duration_min: 5,
    whitelist: '',
};

async function getXDPConfig(): Promise<{ success: boolean; config?: XDPConfig; present?: boolean }> {
    try {
        const token = localStorage.getItem('authToken') || localStorage.getItem('token');
        const API_URL = process.env.NEXT_PUBLIC_API_URL || 'http://localhost:25500/api';
        const res = await fetch(`${API_URL}/admin/xdp/config`, {
            headers: { Authorization: `Bearer ${token}` },
        });
        return await res.json();
    } catch {
        return { success: false };
    }
}

async function saveXDPConfig(cfg: XDPConfig): Promise<{ success: boolean; message?: string }> {
    try {
        const token = localStorage.getItem('authToken') || localStorage.getItem('token');
        const API_URL = process.env.NEXT_PUBLIC_API_URL || 'http://localhost:25500/api';
        const res = await fetch(`${API_URL}/admin/xdp/config`, {
            method: 'PUT',
            headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${token}` },
            body: JSON.stringify(cfg),
        });
        return await res.json();
    } catch {
        return { success: false, message: 'Network error' };
    }
}

function XDPPanel({ showToast }: { showToast: (msg: string, ok?: boolean) => void }) {
    const [cfg, setCfg] = useState<XDPConfig>(XDP_DEFAULTS);
    const [loading, setLoading] = useState(true);
    const [saving, setSaving] = useState(false);
    const [present, setPresent] = useState(false);

    // Snapshot of last-saved config for dirty detection.
    const snapshotRef = useRef<XDPConfig | null>(null);

    useEffect(() => {
        getXDPConfig().then(res => {
            if (res.success && res.config) {
                setCfg(res.config);
                setPresent(!!res.present);
                snapshotRef.current = res.config;
            }
            setLoading(false);
        });
    }, []);

    const set = <K extends keyof XDPConfig>(key: K, value: XDPConfig[K]) =>
        setCfg(s => ({ ...s, [key]: value }));

    const handleSave = async () => {
        setSaving(true);
        const res = await saveXDPConfig(cfg);
        showToast(res.success ? 'XDP config saved — Edges reconcile within 30s.' : (res.message || 'Save failed.'), res.success);
        if (res.success) {
            setPresent(true);
            snapshotRef.current = cfg;
        }
        setSaving(false);
    };

    const handleDiscard = () => {
        if (snapshotRef.current) setCfg(snapshotRef.current);
    };

    const dirty =
        snapshotRef.current !== null &&
        JSON.stringify(cfg) !== JSON.stringify(snapshotRef.current);

    useUnsavedChanges({ dirty, save: handleSave, discard: handleDiscard, saving });

    if (loading) return <LoadingState />;

    return (
        <div className="space-y-6">
            <div>
                <h2 className="text-base font-display font-bold text-(--base-09) mb-1">DDoS Protection (XDP / eBPF)</h2>
                <p className="text-sm text-(--base-07)">
                    Kernel-level packet filtering on every Edge replica. Changes here are written to Redis and picked up
                    by all Edges within ~30 seconds — saving triggers an automatic sidecar recreate (≈1-3s downtime of the
                    XDP shield, the Edge proxy itself stays up).
                </p>
                {!present && (
                    <div className="mt-3 flex items-start gap-2 p-3 rounded-md bg-(--accent)/5 border border-(--accent-border)/40 text-xs text-(--base-08)">
                        <AlertTriangle size={14} className="text-(--accent-light) mt-0.5 shrink-0" />
                        <span>No XDP config in Redis yet — these are the package defaults. Save once to commit them.</span>
                    </div>
                )}
            </div>

            <div className="card p-5 space-y-4">
                <h3 className="text-sm font-display font-semibold text-(--accent-light) mb-2">General</h3>

                <div className="flex items-center justify-between">
                    <div>
                        <label className="input-label">XDP Enabled</label>
                        <p className="text-xs text-(--base-06) mt-0.5">Master switch — disables packet filtering when off</p>
                    </div>
                    <button
                        onClick={() => set('enabled', !cfg.enabled)}
                        className={`toggle-track ${cfg.enabled ? 'toggle-track-on' : 'toggle-track-off'}`}
                        role="switch"
                        aria-checked={cfg.enabled}
                    >
                        <span className={`toggle-knob ${cfg.enabled ? 'toggle-knob-on' : 'toggle-knob-off'}`} />
                    </button>
                </div>

                <div className="flex items-center justify-between">
                    <div>
                        <label className="input-label">Host Sidecar Mode</label>
                        <p className="text-xs text-(--base-06) mt-0.5">
                            Run XDP as a separate host-networked container (recommended for production).
                            Toggling this requires an Edge restart.
                        </p>
                    </div>
                    <button
                        onClick={() => set('host_mode', !cfg.host_mode)}
                        className={`toggle-track ${cfg.host_mode ? 'toggle-track-on' : 'toggle-track-off'}`}
                        role="switch"
                        aria-checked={cfg.host_mode}
                    >
                        <span className={`toggle-knob ${cfg.host_mode ? 'toggle-knob-on' : 'toggle-knob-off'}`} />
                    </button>
                </div>

                <div className="flex flex-col gap-[5px]">
                    <label className="input-label">Network Interface</label>
                    <p className="text-xs text-(--base-06) mb-1">Empty = auto-detect all non-loopback IPv4 interfaces (sidecar mode only)</p>
                    <input
                        type="text"
                        value={cfg.interface || ''}
                        onChange={e => set('interface', e.target.value)}
                        placeholder="eth0"
                        className="input-field w-48"
                    />
                </div>

                <div className="flex flex-col gap-[5px]">
                    <label className="input-label">Protected Ports</label>
                    <p className="text-xs text-(--base-06) mb-1">Comma-separated list of ports to apply rate-limiting to</p>
                    <input
                        type="text"
                        value={cfg.protected_ports}
                        onChange={e => set('protected_ports', e.target.value)}
                        placeholder="25565,80,443"
                        className="input-field w-72"
                    />
                </div>
            </div>

            <div className="card p-5 space-y-4">
                <h3 className="text-sm font-display font-semibold text-(--base-08) mb-2">Per-IP Rate Limiting</h3>
                <p className="text-xs text-(--base-06)">Drops packets from any source IP that exceeds the threshold within the window. Tripped IPs are blocked for the ban duration.</p>

                <div className="grid grid-cols-3 gap-4">
                    <div className="flex flex-col gap-[5px]">
                        <label className="input-label">Packets / Window</label>
                        <input type="number" min={1} max={1000000} value={cfg.rate_limit}
                            onChange={e => set('rate_limit', Math.max(1, parseInt(e.target.value) || 1))}
                            className="input-field" />
                    </div>
                    <div className="flex flex-col gap-[5px]">
                        <label className="input-label">Window (ms)</label>
                        <input type="number" min={100} value={cfg.rate_window_ms}
                            onChange={e => set('rate_window_ms', Math.max(100, parseInt(e.target.value) || 1000))}
                            className="input-field" />
                    </div>
                    <div className="flex flex-col gap-[5px]">
                        <label className="input-label">Ban Duration (min)</label>
                        <input type="number" min={1} value={cfg.ban_duration_min}
                            onChange={e => set('ban_duration_min', Math.max(1, parseInt(e.target.value) || 1))}
                            className="input-field" />
                    </div>
                </div>
            </div>

            <div className="card p-5 space-y-4">
                <h3 className="text-sm font-display font-semibold text-(--base-08) mb-2">Minecraft-Aware Filters</h3>
                <p className="text-xs text-(--base-06)">Protocol-level filters using the Edge's MC handshake parser. Catches scanners and malformed-packet floods that pass plain rate-limiting.</p>

                <div className="grid grid-cols-2 gap-4">
                    <div className="flex flex-col gap-[5px]">
                        <label className="input-label">Malformed / Window</label>
                        <input type="number" min={1} value={cfg.mc_malformed_limit}
                            onChange={e => set('mc_malformed_limit', Math.max(1, parseInt(e.target.value) || 1))}
                            className="input-field" />
                    </div>
                    <div className="flex flex-col gap-[5px]">
                        <label className="input-label">Window (min)</label>
                        <input type="number" min={1} value={cfg.mc_malformed_window_min}
                            onChange={e => set('mc_malformed_window_min', Math.max(1, parseInt(e.target.value) || 1))}
                            className="input-field" />
                    </div>
                    <div className="flex flex-col gap-[5px]">
                        <label className="input-label">Invalid Host / Window</label>
                        <input type="number" min={1} value={cfg.mc_invalid_host_limit}
                            onChange={e => set('mc_invalid_host_limit', Math.max(1, parseInt(e.target.value) || 1))}
                            className="input-field" />
                    </div>
                    <div className="flex flex-col gap-[5px]">
                        <label className="input-label">Window (min)</label>
                        <input type="number" min={1} value={cfg.mc_invalid_host_window_min}
                            onChange={e => set('mc_invalid_host_window_min', Math.max(1, parseInt(e.target.value) || 1))}
                            className="input-field" />
                    </div>
                    <div className="flex flex-col gap-[5px] col-span-2">
                        <label className="input-label">MC Ban Duration (min)</label>
                        <input type="number" min={1} value={cfg.mc_ban_duration_min}
                            onChange={e => set('mc_ban_duration_min', Math.max(1, parseInt(e.target.value) || 1))}
                            className="input-field w-48" />
                    </div>
                </div>
            </div>

            <div className="card p-5 space-y-3">
                <h3 className="text-sm font-display font-semibold text-(--base-08)">Whitelist</h3>
                <p className="text-xs text-(--base-06)">
                    IPs and CIDRs that bypass all checks. Comma-separated. Useful for monitoring services or known crawlers.
                </p>
                <textarea
                    value={cfg.whitelist || ''}
                    onChange={e => set('whitelist', e.target.value)}
                    placeholder="1.2.3.4, 10.0.0.0/8, 192.168.0.0/16"
                    rows={3}
                    className="input-field font-mono text-xs resize-y"
                />
            </div>
        </div>
    );
}

// ─────────────────────────────────────────────
// Main export: Gateway + Beam with left-nav
// ─────────────────────────────────────────────

export default function GatewayTab() {
    const [subTab, setSubTab] = useState<SubTab>('gateway');
    const [toast, setToast] = useState<{ msg: string; ok: boolean } | null>(null);

    // Sub-tab switch guard — the active panel registers its unsaved state with
    // the shared bar; we intercept sub-nav clicks when it's dirty.
    const registration = useUnsavedChangesState();
    const [pendingSubTab, setPendingSubTab] = useState<SubTab | null>(null);
    const [dialogSaving, setDialogSaving] = useState(false);

    const showToast = (msg: string, ok = true) => {
        setToast({ msg, ok });
        setTimeout(() => setToast(null), 3500);
    };

    const requestSubTab = (id: SubTab) => {
        if (id === subTab) return;
        if (registration?.dirty) {
            setPendingSubTab(id);
        } else {
            setSubTab(id);
        }
    };

    const handleDialogSave = async () => {
        if (!registration || pendingSubTab === null) return;
        setDialogSaving(true);
        try {
            await registration.save();
        } finally {
            setDialogSaving(false);
        }
        setSubTab(pendingSubTab);
        setPendingSubTab(null);
    };

    const handleDialogDiscard = () => {
        if (!registration || pendingSubTab === null) return;
        registration.discard();
        setSubTab(pendingSubTab);
        setPendingSubTab(null);
    };

    return (
        <div className="flex gap-0 h-full">
            {/* Left nav */}
            <nav className="w-44 shrink-0 border-r border-(--base-03) pr-4 flex flex-col gap-1 pt-1">
                {NAV_ITEMS.map(({ id, label, icon: Icon }) => (
                    <button
                        key={id}
                        onClick={() => requestSubTab(id)}
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

            {/* Right content */}
            <div className="flex-1 pl-6 overflow-y-auto">
                {subTab === 'gateway' && <GatewayPanel showToast={showToast} />}
                {subTab === 'beam' && <BeamPanel showToast={showToast} />}
                {subTab === 'xdp' && <XDPPanel showToast={showToast} />}
            </div>

            {/* Sub-tab switch confirm dialog */}
            {pendingSubTab !== null && registration && (
                <UnsavedDialog
                    onSave={handleDialogSave}
                    onDiscard={handleDialogDiscard}
                    onCancel={() => { setPendingSubTab(null); setDialogSaving(false); }}
                    saving={dialogSaving}
                />
            )}

            {/* Shared toast */}
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
