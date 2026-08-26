"use client";

import React, { useState, useEffect, useRef } from 'react';
import { getServerSettings, saveServerSettings, ServerLimitSettings } from '@/lib/api';
import { Server } from 'lucide-react';
import { SkeletonHeader, SkeletonCard } from '@/components/Skeleton';
import { useUnsavedChanges } from '@/components/settings/UnsavedChanges';
import { toast } from '@/components/ui/Toast';

export default function ServersTab() {
    const [settings, setSettings] = useState<ServerLimitSettings>({ maxSubServers: 3 });
    const [loading, setLoading] = useState(true);
    const [saving, setSaving] = useState(false);

    // Snapshot of last-saved settings for dirty detection.
    const snapshotRef = useRef<ServerLimitSettings | null>(null);

    const showToast = (msg: string, ok = true) => toast(msg, ok);

    useEffect(() => {
        getServerSettings().then(res => {
            if (res.success && res.settings) {
                setSettings(res.settings);
                snapshotRef.current = res.settings;
            }
            setLoading(false);
        });
    }, []);

    const handleSave = async (): Promise<boolean> => {
        setSaving(true);
        try {
        const res = await saveServerSettings(settings);
        if (res.success) {
            showToast('Server settings saved.');
            snapshotRef.current = settings;
            return true;
        }
        showToast(res.message || 'Save failed.', false);
        return false;
        } finally {
            setSaving(false);
        }
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
            <SkeletonCard height="h-32" />
        </div>
    );

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
        </div>
    );
}
