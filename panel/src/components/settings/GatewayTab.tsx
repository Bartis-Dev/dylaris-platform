"use client";

import React, { useState, useEffect } from 'react';
import { getGatewaySettings, saveGatewaySettings, GatewaySettings } from '@/lib/api';
import { RefreshCw, Save, CircleCheck, CircleAlert, Shield, Router, Database, ChevronDown } from 'lucide-react';

type LimitKey = 'global' | 'userDefault' | 'perServer' | 'portMc' | 'portHttps';

export default function GatewayTab() {
    const [settings, setSettings] = useState<GatewaySettings>({
        redisMode: 'shared',
        redisAddr: '',
        redisUser: '',
        redisPass: '',
        redisDb: 0,
        defaultLinkImage: '',
        limits: {
            global: -1,
            userDefault: -1,
            perServer: -1,
            portMc: -1,
            portMcEnabled: true,
            portHttps: -1,
            portHttpsEnabled: true,
        },
    });
    const [loading, setLoading] = useState(true);
    const [saving, setSaving] = useState(false);
    const [toast, setToast] = useState<{ msg: string; ok: boolean } | null>(null);
    const [redisOpen, setRedisOpen] = useState(false);

    const showToast = (msg: string, ok = true) => {
        setToast({ msg, ok });
        setTimeout(() => setToast(null), 3500);
    };

    useEffect(() => {
        getGatewaySettings()
            .then(res => {
                if (res.success && res.settings) {
                    setSettings(res.settings);
                    if (res.settings.redisMode === 'separate') {
                        setRedisOpen(true);
                    }
                }
            })
            .finally(() => setLoading(false));
    }, []);

    const handleSave = async () => {
        setSaving(true);
        const res = await saveGatewaySettings(settings);
        if (res.success) {
            showToast('Gateway settings saved.');
        } else {
            showToast(res.message || 'Save failed.', false);
        }
        setSaving(false);
    };

    const setLimit = (key: LimitKey, value: number) => {
        setSettings(prev => ({ ...prev, limits: { ...prev.limits, [key]: value } }));
    };

    const isUnlimited = (key: LimitKey) => settings.limits[key] === -1;

    const toggleUnlimited = (key: LimitKey) => {
        if (isUnlimited(key)) {
            setLimit(key, 0);
        } else {
            setLimit(key, -1);
        }
    };

    const isSeparate = settings.redisMode === 'separate';

    const toggleRedisMode = () => {
        if (isSeparate) {
            setSettings(prev => ({ ...prev, redisMode: 'shared' }));
        } else {
            setSettings(prev => ({ ...prev, redisMode: 'separate' }));
            setRedisOpen(true);
        }
    };

    if (loading) {
        return (
            <div className="flex items-center justify-center h-40 text-(--base-07)">
                <RefreshCw size={30} className="animate-spin" />
            </div>
        );
    }

    const allocationFields: { key: LimitKey; label: string; desc: string }[] = [
        { key: 'global', label: 'Global Max Routes', desc: 'Total routes across all users and servers' },
        { key: 'userDefault', label: 'Default Per-User Max', desc: 'Default limit for users without a custom override' },
        { key: 'perServer', label: 'Per-Server Max', desc: 'Max routes per individual MC server' },
    ];

    return (
        <div className="max-w-2xl space-y-6">
            <div>
                <h2 className="text-base font-display font-bold text-(--base-09) mb-1">Gateway Configuration</h2>
                <p className="text-sm text-(--base-07)">Manage gateway routing, link defaults and route limits for gates and links.</p>
            </div>

            {/* Redis Connection */}
            <div className="card overflow-hidden">
                <button
                    type="button"
                    onClick={() => setRedisOpen(!redisOpen)}
                    className="w-full p-5 flex items-center gap-3 hover:bg-(--base-02) transition-colors"
                >
                    <div className="w-9 h-9 rounded-md bg-(--base-03) flex items-center justify-center shrink-0">
                        <Database size={18} className="text-(--accent-light)" />
                    </div>
                    <div className="flex-1 text-left">
                        <div className="font-medium text-sm text-(--base-09)">Redis Connection</div>
                        <div className="text-xs text-(--base-06)">
                            {isSeparate ? 'Using separate Redis instance' : 'Using shared Redis (Core)'}
                            {!isSeparate && settings.redisDb > 0 && ` · DB ${settings.redisDb}`}
                        </div>
                    </div>
                    <ChevronDown
                        size={16}
                        className={`text-(--base-06) transition-transform duration-200 ${redisOpen ? 'rotate-180' : ''}`}
                    />
                </button>

                {redisOpen && (
                    <div className="px-5 pb-5 space-y-4 border-t border-(--base-03)">
                        <div className="flex items-center justify-between pt-4">
                            <div>
                                <p className="text-sm text-(--base-09)">Use separate Redis</p>
                                <p className="text-xs text-(--base-06)">Connect to a dedicated Redis instance instead of the shared Core Redis</p>
                            </div>
                            <button
                                type="button"
                                role="switch"
                                aria-checked={isSeparate}
                                onClick={toggleRedisMode}
                                className={`toggle-track ${isSeparate ? 'toggle-track-on' : 'toggle-track-off'}`}
                            >
                                <span className={`toggle-knob ${isSeparate ? 'toggle-knob-on' : 'toggle-knob-off'}`} />
                            </button>
                        </div>

                        <div className="flex flex-col gap-[5px]">
                            <label className="input-label">Redis DB Index</label>
                            <select
                                value={settings.redisDb}
                                onChange={e => setSettings(prev => ({ ...prev, redisDb: Number(e.target.value) }))}
                                className="input-mono w-full"
                            >
                                {Array.from({ length: 16 }, (_, i) => (
                                    <option key={i} value={i}>{i}{i === 0 ? ' (default)' : ''}</option>
                                ))}
                            </select>
                            <p className="text-xs text-(--base-06)">
                                {isSeparate
                                    ? 'Database index on the separate Redis instance.'
                                    : 'Use a different DB index on the shared Redis to isolate gateway data.'}
                            </p>
                        </div>

                        {isSeparate && (
                            <div className="grid grid-cols-2 gap-4 pt-2 border-t border-(--base-03)">
                                <div className="flex flex-col gap-[5px]">
                                    <label className="input-label">Redis Address</label>
                                    <input
                                        type="text"
                                        value={settings.redisAddr}
                                        onChange={e => setSettings(prev => ({ ...prev, redisAddr: e.target.value }))}
                                        placeholder="localhost:6379"
                                        className="input-mono w-full"
                                    />
                                </div>
                                <div className="flex flex-col gap-[5px]">
                                    <label className="input-label">Redis User</label>
                                    <input
                                        type="text"
                                        value={settings.redisUser}
                                        onChange={e => setSettings(prev => ({ ...prev, redisUser: e.target.value }))}
                                        placeholder="default"
                                        className="input-mono w-full"
                                    />
                                </div>
                                <div className="col-span-2 flex flex-col gap-[5px]">
                                    <label className="input-label">Redis Password</label>
                                    <input
                                        type="password"
                                        value={settings.redisPass ?? ''}
                                        onChange={e => setSettings(prev => ({ ...prev, redisPass: e.target.value }))}
                                        placeholder="••••••••"
                                        className="input-mono w-full"
                                    />
                                </div>
                            </div>
                        )}
                    </div>
                )}
            </div>

            {/* General */}
            <div className="card p-5 space-y-4">
                <div className="flex items-center gap-3 mb-2">
                    <div className="w-9 h-9 rounded-md bg-(--base-03) flex items-center justify-center">
                        <Shield size={18} className="text-(--accent-light)" />
                    </div>
                    <div>
                        <div className="font-medium text-sm text-(--base-09)">General</div>
                        <div className="text-xs text-(--base-06)">Default link container image</div>
                    </div>
                </div>

                <div className="flex flex-col gap-[5px]">
                    <label className="input-label">Default Link Image</label>
                    <input
                        type="text"
                        value={settings.defaultLinkImage}
                        onChange={e => setSettings(prev => ({ ...prev, defaultLinkImage: e.target.value }))}
                        placeholder="ghcr.io/bartis-dev/dylaris-link:latest"
                        className="input-mono w-full"
                    />
                    <p className="text-xs text-(--base-06) mt-0.5">Docker image used for new link containers when no override is specified.</p>
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

                {/* Route Allocation */}
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
                                        <input
                                            type="number"
                                            min={0}
                                            value={settings.limits[key]}
                                            onChange={e => setLimit(key, Number(e.target.value))}
                                            className="input-mono w-20 text-center"
                                        />
                                    )}
                                    <label className="flex items-center gap-1.5 cursor-pointer select-none">
                                        <button
                                            type="button"
                                            role="switch"
                                            aria-checked={isUnlimited(key)}
                                            onClick={() => toggleUnlimited(key)}
                                            className={`toggle-track ${isUnlimited(key) ? 'toggle-track-on' : 'toggle-track-off'}`}
                                        >
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

                {/* Port Configuration */}
                <div className="border-t border-(--base-03) pt-5">
                    <h3 className="font-mono text-[10px] uppercase tracking-[0.08em] text-(--base-06) mb-3">Port Configuration</h3>
                    <div className="space-y-3">
                        {/* MC Port 25565 */}
                        <div className={`p-3 rounded-md bg-(--base-02) ${!settings.limits.portMcEnabled ? 'opacity-60' : ''}`}>
                            <div className="flex items-center justify-between mb-2">
                                <div className="flex items-center gap-2">
                                    <span className="text-sm font-semibold text-(--base-09)">Minecraft</span>
                                    <span className="font-mono text-[10px] px-1.5 py-0.5 rounded bg-(--base-03) text-(--base-06)">25565</span>
                                </div>
                                <button
                                    type="button"
                                    role="switch"
                                    aria-checked={settings.limits.portMcEnabled}
                                    onClick={() => setSettings(prev => ({
                                        ...prev,
                                        limits: { ...prev.limits, portMcEnabled: !prev.limits.portMcEnabled }
                                    }))}
                                    className={`toggle-track ${settings.limits.portMcEnabled ? 'toggle-track-on' : 'toggle-track-off'}`}
                                >
                                    <span className={`toggle-knob ${settings.limits.portMcEnabled ? 'toggle-knob-on' : 'toggle-knob-off'}`} />
                                </button>
                            </div>
                            {settings.limits.portMcEnabled && (
                                <div className="flex items-center justify-between mt-2 pt-2 border-t border-(--base-03)">
                                    <span className="text-xs text-(--base-06)">Max routes on this port</span>
                                    <div className="flex items-center gap-3">
                                        {!isUnlimited('portMc') && (
                                            <input
                                                type="number"
                                                min={0}
                                                value={settings.limits.portMc}
                                                onChange={e => setLimit('portMc', Number(e.target.value))}
                                                className="input-mono w-20 text-center"
                                            />
                                        )}
                                        <label className="flex items-center gap-1.5 cursor-pointer select-none">
                                            <button
                                                type="button"
                                                role="switch"
                                                aria-checked={isUnlimited('portMc')}
                                                onClick={() => toggleUnlimited('portMc')}
                                                className={`toggle-track ${isUnlimited('portMc') ? 'toggle-track-on' : 'toggle-track-off'}`}
                                            >
                                                <span className={`toggle-knob ${isUnlimited('portMc') ? 'toggle-knob-on' : 'toggle-knob-off'}`} />
                                            </button>
                                            <span className="text-[10px] font-mono uppercase text-(--base-06)">Unlimited</span>
                                        </label>
                                    </div>
                                </div>
                            )}
                        </div>

                        {/* HTTPS Port 443 */}
                        <div className={`p-3 rounded-md bg-(--base-02) ${!settings.limits.portHttpsEnabled ? 'opacity-60' : ''}`}>
                            <div className="flex items-center justify-between mb-2">
                                <div className="flex items-center gap-2">
                                    <span className="text-sm font-semibold text-(--base-09)">HTTPS</span>
                                    <span className="font-mono text-[10px] px-1.5 py-0.5 rounded bg-(--base-03) text-(--base-06)">443</span>
                                </div>
                                <button
                                    type="button"
                                    role="switch"
                                    aria-checked={settings.limits.portHttpsEnabled}
                                    onClick={() => setSettings(prev => ({
                                        ...prev,
                                        limits: { ...prev.limits, portHttpsEnabled: !prev.limits.portHttpsEnabled }
                                    }))}
                                    className={`toggle-track ${settings.limits.portHttpsEnabled ? 'toggle-track-on' : 'toggle-track-off'}`}
                                >
                                    <span className={`toggle-knob ${settings.limits.portHttpsEnabled ? 'toggle-knob-on' : 'toggle-knob-off'}`} />
                                </button>
                            </div>
                            {settings.limits.portHttpsEnabled && (
                                <div className="flex items-center justify-between mt-2 pt-2 border-t border-(--base-03)">
                                    <span className="text-xs text-(--base-06)">Max routes on this port</span>
                                    <div className="flex items-center gap-3">
                                        {!isUnlimited('portHttps') && (
                                            <input
                                                type="number"
                                                min={0}
                                                value={settings.limits.portHttps}
                                                onChange={e => setLimit('portHttps', Number(e.target.value))}
                                                className="input-mono w-20 text-center"
                                            />
                                        )}
                                        <label className="flex items-center gap-1.5 cursor-pointer select-none">
                                            <button
                                                type="button"
                                                role="switch"
                                                aria-checked={isUnlimited('portHttps')}
                                                onClick={() => toggleUnlimited('portHttps')}
                                                className={`toggle-track ${isUnlimited('portHttps') ? 'toggle-track-on' : 'toggle-track-off'}`}
                                            >
                                                <span className={`toggle-knob ${isUnlimited('portHttps') ? 'toggle-knob-on' : 'toggle-knob-off'}`} />
                                            </button>
                                            <span className="text-[10px] font-mono uppercase text-(--base-06)">Unlimited</span>
                                        </label>
                                    </div>
                                </div>
                            )}
                        </div>
                    </div>
                    <p className="text-xs text-(--base-05) mt-2">Disabled ports block all route creation on that port. HTTP (80) is not available for security reasons.</p>
                </div>
            </div>

            {/* Save */}
            <div className="flex gap-3 pt-2">
                <button
                    onClick={handleSave}
                    disabled={saving}
                    className="btn btn-primary px-6 py-2 text-sm disabled:opacity-50"
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
