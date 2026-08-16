"use client";

// Settings tab for the modpack-authoring subsystem. Houses:
//   - the platform-wide feature toggle (gates write endpoints + non-admin UI)
//   - the .mrpack storage provider (local mirror list or S3 bucket)
//   - the S3-secret "(unchanged — leave empty)" placeholder when a secret is
//     already on file (GET omits the value, returns secretSet=true)
// Pattern mirrors LibraryTab/FeaturesTab: snapshot + useUnsavedChanges so the
// shared dirty-bar in settings/layout.tsx surfaces save/discard.

import React, { useEffect, useRef, useState } from 'react';
import { Package, Plus, X, CircleCheck, CircleAlert, AlertTriangle } from 'lucide-react';
import { SkeletonHeader, SkeletonCard } from '@/components/Skeleton';
import { useUnsavedChanges } from '@/components/settings/UnsavedChanges';
import {
    getModpackSettings, setModpackSettings, getModpackDeliveryCapabilities,
    type ModpackSettings, type DeliveryCapabilities,
} from '@/lib/api/modpackSettings';
import { listStorageConnections, type StorageConnection } from '@/lib/api';

// Whether a Solder delivery mode is currently unusable given the backend's
// probed capabilities. `caps === null` (not yet loaded, or the probe failed)
// fails OPEN — every mode stays enabled rather than greying out on an unknown,
// same reasoning as FeaturesTab's storageConfigured gate.
export function isDeliveryModeDisabled(
    mode: ModpackSettings['solderDeliveryMode'],
    caps: DeliveryCapabilities | null,
): boolean {
    if (mode === 'presigned') return caps?.canPresign === false;
    if (mode === 'public') return caps?.publicConfigured === false || caps?.publicReachable === false;
    return false;
}

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
    connectionId: 0,
    corePublicUrl: '',
    solderMirrorUrl: '',
    solderDeliveryMode: 'core',
};

