"use client";

// Settings tab for the modpack-authoring subsystem. Houses:
//   - the platform-wide feature toggle (gates write endpoints + non-admin UI)
//   - the .mrpack storage provider (local mirror list, or a saved S3 connection)
//   - Solder delivery mode + the public base launchers download from
// S3 credentials are NOT edited here: they live in Settings -> Storage
// Connections and this screen only references one by id.
// Pattern mirrors LibraryTab/FeaturesTab: snapshot + useUnsavedChanges so the
// shared dirty-bar in settings/layout.tsx surfaces save/discard.

import { useEffect, useRef, useState } from 'react';
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

// The backend already knows WHY a mode is unavailable and returns it in
// caps.notes. Surfacing it beside the disabled option is the difference between
// "this is broken" and "point this at a bucket first". Falls back to a generic
// line only when the probe returned no note.
export function deliveryDisabledReason(
    mode: ModpackSettings['solderDeliveryMode'],
    caps: DeliveryCapabilities | null,
): string {
    if (!isDeliveryModeDisabled(mode, caps)) return '';
    if (mode === 'presigned') return caps?.notes?.presigned || 'The current storage backend cannot issue presigned URLs.';
    if (mode === 'public') return caps?.notes?.public || 'No reachable public base URL is configured.';
    return '';
}

const PROVIDER_OPTIONS = [
    { value: 'local' as const, label: 'Local paths', desc: 'Archives on disk, mirrored across the paths below' },
    { value: 's3' as const, label: 'S3 connection', desc: 'A bucket from Settings → Storage Connections' },
];

