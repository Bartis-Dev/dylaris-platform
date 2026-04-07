"use client";

import React, { useState, useEffect } from 'react';
import { getFeatureSettings, saveFeatureSettings, FeatureSettings } from '@/lib/api';
import { RefreshCw, Save, CircleCheck, CircleAlert, Network } from 'lucide-react';

export default function FeaturesTab() {
    const [settings, setSettings] = useState<FeatureSettings>({ proxyEnabled: true });
    const [loading, setLoading] = useState(true);
    const [saving, setSaving] = useState(false);
    const [toast, setToast] = useState<{ msg: string; ok: boolean } | null>(null);

    const showToast = (msg: string, ok = true) => {
        setToast({ msg, ok });
        setTimeout(() => setToast(null), 3500);
    };

    useEffect(() => {
        getFeatureSettings().then(res => {
            if (res.success && res.settings) setSettings(res.settings);
            setLoading(false);
        });
    }, []);

    const handleSave = async () => {
        setSaving(true);
        const res = await saveFeatureSettings(settings);
        if (res.success) {
            showToast('Feature settings saved.');
        } else {
            showToast(res.message || 'Save failed.', false);
        }
        setSaving(false);
    };

    if (loading) return <div className="flex items-center justify-center h-40 text-(--base-07)"><RefreshCw size={30} className="animate-spin" /></div>;

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
