"use client";

import React, { useState, useEffect, useRef } from 'react';
import {
    getGatewaySettings, saveGatewaySettings, GatewaySettings,
    getRoutingMode, saveRoutingMode, getRoutingMigrationStatus,
    RoutingMode, FileAccessMode,
} from '@/lib/api';
import { RefreshCw, Save, CircleCheck, CircleAlert, Router, AlertTriangle, EyeOff, Radio } from 'lucide-react';

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
type SubTab = 'gateway' | 'beam';

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
];

// ─────────────────────────────────────────────
// Beam panel
// ─────────────────────────────────────────────

function BeamPanel({ showToast }: { showToast: (msg: string, ok?: boolean) => void }) {
    const [settings, setSettings] = useState<BeamSettings>({ relayAddress: '', bwLimit: 0, enabled: true, downloadLink: '' });
    const [loading, setLoading] = useState(true);
    const [saving, setSaving] = useState(false);
    const [bwValue, setBwValue] = useState(0);
    const [bwUnit, setBwUnit] = useState('MB/s');
    const [unlimited, setUnlimited] = useState(true);

    useEffect(() => {
        getBeamSettings().then(res => {
            if (res.success && res.settings) {
                setSettings(res.settings);
                const isUnlimited = res.settings.bwLimit === 0;
                setUnlimited(isUnlimited);
                if (!isUnlimited) {
                    const d = bwToDisplay(res.settings.bwLimit);
                    setBwValue(d.value);
                    setBwUnit(d.unit);
                }
            }
            setLoading(false);
        });
    }, []);

    const handleSave = async () => {
        setSaving(true);
        const bwLimit = unlimited ? 0 : displayToBw(bwValue, bwUnit);
        const res = await saveBeamSettings({ ...settings, bwLimit });
        showToast(res.success ? 'Beam settings saved.' : (res.message || 'Save failed.'), res.success);
        setSaving(false);
    };

    if (loading) return <div className="flex items-center justify-center h-40 text-(--base-07)"><RefreshCw size={24} className="animate-spin" /></div>;

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

            <div className="flex gap-3 pt-2">
                <button onClick={handleSave} disabled={saving} className="btn btn-primary px-6 py-2 text-sm disabled:opacity-50">
                    <Save size={14} />
                    {saving ? 'Saving...' : 'Save Settings'}
                </button>
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

    useEffect(() => {
        Promise.all([getGatewaySettings(), getRoutingMode()]).then(([gwRes, rmRes]) => {
            if (gwRes.success && gwRes.settings) {
                setSettings(gwRes.settings);
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
        setSaving(false);
    };

    const setLimit = (key: LimitKey, value: number) =>
        setSettings(prev => ({ ...prev, limits: { ...prev.limits, [key]: value } }));
    const isUnlimited = (key: LimitKey) => settings.limits[key] === -1;
    const toggleUnlimited = (key: LimitKey) => setLimit(key, isUnlimited(key) ? 0 : -1);
    const routingChanged = routingMode !== origRoutingMode || fileMode !== origFileMode;

    const allocationFields: { key: LimitKey; label: string; desc: string }[] = [
        { key: 'global', label: 'Global Max Routes', desc: 'Total routes across all users and servers' },
        { key: 'userDefault', label: 'Default Per-User Max', desc: 'Default limit for users without a custom override' },
        { key: 'perServer', label: 'Per-Server Max', desc: 'Max routes per individual MC server' },
    ];

    if (loading) return <div className="flex items-center justify-center h-40 text-(--base-07)"><RefreshCw size={24} className="animate-spin" /></div>;

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
                    <h3 className="font-mono text-[10px] uppercase tracking-[0.08em] text-(--base-06) mb-3">Game Traffic</h3>
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
                    <h3 className="font-mono text-[10px] uppercase tracking-[0.08em] text-(--base-06) mb-3">File Access</h3>
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
                                <RefreshCw size={13} className={`${migration.running ? 'animate-spin' : ''} text-(--accent-light)`} />
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
                        className="btn btn-primary px-5 py-2 text-sm disabled:opacity-40">
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
                    <h3 className="font-mono text-[10px] uppercase tracking-[0.08em] text-(--base-06) mb-3">Route Allocation</h3>
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
                    <h3 className="font-mono text-[10px] uppercase tracking-[0.08em] text-(--base-06) mb-3">Port Configuration</h3>
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

            <div className="flex gap-3 pt-2">
                <button onClick={handleSave} disabled={saving} className="btn btn-primary px-6 py-2 text-sm disabled:opacity-50">
                    <Save size={14} />
                    {saving ? 'Saving...' : 'Save Settings'}
                </button>
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
                            <button onClick={handleSaveRouting} className="btn btn-primary px-5 py-2 text-sm flex-1">Confirm & Apply</button>
                            <button onClick={() => setConfirmModal(false)} className="btn px-5 py-2 text-sm flex-1">Cancel</button>
                        </div>
                    </div>
                </div>
            )}
        </div>
    );
}

// ─────────────────────────────────────────────
// Main export: Gateway + Beam with left-nav
// ─────────────────────────────────────────────

export default function GatewayTab() {
    const [subTab, setSubTab] = useState<SubTab>('gateway');
    const [toast, setToast] = useState<{ msg: string; ok: boolean } | null>(null);

    const showToast = (msg: string, ok = true) => {
        setToast({ msg, ok });
        setTimeout(() => setToast(null), 3500);
    };

    return (
        <div className="flex gap-0 h-full">
            {/* Left nav */}
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

            {/* Right content */}
            <div className="flex-1 pl-6 overflow-y-auto">
                {subTab === 'gateway' && <GatewayPanel showToast={showToast} />}
                {subTab === 'beam' && <BeamPanel showToast={showToast} />}
            </div>

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
