"use client";

import React, { useState, useEffect, useRef } from 'react';
import { getServerSettings, saveServerSettings, ServerLimitSettings } from '@/lib/api';
import { CircleCheck, CircleAlert, Server } from 'lucide-react';
import LoadingState from '@/components/LoadingState';
import { useUnsavedChanges } from '@/components/settings/UnsavedChanges';

export default function ServersTab() {
    const [settings, setSettings] = useState<ServerLimitSettings>({ maxSubServers: 3 });
    const [loading, setLoading] = useState(true);
    const [saving, setSaving] = useState(false);
    const [toast, setToast] = useState<{ msg: string; ok: boolean } | null>(null);

    // Snapshot of last-saved settings for dirty detection.
    const snapshotRef = useRef<ServerLimitSettings | null>(null);

    const showToast = (msg: string, ok = true) => {
        setToast({ msg, ok });
        setTimeout(() => setToast(null), 3500);
    };

    useEffect(() => {
        getServerSettings().then(res => {
            if (res.success && res.settings) {
                setSettings(res.settings);
                snapshotRef.current = res.settings;
            }
            setLoading(false);
        });
    }, []);

    const handleSave = async () => {
        setSaving(true);
        const res = await saveServerSettings(settings);
        if (res.success) {
            showToast('Server settings saved.');
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

    if (loading) return <LoadingState />;

    return (
        <div className="max-w-2xl space-y-6">
            <div>
                <h2 className="text-base font-display font-bold text-(--base-09) mb-1">Server Limits</h2>
                <p className="text-sm text-(--base-07)">Configure default limits that apply to all servers on the platform.</p>
            </div>

            <div className="card p-5 space-y-5">
                <div>
                    <div className="flex items-center gap-3 mb-3">
                        <div className="w-9 h-9 rounded-md bg-(--base-03) flex items-center justify-center">
                            <Server size={18} className="text-(--accent-light)" />
                        </div>
                        <div>
                            <div className="font-medium text-sm text-(--base-09)">Max Sub-Servers per Server</div>
                            <div className="text-xs text-(--base-06)">Maximum number of sub-servers a user can create per server. Set to 0 for unlimited.</div>
                        </div>
                    </div>
                    <div className="flex items-center gap-3 pl-12">
                        <div className="relative">
                            <input
                                type="number"
                                min={0}
                                max={100}
                                value={settings.maxSubServers}
                                onChange={e => setSettings(prev => ({ ...prev, maxSubServers: Math.max(0, parseInt(e.target.value) || 0) }))}
                                className="input-field input-mono w-28 text-center [appearance:textfield] [&::-webkit-outer-spin-button]:appearance-none [&::-webkit-inner-spin-button]:appearance-none"
                            />
                        </div>
                        <span className="text-xs text-(--base-06)">
                            {settings.maxSubServers === 0 ? 'Unlimited' : 'sub-servers per server'}
                        </span>
                    </div>
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
