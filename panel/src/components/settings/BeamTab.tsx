"use client";

import React, { useState, useEffect } from 'react';
import { Save, CircleCheck, CircleAlert, Radar } from 'lucide-react';
import LoadingState from '@/components/LoadingState';

interface BeamRelayInfo {
    beam_id: string;
    ip: string;
    private_ip?: string;
    public_host?: string;
    service_port: string;
    client_port?: string;
    download_port?: string;
    timestamp: number;
}

interface BeamSettings {
    relayAddress: string;             // Effective (discovered or manual)
    manualOverride: string;           // Admin-configured override
    discoveredRelays: BeamRelayInfo[]; // Auto-registered relays
    bwLimit: number;
    enabled: boolean;
    downloadLink?: string;
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
            headers: {
                'Content-Type': 'application/json',
                Authorization: `Bearer ${token}`,
            },
            body: JSON.stringify(settings),
        });
        return await res.json();
    } catch {
        return { success: false, message: 'Network error' };
    }
}

export default function BeamTab() {
    const [settings, setSettings] = useState<BeamSettings>({
        relayAddress: '',
        manualOverride: '',
        discoveredRelays: [],
        bwLimit: 0,
        enabled: true,
    });
    const [loading, setLoading] = useState(true);
    const [saving, setSaving] = useState(false);
    const [toast, setToast] = useState<{ msg: string; ok: boolean } | null>(null);

    // BW display state
    const [bwValue, setBwValue] = useState(0);
    const [bwUnit, setBwUnit] = useState('MB/s');
    const [unlimited, setUnlimited] = useState(true);

    const showToast = (msg: string, ok = true) => {
        setToast({ msg, ok });
        setTimeout(() => setToast(null), 3500);
    };

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
        // Backend stores manualOverride under beam.relay_address — send as relayAddress.
        const payload: BeamSettings = {
            ...settings,
            relayAddress: settings.manualOverride,
            bwLimit,
        };
        const res = await saveBeamSettings(payload);
        if (res.success) {
            showToast('Beam settings saved.');
        } else {
            showToast(res.message || 'Save failed.', false);
        }
        setSaving(false);
    };

    if (loading) return <LoadingState />;

    return (
        <div className="max-w-2xl space-y-6">
            <div>
                <h2 className="text-base font-display font-bold text-(--base-09) mb-1">Beam File Transfer</h2>
                <p className="text-sm text-(--base-07)">Configure the Beam desktop file transfer service. Users can download the Beam app to manage server files directly.</p>
            </div>

            {/* General */}
            <div className="card p-5 space-y-4">
                <h3 className="text-sm font-display font-semibold text-(--accent-light) mb-2">General</h3>

                {/* Enabled Toggle */}
                <div className="flex items-center justify-between">
                    <div>
                        <label className="input-label">Beam Enabled</label>
                        <p className="text-xs text-(--base-06) mt-0.5">Allow users to connect via Beam desktop app</p>
                    </div>
                    <button
                        onClick={() => setSettings(s => ({ ...s, enabled: !s.enabled }))}
                        className="toggle-track"
                        role="switch"
                        aria-checked={settings.enabled}
                    >
                        <span className={`toggle-knob ${settings.enabled ? 'translate-x-5 bg-(--accent)' : 'translate-x-0.5 bg-(--base-05)'}`} />
                    </button>
                </div>

                {/* Discovered Relays */}
                <div className="flex flex-col gap-2">
                    <div className="flex items-center justify-between">
                        <label className="input-label">Discovered Relays</label>
                        <span className="mono-label">{settings.discoveredRelays.length} online</span>
                    </div>
                    {settings.discoveredRelays.length > 0 ? (
                        <ul className="rounded-md border border-(--base-03) divide-y divide-(--base-03) bg-(--base-01)">
                            {settings.discoveredRelays.map(relay => {
                                const reachable = relay.public_host || relay.ip;
                                const isInternal = !relay.public_host && relay.ip;
                                return (
                                    <li key={relay.beam_id} className="flex items-center gap-3 px-3 py-2">
                                        <Radar size={12} className="text-(--success-light) shrink-0" />
                                        <div className="min-w-0 flex-1">
                                            <div className="flex items-center gap-2 flex-wrap">
                                                <span className="text-sm font-medium text-(--base-09)">{relay.beam_id}</span>
                                                <span className="font-mono text-xs text-(--base-08)">{reachable || 'no address'}</span>
                                                {isInternal && (
                                                    <span className="badge badge-warning" title="Set BEAM_PUBLIC_HOST in the relay container so clients can reach it from outside Swarm">
                                                        internal IP
                                                    </span>
                                                )}
                                            </div>
                                            <div className="text-xs text-(--base-06) font-mono mt-0.5">
                                                service:{relay.service_port}
                                                {relay.client_port && <> · client:{relay.client_port}</>}
                                                {relay.download_port && <> · download:{relay.download_port}</>}
                                            </div>
                                        </div>
                                    </li>
                                );
                            })}
                        </ul>
                    ) : (
                        <p className="alert alert-warning text-xs">
                            No relays have registered themselves via Redis auto-discovery yet.
                            Either start a Beam relay (it self-registers in <code className="font-mono">sys:beams</code> + <code className="font-mono">beam:registry:*</code>)
                            or set a manual override below.
                        </p>
                    )}
                </div>

                {/* Manual Override */}
                <div className="flex flex-col gap-[5px]">
                    <label className="input-label">Manual Override</label>
                    <p className="text-xs text-(--base-06) mb-1">Optional. When set, forces all clients to this address and bypasses discovery.</p>
                    <input
                        type="text"
                        value={settings.manualOverride}
                        onChange={e => setSettings(s => ({ ...s, manualOverride: e.target.value }))}
                        placeholder="beam.example.com:9095 (leave blank for auto-discovery)"
                        className="input-field font-mono"
                    />
                    <p className="text-xs text-(--base-06)">
                        Effective relay: <span className="font-mono text-(--base-08)">{settings.relayAddress || '— none —'}</span>
                    </p>
                </div>
            </div>

            {/* Bandwidth */}
            <div className="card p-5 space-y-4">
                <h3 className="text-sm font-display font-semibold text-(--base-08) mb-2">Bandwidth Limit</h3>
                <p className="text-xs text-(--base-06)">Global bandwidth cap shared across all Beam transfers on each node. Fair sharing is automatic.</p>

                {/* Unlimited Toggle */}
                <div className="flex items-center justify-between">
                    <label className="input-label">Unlimited</label>
                    <button
                        onClick={() => setUnlimited(!unlimited)}
                        className="toggle-track"
                        role="switch"
                        aria-checked={unlimited}
                    >
                        <span className={`toggle-knob ${unlimited ? 'translate-x-5 bg-(--accent)' : 'translate-x-0.5 bg-(--base-05)'}`} />
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
                        <select
                            value={bwUnit}
                            onChange={e => setBwUnit(e.target.value)}
                            className="input-field w-28"
                        >
                            {BW_UNITS.map(u => (
                                <option key={u.label} value={u.label}>{u.label}</option>
                            ))}
                        </select>
                    </div>
                )}
            </div>

            {/* Save */}
            <div className="flex gap-3 pt-2">
                <button
                    onClick={handleSave}
                    disabled={saving}
                    className="btn btn-primary disabled:opacity-40"
                >
                    <Save size={14} />
                    {saving ? 'Saving...' : 'Save Settings'}
                </button>
            </div>

            {/* Toast */}
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
