"use client";

import React, { useState, useEffect , useRef} from 'react';
import { getBillingSettings, setBillingSettings, BillingSettings } from '@/lib/api/billing';
import { SkeletonHeader, SkeletonCard } from '@/components/Skeleton';
import { Loader2, Info } from 'lucide-react';
import { toast } from '@/components/ui/Toast';
import { useUnsavedChanges } from '@/components/settings/UnsavedChanges';

const SPEC_RE = /^\d+[dwm]$/;

export default function BillingTab() {
    const [settings, setSettings] = useState<BillingSettings>({ gracePeriod: '3d', r2Retention: '3m', nodeRetention: '2w', r2QuotaGb: '0', presignTtlNodeMin: '60', presignTtlByonMin: '360', paymentUrl: '' });
    const [loading, setLoading] = useState(true);
    const [saving, setSaving] = useState(false);
    const [loadError, setLoadError] = useState<string | null>(null);
    // See MaintenanceTab: this form tracked nothing either. A null snapshot
    // after a failed load keeps it clean, so the component's own hardcoded
    // defaults can never be saved over real settings.
    const snapshotRef = useRef<BillingSettings | null>(null);

    useEffect(() => {
        getBillingSettings().then(res => {
            // Core answers 503 feature_disabled when BYON is off. Dropping that left
            // the component's OWN hardcoded defaults on screen looking like stored
            // settings, above a Save button whose write would fail the same way -
            // the same misread PlansTab already fixes.
            if (!res.success) setLoadError(res.message || 'Failed to load billing settings.');
            if (res.success) {
                const stored: BillingSettings = {
                    gracePeriod: res.gracePeriod || '3d',
                    r2Retention: res.r2Retention || '3m',
                    nodeRetention: res.nodeRetention || '2w',
                    r2QuotaGb: res.r2QuotaGb || '0',
                    presignTtlNodeMin: res.presignTtlNodeMin || '60',
                    presignTtlByonMin: res.presignTtlByonMin || '360',
                    paymentUrl: res.paymentUrl || '',
                };
                setSettings(stored);
                snapshotRef.current = stored;
            }
            setLoading(false);
        });
    }, []);

    const showToast = (msg: string, ok = true) => toast(msg, ok);

    const specsValid = SPEC_RE.test(settings.gracePeriod) && SPEC_RE.test(settings.r2Retention) && SPEC_RE.test(settings.nodeRetention);

    const save = async () => {
        if (!specsValid) { showToast('Retention values must look like 3d, 2w or 3m.', false); return; }
        setSaving(true);
        const res = await setBillingSettings(settings);
        setSaving(false);
        showToast(res.success ? 'Saved.' : (res.message || 'Save failed.'), res.success);
    };

    const dirty =
        snapshotRef.current !== null &&
        JSON.stringify(settings) !== JSON.stringify(snapshotRef.current);

    const handleDiscard = () => {
        if (snapshotRef.current) setSettings(snapshotRef.current);
    };

    useUnsavedChanges({ dirty, save, discard: handleDiscard, saving });

    if (loading) {
        return <div className="space-y-4"><SkeletonHeader /><SkeletonCard /></div>;
    }

    return (
        <div className="space-y-6 max-w-2xl">
            <div>
                <h2 className="font-display text-xl text-(--base-09)">Billing & Non-Payment</h2>
                <p className="font-mono text-[11px] uppercase tracking-[0.08em] text-(--base-06) mt-1">Platform defaults (BYON)</p>
            </div>

            {loadError && <p className="text-sm text-(--error)">{loadError}</p>}

            <div className="flex items-start gap-2 rounded-md border border-(--base-04) bg-(--base-01) p-3">
                <Info size={15} className="text-(--base-06) shrink-0 mt-0.5" />
                <p className="text-sm text-(--base-07)">
                    When a tenant does not pay they enter a grace period (everything keeps running, a dunning email goes out,
                    a red banner shows in their panel). After grace they are suspended (servers stopped, data + backups kept and
                    still viewable). Their R2 backups and node connection are only removed after the retention windows below.
                    User accounts and server metadata are never deleted. Per-user overrides can be set from User Management.
                </p>
            </div>

            <div className="space-y-4 rounded-md border border-(--base-04) p-4">
                <Field
                    label="Grace period"
                    hint="How long after a missed payment before suspension. e.g. 3d"
                    value={settings.gracePeriod}
                    onChange={v => setSettings(s => ({ ...s, gracePeriod: v }))}
                    valid={SPEC_RE.test(settings.gracePeriod)}
                />
                <Field
                    label="R2 backup retention"
                    hint="How long backups are kept after suspension before deletion. e.g. 3m"
                    value={settings.r2Retention}
                    onChange={v => setSettings(s => ({ ...s, r2Retention: v }))}
                    valid={SPEC_RE.test(settings.r2Retention)}
                />
                <Field
                    label="Node connection retention"
                    hint="How long the node connection is kept after suspension. e.g. 2w"
                    value={settings.nodeRetention}
                    onChange={v => setSettings(s => ({ ...s, nodeRetention: v }))}
                    valid={SPEC_RE.test(settings.nodeRetention)}
                />
                <div className="space-y-1">
                    <label className="font-mono text-[10px] uppercase tracking-[0.08em] text-(--base-06)">R2 backup quota (GB)</label>
                    <input
                        type="number"
                        min={0}
                        value={settings.r2QuotaGb}
                        onChange={e => setSettings(s => ({ ...s, r2QuotaGb: e.target.value }))}
                        className="input-field w-32"
                    />
                    <p className="text-xs text-(--base-05)">Max stored backup size per tenant. 0 = unlimited. New backups are blocked with a message once exceeded. Per-user overrides come from User Management.</p>
                </div>

                <div className="grid grid-cols-2 gap-4">
                    <div className="space-y-1">
                        <label className="font-mono text-[10px] uppercase tracking-[0.08em] text-(--base-06)">Presigned URL TTL — nodes (min)</label>
                        <input
                            type="number"
                            min={1}
                            value={settings.presignTtlNodeMin}
                            onChange={e => setSettings(s => ({ ...s, presignTtlNodeMin: e.target.value }))}
                            className="input-field w-32"
                        />
                        <p className="text-xs text-(--base-05)">Operator nodes. 60 = 1h.</p>
                    </div>
                    <div className="space-y-1">
                        <label className="font-mono text-[10px] uppercase tracking-[0.08em] text-(--base-06)">Presigned URL TTL — BYON (min)</label>
                        <input
                            type="number"
                            min={1}
                            value={settings.presignTtlByonMin}
                            onChange={e => setSettings(s => ({ ...s, presignTtlByonMin: e.target.value }))}
                            className="input-field w-32"
                        />
                        <p className="text-xs text-(--base-05)">Tenant nodes (slower uplinks). 360 = 6h.</p>
                    </div>
                </div>

                <div className="space-y-1">
                    <label className="font-mono text-[10px] uppercase tracking-[0.08em] text-(--base-06)">Payment URL</label>
                    <input
                        type="url"
                        value={settings.paymentUrl}
                        onChange={e => setSettings(s => ({ ...s, paymentUrl: e.target.value }))}
                        placeholder="https://pay.example.com/..."
                        className="input-field w-full"
                    />
                    <p className="text-xs text-(--base-05)">Where the red banner + dunning email link. Leave empty to use the in-app /account/billing page.</p>
                </div>
            </div>

            <div className="flex items-center gap-3">
                <button onClick={save} disabled={saving || !specsValid} className="btn btn-primary inline-flex items-center gap-1.5 disabled:opacity-40">
                    {saving && <Loader2 size={14} className="animate-spin" />} Save
                </button>
            </div>
        </div>
    );
}

function Field({ label, hint, value, onChange, valid }: { label: string; hint: string; value: string; onChange: (v: string) => void; valid: boolean }) {
    return (
        <div className="space-y-1">
            <label className="font-mono text-[10px] uppercase tracking-[0.08em] text-(--base-06)">{label}</label>
            <input
                type="text"
                value={value}
                onChange={e => onChange(e.target.value)}
                className={`input-field w-32 ${valid ? '' : 'border-(--error)'}`}
            />
            <p className="text-xs text-(--base-05)">{hint}</p>
        </div>
    );
}