const DELIVERY_OPTIONS = [
    { value: 'core' as const, label: 'Through Core', desc: 'Core proxies every file. Works everywhere, and all traffic goes through it.' },
    { value: 'presigned' as const, label: 'Presigned', desc: 'Expiring links straight to a private bucket. Core stays out of the transfer.' },
    { value: 'public' as const, label: 'Public base', desc: 'Files served from a public bucket or CDN. Anyone with the URL can fetch them.' },
];

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
    const detectedUrl = (settings.detectedCorePublicUrl ?? '').trim();

    const handleSave = async () => {
        setSaving(true);
        const res = await setModpackSettings(settings);
        if (res.success) {
            flash('Saved.');
            // Re-snapshot with secret blanked — the value was just persisted
            // server-side; we keep the input empty + show "(unchanged)".
            const after: ModpackSettings = { ...settings, s3SecretKey: '' };
            snapshotRef.current = after;
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
                    Storage layout and delivery for modpack archives. The on/off switches live under
                    Settings -&gt; Features.
                </p>
            </div>

            {/* Read-only mirror of the platform flag, NOT a second switch.
                This screen used to carry its own toggle writing the same
                feature_modpacks_enabled key as Settings -> Features, so the
                platform had one switch behind two controls: flipping either
                moved the other, and once authoring became its own flag one of
                the two screens would always show a half-truth. Features owns
                the flags now; this shows the resulting state so the storage
                config below has context. */}
            <div className="card p-5">
                <div className="flex items-center justify-between gap-4">
                    <div>
                        <div className="font-medium text-sm text-(--base-09)">Subsystem state</div>
                        <div className="text-xs text-(--base-06) mt-0.5 max-w-md">
                            {settings.featureEnabled
                                ? 'Modpacks are enabled. Whether users may author (rather than admins only) is the "Open authoring to users" switch under Settings -> Features.'
                                : 'Modpacks are disabled: write endpoints return 503 and the Modpacks nav entry is hidden. Existing modpacks stay readable and downloadable. Enable it under Settings -> Features.'}
                        </div>
                    </div>
                    <span className={`badge shrink-0 ${settings.featureEnabled ? 'badge-accent' : 'badge-neutral'}`}>
                        {settings.featureEnabled ? 'Enabled' : 'Disabled'}
                    </span>
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
                    <div className="font-medium text-sm text-(--base-09) mb-2">Storage provider</div>
                    <div className="grid grid-cols-1 sm:grid-cols-2 gap-2">
                        {PROVIDER_OPTIONS.map(opt => {
                            const active = settings.provider === opt.value;
                            return (
                                <button
                                    key={opt.value}
                                    type="button"
                                    aria-pressed={active}
                                    onClick={() => setSettings(s => ({ ...s, provider: opt.value }))}
                                    className={`p-3 rounded-md border text-left transition-colors ${
                                        active
                                            ? 'border-(--accent) bg-(--accent)/10'
                                            : 'border-(--base-03) bg-(--base-02) hover:border-(--base-05)'
                                    }`}
                                >
                                    <div className={`text-sm font-medium ${active ? 'text-(--accent-light)' : 'text-(--base-09)'}`}>{opt.label}</div>
                                    <div className="text-xs text-(--base-06) mt-0.5">{opt.desc}</div>
                                </button>
                            );
                        })}
                    </div>
                </div>

                {/* Solder delivery mode. Each option carries its own explanation
                    and, when the current storage config cannot serve it, the
                    concrete reason - the probe already knows it, and "greyed out
                    with no reason" is the version of this screen people file
                    bugs about. */}
                <div>
                    <div className="font-medium text-sm text-(--base-09) mb-0.5">Solder delivery</div>
                    <p className="text-xs text-(--base-06) mb-2 max-w-2xl">
                        How a launcher actually fetches mod files. This is independent of where they are
                        stored — it only decides who serves the bytes.
                    </p>
                    <div className="grid grid-cols-1 sm:grid-cols-3 gap-2">
                        {DELIVERY_OPTIONS.map(opt => {
                            const disabled = isDeliveryModeDisabled(opt.value, deliveryCaps);
                            const active = settings.solderDeliveryMode === opt.value;
                            const reason = disabled ? deliveryDisabledReason(opt.value, deliveryCaps) : '';
                            return (
                                <button
                                    key={opt.value}
                                    type="button"
                                    aria-pressed={active}
                                    disabled={disabled}
                                    title={reason || opt.desc}
                                    onClick={() => setSettings(s => ({ ...s, solderDeliveryMode: opt.value }))}
                                    className={`p-3 rounded-md border text-left transition-colors ${
                                        disabled
                                            ? 'border-(--base-03) bg-(--base-02) opacity-50 cursor-not-allowed'
                                            : active
                                                ? 'border-(--accent) bg-(--accent)/10'
                                                : 'border-(--base-03) bg-(--base-02) hover:border-(--base-05)'
                                    }`}
                                >
                                    <div className={`text-sm font-medium ${active && !disabled ? 'text-(--accent-light)' : 'text-(--base-09)'}`}>{opt.label}</div>
                                    <div className="text-xs text-(--base-06) mt-0.5">{opt.desc}</div>
                                    {reason && (
                                        <div className="flex items-start gap-1 text-[11px] text-(--warning-light) mt-1.5">
                                            <AlertTriangle size={10} className="mt-0.5 shrink-0" />
                                            <span>{reason}</span>
                                        </div>
                                    )}
                                </button>
                            );
                        })}
                    </div>
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
                        {/* One place defines an S3 target: Settings → Storage
                            Connections. This screen used to offer endpoint /
                            bucket / region / key / secret inline as well, which
                            meant the same bucket got typed in several tabs, each
                            with its own copy of the credentials and its own
                            chance to be wrong or to be missed on a rotation.
                            There is no "manual" option any more. */}
                        <div className="flex flex-col gap-[5px]">
                            <label className="mono-label">Storage connection</label>
                            <select
                                value={settings.connectionId ?? 0}
                                onChange={e => setSettings(s => ({ ...s, connectionId: Number(e.target.value) }))}
                                className="input-field w-full"
                            >
                                <option value={0}>— select a connection —</option>
                                {connections.map(c => <option key={c.id} value={c.id}>{c.name}</option>)}
                            </select>
                            <p className="text-xs text-(--base-06)">
                                Buckets and credentials are defined once under Settings → Storage Connections
                                and referenced here, so a rotation happens in one place.
                            </p>
                        </div>

                        {usingConnection ? (
                            <div className="alert alert-info text-xs">
                                Credentials come from the selected connection. Manage or test it on the
                                Storage Connections page.
                            </div>
                        ) : connections.length === 0 ? (
                            <div className="alert alert-warning text-xs">
                                <AlertTriangle size={14} className="shrink-0 mt-0.5" />
                                <span>
                                    No storage connections exist yet. Create one under Settings → Storage
                                    Connections, then pick it here — modpack storage cannot use S3 until you do.
                                </span>
                            </div>
                        ) : (
                            <div className="alert alert-warning text-xs">
                                <AlertTriangle size={14} className="shrink-0 mt-0.5" />
                                <span>Pick a connection above — S3 storage is not configured until you do.</span>
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
                    <div className="space-y-1.5">
                        <Field
                            label="Core public URL"
                            value={settings.corePublicUrl}
                            onChange={v => setSettings(s => ({ ...s, corePublicUrl: v }))}
                            placeholder="https://api.example.com"
                        />
                        {/* Core reports the origin this very request came in on.
                            Offered, never applied on its own: an admin reaching
                            Core over localhost or a service name would otherwise
                            silently persist an address no launcher can resolve. */}
                        {detectedUrl && detectedUrl !== settings.corePublicUrl.trim() && (
                            <button
                                type="button"
                                onClick={() => setSettings(s => ({ ...s, corePublicUrl: detectedUrl }))}
                                className="btn btn-secondary btn-sm"
                            >
                                Use the address you are on: <span className="font-mono ml-1">{detectedUrl}</span>
                            </button>
                        )}
                    </div>
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