export default function ModpacksTab() {
    const [settings, setSettings] = useState<ModpackSettings>(DEFAULTS);
    const [secretSet, setSecretSet] = useState(false);
    const [loading, setLoading] = useState(true);
    const [saving, setSaving] = useState(false);
    const [toast, setToast] = useState<{ msg: string; ok: boolean } | null>(null);
    const [connections, setConnections] = useState<StorageConnection[]>([]);
    // Probed once on mount — which delivery modes the backend can actually
    // serve right now, so the radios below can grey out one that would just
    // 500 at Solder-download time. null = not loaded yet / probe failed.
    const [deliveryCaps, setDeliveryCaps] = useState<DeliveryCapabilities | null>(null);

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
        getModpackDeliveryCapabilities()
            .then(res => setDeliveryCaps(res.capabilities ?? null))
            .catch(() => setDeliveryCaps(null));
    }, []);

    useEffect(() => {
        listStorageConnections().then(res => {
            if (res.success && res.connections) setConnections(res.connections);
        });
    }, []);

    const usingConnection = settings.provider === 's3' && (settings.connectionId ?? 0) > 0;

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

                {/* Solder delivery mode — greyed out per-mode from the probed
                    capabilities above, so picking one the current storage
                    config can't serve isn't possible in the first place. */}
                <div>
                    <div className="font-medium text-sm text-(--base-09) mb-2">Solder delivery</div>
                    <div className="flex gap-4">
                        {(['core', 'presigned', 'public'] as const).map(mode => {
                            const disabled = isDeliveryModeDisabled(mode, deliveryCaps);
                            return (
                                <label
                                    key={mode}
                                    className={`flex items-center gap-2 text-sm ${disabled ? 'opacity-40 cursor-not-allowed' : 'cursor-pointer'}`}
                                >
                                    <input
                                        type="radio"
                                        name="solderDeliveryMode"
                                        checked={settings.solderDeliveryMode === mode}
                                        disabled={disabled}
                                        onChange={() => setSettings(s => ({ ...s, solderDeliveryMode: mode }))}
                                        className="accent-(--accent)"
                                    />
                                    <span className="font-mono uppercase tracking-wide">{mode}</span>
                                </label>
                            );
                        })}
                    </div>
                    {deliveryCaps?.canPresign === false && deliveryCaps.notes.presigned && (
                        <p className="flex items-start gap-1.5 text-xs text-(--warning-light) mt-2">
                            <AlertTriangle size={12} className="mt-0.5 shrink-0" />
                            <span>{deliveryCaps.notes.presigned}</span>
                        </p>
                    )}
                    {(deliveryCaps?.publicConfigured === false || deliveryCaps?.publicReachable === false) && deliveryCaps?.notes.public && (
                        <p className="flex items-start gap-1.5 text-xs text-(--warning-light) mt-2">
                            <AlertTriangle size={12} className="mt-0.5 shrink-0" />
                            <span>{deliveryCaps.notes.public}</span>
                        </p>
                    )}
                    {settings.solderDeliveryMode === 'public' && (deliveryCaps?.privatePackCount ?? 0) > 0 && (
                        <p className="flex items-start gap-1.5 text-xs text-(--warning) mt-2">
                            <AlertTriangle size={12} className="mt-0.5 shrink-0" />
                            <span>
                                {deliveryCaps?.privatePackCount} private/hidden Solder pack{(deliveryCaps?.privatePackCount ?? 0) === 1 ? '' : 's'} exist. In public
                                mode their files sit in a publicly readable bucket and can be fetched by anyone who derives the URL; the URL is just not advertised.
                                Use presigned to keep private packs confidential.
                            </span>
                        </p>
                    )}
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

                {/* S3 — a saved connection or inline endpoint + bucket + region + creds */}
                {settings.provider === 's3' && (
                    <div className="space-y-4">
                        <div className="flex flex-col gap-[5px]">
                            <label className="mono-label">Storage connection</label>
                            <select
                                value={settings.connectionId ?? 0}
                                onChange={e => setSettings(s => ({ ...s, connectionId: Number(e.target.value) }))}
                                className="input-field w-full"
                            >
                                <option value={0}>Enter credentials manually</option>
                                {connections.map(c => <option key={c.id} value={c.id}>{c.name}</option>)}
                            </select>
                            <p className="text-xs text-(--base-06)">Reuse a saved connection from Settings -&gt; Storage Connections, or enter S3 credentials inline below.</p>
                        </div>

                        {usingConnection ? (
                            <div className="alert alert-info text-xs">Credentials come from the selected connection. Manage or test it on the Storage Connections page.</div>
                        ) : (
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
                )}
            </div>

            {/* Solder mirror base — the public URL launchers download from */}
            <div className="card p-5 space-y-3">
                <div>
                    <div className="font-medium text-sm text-(--base-09)">Solder download address</div>
                    <div className="text-xs text-(--base-06) mt-0.5 max-w-2xl">
                        {settings.solderDeliveryMode === 'public'
                            ? 'Public mode serves artifacts straight from your bucket or CDN, so launchers need its public base URL. Core never sees these downloads.'
                            : 'Launchers download through Core in this mode, so Core needs to know its own public address. A separate mirror URL is only used by public delivery.'}
                    </div>
                </div>
                {settings.solderDeliveryMode === 'public' ? (
                    <Field
                        label="Mirror URL"
                        value={settings.solderMirrorUrl}
                        onChange={v => setSettings(s => ({ ...s, solderMirrorUrl: v }))}
                        placeholder="https://cdn.example.com/modpacks"
                    />
                ) : (
                    <Field
                        label="Core public URL"
                        value={settings.corePublicUrl}
                        onChange={v => setSettings(s => ({ ...s, corePublicUrl: v }))}
                        placeholder="https://api.example.com"
                    />
                )}
                {((settings.solderDeliveryMode === 'public' ? settings.solderMirrorUrl : settings.corePublicUrl).trim() === '') && (
                    <div className="text-xs text-(--warning-light) italic">
                        {settings.solderDeliveryMode === 'public'
                            ? 'Not set: in public mode the pack list answers 500 and launchers cannot download.'
                            : 'Not set: the pack list answers 500 and launchers cannot download. This is the address Core serves mirror files from.'}
                    </div>
                )}
                {settings.provider !== 's3' && (
                    <p className="text-xs text-(--base-06)">
                        Core serves the mirror itself at <span className="font-mono">{'{url}'}/solder/mirror/</span>,
                        so this is the origin players reach Core on, not an internal address.
                    </p>
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
