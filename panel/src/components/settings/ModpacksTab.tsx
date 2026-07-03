"use client";

// Settings tab for the modpack-authoring subsystem. Houses:
//   - the platform-wide feature toggle (gates write endpoints + non-admin UI)
//   - the .mrpack storage provider (local mirror list or S3 bucket)
//   - the S3-secret "(unchanged — leave empty)" placeholder when a secret is
//     already on file (GET omits the value, returns secretSet=true)
// Pattern mirrors LibraryTab/FeaturesTab: snapshot + useUnsavedChanges so the
// shared dirty-bar in settings/layout.tsx surfaces save/discard.

import React, { useEffect, useRef, useState } from 'react';
import { Package, Plus, X, CircleCheck, CircleAlert } from 'lucide-react';
import { SkeletonHeader, SkeletonCard } from '@/components/Skeleton';
import { useUnsavedChanges } from '@/components/settings/UnsavedChanges';
import { getModpackSettings, setModpackSettings, type ModpackSettings } from '@/lib/api/modpackSettings';

const DEFAULTS: ModpackSettings = {
    featureEnabled: true,
    provider: 'local',
    paths: [],
    s3Endpoint: '',
    s3Bucket: '',
    s3Region: '',
    s3AccessKey: '',
    s3SecretKey: '',
    updateCheckIntervalHours: 24,
    shareLinksEnabled: false,
};

