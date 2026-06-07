"use client";

import React, { useState, useEffect, useRef } from 'react';
import { getFeatureSettings, saveFeatureSettings, FeatureSettings } from '@/lib/api';
import { getTelemetrySettings, setTelemetrySettings } from '@/lib/api/telemetry';
import { CircleCheck, CircleAlert, Network, Globe, Radio } from 'lucide-react';
import { SkeletonHeader, SkeletonCard } from '@/components/Skeleton';
import { useUnsavedChanges } from '@/components/settings/UnsavedChanges';

export default function FeaturesTab() {
    const [settings, setSettings] = useState<FeatureSettings>({ proxyEnabled: true, gatewayEnabled: true });
    const [loading, setLoading] = useState(true);
    const [saving, setSaving] = useState(false);
    const [toast, setToast] = useState<{ msg: string; ok: boolean } | null>(null);

    // Telemetry is a single bool that saves on click (no dirty-tracking) —
    // it's deliberately outside the FeatureSettings struct + Save bar so the
    // user can flip it independently without needing to remember to Save.
    const [telemetryEnabled, setTelemetryEnabled] = useState(true);
    const [telemetrySaving, setTelemetrySaving] = useState(false);

    // Snapshot of last-saved settings for dirty detection.
    const snapshotRef = useRef<FeatureSettings | null>(null);

    const showToast = (msg: string, ok = true) => {
        setToast({ msg, ok });
        setTimeout(() => setToast(null), 3500);
    };

    useEffect(() => {
        getFeatureSettings().then(res => {
            if (res.success && res.settings) {
                setSettings(res.settings);
                snapshotRef.current = res.settings;
            }
            setLoading(false);
        });
        getTelemetrySettings().then(res => {
            if (res.success && res.settings) {
                setTelemetryEnabled(res.settings.enabled);
            }
        });
    }, []);

    // Save-on-click: flip the UI optimistically, persist, revert + toast on
    // failure so the user sees the truth instead of a stale "on" state.
    const saveTelemetry = async (v: boolean) => {
        if (telemetrySaving) return;
        const prev = telemetryEnabled;
        setTelemetryEnabled(v);
        setTelemetrySaving(true);
        const res = await setTelemetrySettings({ enabled: v, endpoint: '' });
        if (!res.success) {
            setTelemetryEnabled(prev);
            showToast(res.message || 'Telemetry save failed.', false);
        }
        setTelemetrySaving(false);
    };

    const handleSave = async () => {
        setSaving(true);
        const res = await saveFeatureSettings(settings);
        if (res.success) {
            showToast('Feature settings saved.');
            snapshotRef.current = settings;
        } else {
            showToast(res.message || 'Save failed.', false);
        }
        setSaving(false);
    };

    const handleDiscard = () => {
        if (snapshotRef.current) setSettings(snapshotRef.current);
    };

    const dirty =
        snapshotRef.current !== null &&
        JSON.stringify(settings) !== JSON.stringify(snapshotRef.current);

    useUnsavedChanges({ dirty, save: handleSave, discard: handleDiscard, saving });

    if (loading) return (
        <div className="max-w-2xl space-y-6">
            <SkeletonHeader />
            <SkeletonCard height="h-20" />
            <SkeletonCard height="h-20" />
        </div>
    );

    return (
        <div className="max-w-2xl space-y-6">
            <div>
                <h2 className="text-base font-display font-bold text-(--base-09) mb-1">Feature Toggles</h2>
                <p className="text-sm text-(--base-07)">Enable or disable platform features. Disabled features hide all related UI and block API endpoints.</p>
            </div>

            <div className="card p-5">
                <div className="flex items-center justify-between">
                    <div className="flex items-center gap-3">
                        <div className="w-9 h-9 rounded-md bg-(--base-03) flex items-center justify-center">
                            <Network size={18} className="text-(--accent-light)" />
                        </div>
                        <div>
                            <div className="font-medium text-sm text-(--base-09)">Proxy / Network Support</div>
                            <div className="text-xs text-(--base-06)">BungeeCord, Velocity, Waterfall proxy containers and server linking</div>
                        </div>
                    </div>
                    <button
                        type="button"
                        role="switch"
                        aria-checked={settings.proxyEnabled}
                        onClick={() => setSettings(prev => ({ ...prev, proxyEnabled: !prev.proxyEnabled }))}
                        className={`toggle-track ${settings.proxyEnabled ? 'toggle-track-on' : 'toggle-track-off'}`}
                    >
                        <span className={`toggle-knob ${settings.proxyEnabled ? 'toggle-knob-on' : 'toggle-knob-off'}`} />
                    </button>
                </div>
            </div>

            <div className="card p-5">
                <div className="flex items-center justify-between">
                    <div className="flex items-center gap-3">
                        <div className="w-9 h-9 rounded-md bg-(--base-03) flex items-center justify-center">
                            <Globe size={18} className="text-(--accent-light)" />
                        </div>
                        <div>
                            <div className="font-medium text-sm text-(--base-09)">Gateway</div>
                            <div className="text-xs text-(--base-06)">Edge-based routing (Edges, Links, custom domains). Disabling hides the Edges and Routes sub-tabs in Infrastructure and blocks new route creation.</div>
                        </div>
                    </div>
                    <button
                        type="button"
                        role="switch"
                        aria-checked={settings.gatewayEnabled}
                        onClick={() => setSettings(prev => ({ ...prev, gatewayEnabled: !prev.gatewayEnabled }))}
                        className={`toggle-track ${settings.gatewayEnabled ? 'toggle-track-on' : 'toggle-track-off'}`}
                    >
                        <span className={`toggle-knob ${settings.gatewayEnabled ? 'toggle-knob-on' : 'toggle-knob-off'}`} />
                    </button>
                </div>
            </div>

            {/* Phase 18 — Anonymous telemetry. Saves on click (single boolean,
                no Save bar). Independent from FeatureSettings + handleSave. */}
            <div className="card p-5">
                <div className="flex items-center justify-between">
                    <div className="flex items-center gap-3">
                        <div className="w-9 h-9 rounded-md bg-(--base-03) flex items-center justify-center">
                            <Radio size={18} className="text-(--accent-light)" />
                        </div>
                        <div>
                            <div className="font-medium text-sm text-(--base-09)">Anonymous Usage Stats</div>
                            <div className="text-xs text-(--base-06)">
                                Sends a tiny anonymized payload to dylaris.dev every 10 min (instance type, container counts, total players, version — no hostnames, no user data) so the public live counter on dylaris.dev works. Disable any time.
                            </div>
                        </div>
                    </div>
                    <button
                        type="button"
                        role="switch"
                        aria-checked={telemetryEnabled}
                        disabled={telemetrySaving}
                        onClick={() => saveTelemetry(!telemetryEnabled)}
                        className={`toggle-track ${telemetryEnabled ? 'toggle-track-on' : 'toggle-track-off'}`}
                    >
                        <span className={`toggle-knob ${telemetryEnabled ? 'toggle-knob-on' : 'toggle-knob-off'}`} />
                    </button>
                </div>
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