export default function ModpacksTab() {
    const [settings, setSettings] = useState<ModpackSettings>(DEFAULTS);
    const [secretSet, setSecretSet] = useState(false);
    const [loading, setLoading] = useState(true);
    const [saving, setSaving] = useState(false);
    const [toast, setToast] = useState<{ msg: string; ok: boolean } | null>(null);

    // Snapshot of last-saved settings for dirty detection. We deliberately
    // stash a copy with s3SecretKey blanked out so toggling the password
    // field empty after save doesn't keep the bar dirty forever.
    const snapshotRef = useRef<ModpackSettings | null>(null);

    const flash = (msg: string, ok = true) => {
        setToast({ msg, ok });
        setTimeout(() => setToast(null), 3000);
    };

    useEffect(() => {
        getModpackSettings().then(res => {
            if (res.success && res.settings) {
                const loaded: ModpackSettings = { ...res.settings, s3SecretKey: '' };
                setSettings(loaded);
                snapshotRef.current = loaded;
                setSecretSet(res.secretSet ?? false);
            }
            setLoading(false);
        });
    }, []);

    const handleSave = async () => {
        setSaving(true);
        const res = await setModpackSettings(settings);
        if (res.success) {
            flash('Saved.');
            // Re-snapshot with secret blanked — the value was just persisted
            // server-side; we keep the input empty + show "(unchanged)".
            const after: ModpackSettings = { ...settings, s3SecretKey: '' };
            snapshotRef.current = after;
            if (settings.s3SecretKey) setSecretSet(true);
            setSettings(after);
        } else {
            flash(res.message || 'Save failed.', false);
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
        <div className="max-w-3xl space-y-6">
            <SkeletonHeader />
            <SkeletonCard height="h-20" />
            <SkeletonCard height="h-56" />
        </div>
    );

    return (
        <div className="max-w-3xl space-y-6">
            {/* Header */}
            <div>
                <h2 className="text-base font-display font-bold text-(--base-09) mb-1 flex items-center gap-2">
                    <Package size={16} className="text-(--accent-light)" />
                    Modpacks
                </h2>
                <p className="text-sm text-(--base-07)">
                    Storage layout for .mrpack archives and the platform-wide modpack-authoring toggle.
                </p>
            </div>

            {/* Feature toggle */}
            <div className="card p-5">
                <div className="flex items-center justify-between gap-4">
                    <div>
                        <div className="font-medium text-sm text-(--base-09)">Modpack Authoring</div>
                        <div className="text-xs text-(--base-06) mt-0.5 max-w-md">
                            When off, the Modpacks UI is hidden for non-admins and write endpoints return
                            503 feature_disabled. Existing modpacks stay readable and downloadable.
                        </div>
                    </div>
                    <button
                        type="button"
                        role="switch"
                        aria-checked={settings.featureEnabled}
                        onClick={() => setSettings(s => ({ ...s, featureEnabled: !s.featureEnabled }))}
                        className={`toggle-track ${settings.featureEnabled ? 'toggle-track-on' : 'toggle-track-off'}`}
                    >
                        <span className={`toggle-knob ${settings.featureEnabled ? 'toggle-knob-on' : 'toggle-knob-off'}`} />
                    </button>
                </div>
            </div>

            {/* Share links toggle */}
            <div className="card p-5">
                <div className="flex items-center justify-between gap-4">
                    <div>
                        <div className="font-medium text-sm text-(--base-09)">Share Links</div>
                        <div className="text-xs text-(--base-06) mt-0.5 max-w-md">
                            When on, pack authors can mint tokenized download links for a build
                            (client .mrpack or server pack). Off hides the create UI and blocks minting
                            new links; existing links keep serving while modpack authoring is on.
                        </div>
                    </div>
                    <button
                        type="button"
                        role="switch"
                        aria-checked={settings.shareLinksEnabled}
                        onClick={() => setSettings(s => ({ ...s, shareLinksEnabled: !s.shareLinksEnabled }))}
                        className={`toggle-track ${settings.shareLinksEnabled ? 'toggle-track-on' : 'toggle-track-off'}`}
                    >
                        <span className={`toggle-knob ${settings.shareLinksEnabled ? 'toggle-knob-on' : 'toggle-knob-off'}`} />
                    </button>
                </div>
            </div>

            {/* Auto-update cadence */}
            <div className="card p-5">
                <div className="flex items-center justify-between gap-4">
                    <div>
                        <div className="font-medium text-sm text-(--base-09)">Auto-update check interval</div>
                        <div className="text-xs text-(--base-06) mt-0.5 max-w-md">
                            How often the background worker re-checks Modrinth-linked mods for a newer
                            version (hours). Applies per mod since it was last checked.
                        </div>
                    </div>
                    <input
                        type="number"
                        min={1}
                        value={settings.updateCheckIntervalHours}
                        onChange={e => setSettings(s => ({ ...s, updateCheckIntervalHours: Math.max(1, parseInt(e.target.value || '24', 10)) }))}
                        className="input-mono w-24 text-right"
                    />
                </div>
            </div>

            {/* Provider radio + provider-specific fields */}
            <div className="card p-5 space-y-5">
                <div>
                    <div className="font-medium text-sm text-(--base-09) mb-2">Storage Provider</div>
                    <div className="flex gap-4">
                        {(['local', 's3'] as const).map(p => (
                            <label key={p} className="flex items-center gap-2 text-sm cursor-pointer">
                                <input
                                    type="radio"
                                    name="provider"
                                    checked={settings.provider === p}
                                    onChange={() => setSettings(s => ({ ...s, provider: p }))}
                                    className="accent-(--accent)"
                                />
                                <span className="font-mono uppercase tracking-wide">{p}</span>
                            </label>
                        ))}
                    </div>
                </div>

                {/* Local — mirrored paths */}
                {settings.provider === 'local' && (
                    <div>
                        <div className="mono-label mb-1">Paths</div>
                        <p className="text-xs text-(--base-06) mb-2">
                            Absolute paths. The provider writes to ALL paths (mirror); reads from the first hit.
                        </p>
                        <div className="space-y-2">
                            {settings.paths.length === 0 && (
                                <div className="text-xs text-(--warning) italic">
                                    No paths configured — publishing or downloading non-draft .mrpack will fail
                                    until at least one path is set.
                                </div>
                            )}
                            {settings.paths.map((p, i) => (
                                <div key={i} className="flex gap-2">
                                    <input
                                        type="text"
                                        value={p}
                                        onChange={e => setSettings(s => {
                                            const next = [...s.paths];
                                            next[i] = e.target.value;
                                            return { ...s, paths: next };
                                        })}
                                        placeholder="/var/lib/dylaris/modpacks"
                                        className="input-mono flex-1"
                                    />
                                    <button
                                        type="button"
                                        onClick={() => setSettings(s => ({
                                            ...s,
                                            paths: s.paths.filter((_, j) => j !== i),
                                        }))}
                                        className="btn btn-secondary btn-sm px-2"
                                        aria-label="Remove path"
                                        title="Remove path"
                                    >
                                        <X size={13} />
                                    </button>
                                </div>
                            ))}
                            <button
                                type="button"
                                onClick={() => setSettings(s => ({ ...s, paths: [...s.paths, ''] }))}
                                className="btn btn-secondary btn-sm inline-flex items-center gap-1"
                            >
                                <Plus size={12} />
                                Add path
                            </button>
                        </div>
                    </div>
                )}

                {/* S3 — endpoint + bucket + region + creds */}
                {settings.provider === 's3' && (
                    <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
                        <Field
                            label="Endpoint"
                            value={settings.s3Endpoint}
                            onChange={v => setSettings(s => ({ ...s, s3Endpoint: v }))}
                            placeholder="https://s3.amazonaws.com"
                        />
                        <Field
                            label="Bucket"
                            value={settings.s3Bucket}
                            onChange={v => setSettings(s => ({ ...s, s3Bucket: v }))}
                            placeholder="dylaris-modpacks"
                        />
                        <Field
                            label="Region"
                            value={settings.s3Region}
                            onChange={v => setSettings(s => ({ ...s, s3Region: v }))}
                            placeholder="us-east-1"
                        />
                        <Field
                            label="Access Key"
                            value={settings.s3AccessKey}
                            onChange={v => setSettings(s => ({ ...s, s3AccessKey: v }))}
                            placeholder="AKIA…"
                        />
                        <div className="sm:col-span-2 flex flex-col gap-[5px]">
                            <label className="mono-label">Secret Key</label>
                            <input
                                type="password"
                                value={settings.s3SecretKey ?? ''}
                                onChange={e => setSettings(s => ({ ...s, s3SecretKey: e.target.value }))}
                                placeholder={secretSet ? '(unchanged — leave empty to keep current)' : '(not set)'}
                                className="input-mono w-full"
                                autoComplete="new-password"
                            />
                            <p className="text-xs text-(--base-06)">
                                The secret is write-only on the wire — GET never returns it.
                            </p>
                        </div>
                    </div>
                )}
            </div>

            {/* Toast */}
            {toast && (
                <div className="toast-container">
                    <div className="toast">
                        <div className={`toast-bar ${toast.ok ? 'bg-(--success-light)' : 'bg-(--error-light)'}`} />
                        {toast.ok ? <CircleCheck size={14} /> : <CircleAlert size={14} />}
                        <span className="text-sm text-(--base-09)">{toast.msg}</span>
                    </div>
                </div>
            )}
        </div>
    );
}

function Field({
    label,
    value,
    onChange,
    placeholder,
}: {
    label: string;
    value: string;
    onChange: (v: string) => void;
    placeholder?: string;
}) {
    return (
        <div className="flex flex-col gap-[5px]">
            <label className="mono-label">{label}</label>
            <input
                type="text"
                value={value}
                onChange={e => onChange(e.target.value)}
                placeholder={placeholder}
                className="input-mono w-full"
            />
        </div>
    );
}
