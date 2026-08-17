"use client";

import { useState, useEffect, useRef } from 'react';
import { useAppData } from '@/lib/AppDataContext';
import { AlertTriangle, CircleCheck, CircleAlert } from 'lucide-react';
import { API_URL } from '@/lib/api';
import { SkeletonHeader, SkeletonCard } from '@/components/Skeleton';
import { useUnsavedChanges } from '@/components/settings/UnsavedChanges';

// ─────────────────────────────────────────────
// Beam settings
// ─────────────────────────────────────────────

interface BeamSettings {
    relayAddress: string;
    bwLimit: number;
    enabled: boolean;
    downloadLink: string;

    // Force-update floor (empty = gating off). Clients below this get HTTP 426
    // from GetBeamTicket and a blocking update screen. Validated server-side.
    minVersion?: string;

    // How the floor is chosen: 'manual' uses minVersion above; 'auto' follows the
    // minVersion baked into the SIGNED release manifest (Core verifies it). In
    // auto mode the manual input is ignored.
    minVersionMode?: 'manual' | 'auto';

    // Who may opt into the dev (prerelease) update channel from their profile:
    // 'disabled' (default), 'admins-only', or 'all-users'.
    devChannelAccess?: 'disabled' | 'admins-only' | 'all-users';

    // Per-direction throttle splits (bytes/sec, 0 = unlimited). Stored
    // alongside bwLimit; Core folds the internal pair into bwLimit until
    // the per-direction enforcement ships.
    bwUpInternal?: number;
    bwDownInternal?: number;
    bwUpExternal?: number;
    bwDownExternal?: number;

    // Operator-recorded host hardware references. Pure informational —
    // never enforced. Surfaced as captions on the throttle inputs so
    // admins can size limits against what the host actually provides.
    refUpInternal?: number;
    refDownInternal?: number;
    refUpExternal?: number;
    refDownExternal?: number;

    // Upload limits (bytes, 0 = unlimited), enforced node-side on the beam
    // upload path. maxUploadBytes is an absolute per-upload cap; dailyUploadBytes
    // is a per-user daily total.
    maxUploadBytes?: number;
    dailyUploadBytes?: number;
}

async function getBeamSettings(): Promise<{ success: boolean; settings?: BeamSettings }> {
    try {
        const token = localStorage.getItem('authToken') || localStorage.getItem('token');
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
        const res = await fetch(`${API_URL}/settings/beam`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${token}` },
            body: JSON.stringify(settings),
        });
        return await res.json();
    } catch {
        return { success: false, message: 'Network error' };
    }
}

// ─────────────────────────────────────────────
// Unit helpers
// ─────────────────────────────────────────────

// Bandwidth values flow as bytes/sec through the API but the operator
// thinks in Mbit/s — this is the canonical UI unit. 1 Mbit/s = 125,000
// bytes/sec (decimal Mbit, matches what hosters advertise on uplinks).
const MBIT_TO_BPS = 125_000;
function mbitToBps(mbit: number): number {
    if (!Number.isFinite(mbit) || mbit <= 0) return 0;
    return Math.round(mbit * MBIT_TO_BPS);
}
function bpsToMbit(bps: number | undefined): number {
    if (!bps || bps <= 0) return 0;
    return Math.round(bps / MBIT_TO_BPS);
}

// Upload limits are stored in bytes; the UI edits them in GiB. 0 = unlimited.
const GIB_TO_BYTES = 1024 * 1024 * 1024;
function bytesToGiB(bytes: number | undefined): number {
    if (!bytes || bytes <= 0) return 0;
    return Math.round((bytes / GIB_TO_BYTES) * 100) / 100; // 2 decimals
}
function giBToBytes(gib: number): number {
    if (!gib || gib <= 0) return 0;
    return Math.round(gib * GIB_TO_BYTES);
}

// BwField is one cell of the per-direction throttle grid: a single
// Mbit/s number input with the operator's matching hardware-reference
// value (refValue) surfaced as a caption underneath, so caps get
// chosen against real link capacity instead of typed blind.
function BwField({
    label,
    value,
    refValue,
    onChange,
}: {
    label: string;
    value: number;
    refValue?: number;
    onChange: (v: number) => void;
}) {
    const refMbit = refValue ? Math.round(refValue / MBIT_TO_BPS) : 0;
    return (
        <div className="flex flex-col gap-[5px]">
            <label className="input-label">{label}</label>
            <div className="flex items-center gap-2">
                <input
                    type="number"
                    min={0}
                    step={1}
                    value={value || ''}
                    onChange={e => onChange(Math.max(0, parseInt(e.target.value) || 0))}
                    placeholder="0"
                    className="input-field w-28 text-right"
                />
                <span className="text-[11px] font-mono text-(--base-06)">Mbit/s</span>
            </div>
            <p className="text-[10px] text-(--base-06)">
                {value === 0 ? 'Unlimited' : `Capped at ${value} Mbit/s`}
                {refMbit > 0 ? ` · host: ${refMbit} Mbit/s` : ''}
            </p>
        </div>
    );
}

function RefField({
    label,
    value,
    onChange,
}: {
    label: string;
    value: number;
    onChange: (v: number) => void;
}) {
    return (
        <div className="flex flex-col gap-[5px]">
            <label className="input-label">{label}</label>
            <div className="flex items-center gap-2">
                <input
                    type="number"
                    min={0}
                    step={1}
                    value={value || ''}
                    onChange={e => onChange(Math.max(0, parseInt(e.target.value) || 0))}
                    placeholder="0"
                    className="input-field w-28 text-right"
                />
                <span className="text-[11px] font-mono text-(--base-06)">Mbit/s</span>
            </div>
        </div>
    );
}

// Snapshot of every field a dirty-check should follow. Everything is
// numeric in bytes/sec for the bw/ref fields so the JSON.stringify
// equality check is exact.
interface BeamEditableSnapshot {
    relayAddress: string;
    downloadLink: string;
    minVersion: string;
    minVersionMode: 'manual' | 'auto';
    devChannelAccess: 'disabled' | 'admins-only' | 'all-users';
    enabled: boolean;
    bwUpInternal: number;
    bwDownInternal: number;
    bwUpExternal: number;
    bwDownExternal: number;
    refUpInternal: number;
    refDownInternal: number;
    refUpExternal: number;
    refDownExternal: number;
    maxUploadBytes: number;
    dailyUploadBytes: number;
}

function beamSnapshot(s: BeamSettings): BeamEditableSnapshot {
    return {
        relayAddress: s.relayAddress,
        downloadLink: s.downloadLink,
        minVersion: s.minVersion ?? '',
        minVersionMode: s.minVersionMode === 'auto' ? 'auto' : 'manual',
        devChannelAccess:
            s.devChannelAccess === 'admins-only' || s.devChannelAccess === 'all-users'
                ? s.devChannelAccess
                : 'disabled',
        enabled: s.enabled,
        bwUpInternal: s.bwUpInternal ?? 0,
        bwDownInternal: s.bwDownInternal ?? 0,
        bwUpExternal: s.bwUpExternal ?? 0,
        bwDownExternal: s.bwDownExternal ?? 0,
        refUpInternal: s.refUpInternal ?? 0,
        refDownInternal: s.refDownInternal ?? 0,
        refUpExternal: s.refUpExternal ?? 0,
        refDownExternal: s.refDownExternal ?? 0,
        maxUploadBytes: s.maxUploadBytes ?? 0,
        dailyUploadBytes: s.dailyUploadBytes ?? 0,
    };
}

// ─────────────────────────────────────────────
// Beam panel
// ─────────────────────────────────────────────

function BeamPanel({ showToast }: { showToast: (msg: string, ok?: boolean) => void }) {
    const { gatewayEnabled } = useAppData();
    const [settings, setSettings] = useState<BeamSettings>({
        relayAddress: '',
        bwLimit: 0,
        enabled: true,
        downloadLink: '',
        minVersion: '',
        minVersionMode: 'manual',
        devChannelAccess: 'disabled',
    });
    const [loading, setLoading] = useState(true);
    const [saving, setSaving] = useState(false);
    // The download-link field stays read-only until the admin acknowledges what a
    // custom link costs. Session-scoped on purpose: it guards the decision at the
    // moment it is made, so there is nothing worth persisting.
    const [downloadLinkUnlocked, setDownloadLinkUnlocked] = useState(false);
    const [showDownloadLinkWarning, setShowDownloadLinkWarning] = useState(false);

    // Snapshot of last-saved editable fields for dirty detection.
    const snapshotRef = useRef<BeamEditableSnapshot | null>(null);

    useEffect(() => {
        getBeamSettings().then(res => {
            if (res.success && res.settings) {
                setSettings(res.settings);
                snapshotRef.current = beamSnapshot(res.settings);
            } else {
                // snapshotRef stays null, so `dirty` stays false and the save
                // bar never appears - the seed values cannot be written back.
                // Say the load failed rather than presenting them as stored.
                showToast('Failed to load Beam settings - shown values are unconfirmed', false);
            }
            setLoading(false);
        });
    }, []);

    const handleSave = async () => {
        setSaving(true);
        // bwLimit stays in the payload for back-compat — Core will overwrite
        // it from min(bwUpInternal, bwDownInternal) when those are non-zero,
        // so we just pass through whatever the API loaded.
        const res = await saveBeamSettings(settings);
        showToast(res.success ? 'Beam settings saved.' : (res.message || 'Save failed.'), res.success);
        if (res.success) {
            snapshotRef.current = beamSnapshot(settings);
        }
        setSaving(false);
    };

    const handleDiscard = () => {
        const snap = snapshotRef.current;
        if (!snap) return;
        setSettings(s => ({
            ...s,
            relayAddress: snap.relayAddress,
            downloadLink: snap.downloadLink,
            minVersion: snap.minVersion,
            minVersionMode: snap.minVersionMode,
            devChannelAccess: snap.devChannelAccess,
            enabled: snap.enabled,
            bwUpInternal: snap.bwUpInternal,
            bwDownInternal: snap.bwDownInternal,
            bwUpExternal: snap.bwUpExternal,
            bwDownExternal: snap.bwDownExternal,
            refUpInternal: snap.refUpInternal,
            refDownInternal: snap.refDownInternal,
            refUpExternal: snap.refUpExternal,
            refDownExternal: snap.refDownExternal,
            maxUploadBytes: snap.maxUploadBytes,
            dailyUploadBytes: snap.dailyUploadBytes,
        }));
    };

    const dirty =
        snapshotRef.current !== null &&
        JSON.stringify(beamSnapshot(settings)) !== JSON.stringify(snapshotRef.current);

    useUnsavedChanges({ dirty, save: handleSave, discard: handleDiscard, saving });

    // ─── Per-direction bandwidth input ──────────────────────────────
    // Renders a labelled Mbit/s input with the operator's reference
    // value as an info caption below ("Host says: 1000 Mbit/s") so
    // limits get sized against real hardware, not guesses.
    type BwKey = 'bwUpInternal' | 'bwDownInternal' | 'bwUpExternal' | 'bwDownExternal';
    type RefKey = 'refUpInternal' | 'refDownInternal' | 'refUpExternal' | 'refDownExternal';
    const setBwField = (k: BwKey, mbit: number) =>
        setSettings(s => ({ ...s, [k]: mbitToBps(mbit) }));
    const setRefField = (k: RefKey, mbit: number) =>
        setSettings(s => ({ ...s, [k]: mbitToBps(mbit) }));

    const setUploadLimit = (k: 'maxUploadBytes' | 'dailyUploadBytes', gib: number) =>
        setSettings(s => ({ ...s, [k]: giBToBytes(gib) }));

    if (loading) return (
        <div className="space-y-6">
            <SkeletonHeader />
            <SkeletonCard height="h-72" />
            <SkeletonCard height="h-64" />
            <SkeletonCard height="h-64" />
        </div>
    );

    return (
        <>
        <div className="space-y-6">
            <div>
                <h2 className="text-base font-display font-bold text-(--base-09) mb-1">Beam Desktop Client</h2>
                <p className="text-sm text-(--base-07)">
                    Beam is DYLARIS&apos;s built-in desktop client for file transfer and server console. It always works
                    over the local network or a direct connection, with no gateway required - Core mints the connection
                    ticket and the node authenticates it. The gateway only adds an optional encrypted relay so users can
                    reach their servers remotely.
                </p>
            </div>

            <div className="space-y-6">

            <div className="card p-5 space-y-4">
                <h3 className="text-sm font-display font-semibold text-(--accent-light) mb-2">General</h3>
                <div className="flex items-center justify-between">
                    <div className="pr-4">
                        <label className="input-label">Offer Beam to users</label>
                        <p className="text-xs text-(--base-06) mt-0.5">
                            Advisory only. Shows or hides the Download Beam button and reports availability to the desktop
                            app. It does not disable Beam itself - Core still mints connection tickets and the node still
                            authenticates, so clients that already have Beam keep working either way.
                        </p>
                    </div>
                    <button
                        onClick={() => setSettings(s => ({ ...s, enabled: !s.enabled }))}
                        className={`toggle-track ${settings.enabled ? 'toggle-track-on' : 'toggle-track-off'}`}
                        role="switch"
                        aria-checked={settings.enabled}
                    >
                        <span className={`toggle-knob ${settings.enabled ? 'toggle-knob-on' : 'toggle-knob-off'}`} />
                    </button>
                </div>
                <div className="flex flex-col gap-[5px]">
                    <label className="input-label">Relay Address</label>
                    <p className="text-xs text-(--base-06) mb-1">
                        Public address of the Beam relay (e.g. <span className="font-mono">beam.example.com:25550</span>).
                        Only used for remote access in gateway routing mode - the desktop app fetches it on login. LAN and
                        direct connections work without it.
                    </p>
                    <input
                        type="text"
                        value={settings.relayAddress}
                        onChange={e => setSettings(s => ({ ...s, relayAddress: e.target.value }))}
                        placeholder="beam.example.com:25550"
                        className="input-field"
                    />
                    {/* A relay only exists inside the gateway subsystem, so with
                        routing on ip_port there is nothing missing - the field
                        stays editable (an admin may set it up before switching
                        routing on) but warning about it would be noise. */}
                    {!settings.relayAddress?.trim() && (
                        gatewayEnabled ? (
                            <p className="flex items-start gap-1.5 text-xs text-(--base-06) mt-1">
                                <AlertTriangle size={12} className="mt-0.5 shrink-0 text-(--warning-light)" />
                                <span>No relay address set. Remote access via the gateway is unavailable until you add one; LAN and direct connections are unaffected.</span>
                            </p>
                        ) : (
                            <p className="text-xs text-(--base-06) mt-1">
                                Not needed while Game Traffic is on IP:Port — Beam connects over LAN or directly.
                            </p>
                        )
                    )}
                </div>
                <div className="flex flex-col gap-[5px]">
                    <label className="input-label">Beam Download Link</label>
                    <p className="text-xs text-(--base-06) mb-1">
                        Where the Files tab sends users to install Beam Desktop. Leave it EMPTY unless you build
                        your own Beam: empty means users get the official signed build, which this Core verifies
                        against the release manifest before it will let a client connect.
                    </p>
                    <input
                        type="text"
                        value={settings.downloadLink}
                        // Locked until acknowledged. Pointing this at your own URL
                        // replaces a signed, verified download with one nothing
                        // checks, and that is not visible from a text field with a
                        // URL placeholder - which is all it was.
                        readOnly={!downloadLinkUnlocked}
                        onFocus={() => { if (!downloadLinkUnlocked) setShowDownloadLinkWarning(true); }}
                        onClick={() => { if (!downloadLinkUnlocked) setShowDownloadLinkWarning(true); }}
                        onChange={e => setSettings(s => ({ ...s, downloadLink: e.target.value }))}
                        placeholder="Empty — users get the official build"
                        aria-describedby="beam-download-link-help"
                        className={`input-field ${downloadLinkUnlocked ? '' : 'cursor-pointer'}`}
                    />
                    <p id="beam-download-link-help" className="text-xs text-(--base-06) mt-0.5">
                        {downloadLinkUnlocked
                            ? 'Custom link active. Core cannot verify a build it does not publish — you are responsible for what this URL serves.'
                            : 'Only needed if you ship your own Beam build or a fork. Click the field to change it.'}
                    </p>
                </div>
                <div className="flex flex-col gap-[5px]">
                    <label className="input-label">Minimum Beam Version</label>
                    <div className="grid grid-cols-2 gap-2 mb-1">
                        {([
                            { value: 'manual' as const, label: 'Manual', desc: 'Use the version set below' },
                            { value: 'auto' as const, label: 'Auto', desc: 'Follow the signed release manifest' },
                        ]).map(opt => {
                            const active = (settings.minVersionMode ?? 'manual') === opt.value;
                            return (
                                <button key={opt.value} type="button"
                                    onClick={() => setSettings(s => ({ ...s, minVersionMode: opt.value }))}
                                    className={`p-3 rounded-md border text-left transition-colors ${active ? 'border-(--accent) bg-(--accent)/10' : 'border-(--base-03) bg-(--base-02) hover:border-(--base-05)'}`}>
                                    <div className={`text-sm font-medium ${active ? 'text-(--accent-light)' : 'text-(--base-09)'}`}>{opt.label}</div>
                                    <div className="text-xs text-(--base-06) mt-0.5">{opt.desc}</div>
                                </button>
                            );
                        })}
                    </div>
                    <input
                        type="text"
                        value={settings.minVersion ?? ''}
                        onChange={e => setSettings(s => ({ ...s, minVersion: e.target.value.trim() }))}
                        placeholder="e.g. 1.2.3"
                        disabled={(settings.minVersionMode ?? 'manual') === 'auto'}
                        className="input-field input-mono disabled:opacity-50 disabled:cursor-not-allowed"
                    />
                    <p className="text-xs text-(--base-06) mt-0.5">
                        {(settings.minVersionMode ?? 'manual') === 'auto'
                            ? 'Core reads the floor from the signed release manifest (minVersion) and verifies it before enforcing. The field above is ignored.'
                            : 'Clients below this version cannot connect and must update. Leave empty to disable.'}
                    </p>
                </div>
                <div className="flex flex-col gap-[5px]">
                    <label className="input-label">Dev Channel Access</label>
                    <div className="grid grid-cols-3 gap-2">
                        {([
                            { value: 'disabled' as const, label: 'Disabled', desc: 'Nobody can opt in' },
                            { value: 'admins-only' as const, label: 'Admins only', desc: 'Admins can opt in' },
                            { value: 'all-users' as const, label: 'All users', desc: 'Anyone can opt in' },
                        ]).map(opt => {
                            const active = (settings.devChannelAccess ?? 'disabled') === opt.value;
                            return (
                                <button key={opt.value} type="button"
                                    onClick={() => setSettings(s => ({ ...s, devChannelAccess: opt.value }))}
                                    className={`p-3 rounded-md border text-left transition-colors ${active ? 'border-(--accent) bg-(--accent)/10' : 'border-(--base-03) bg-(--base-02) hover:border-(--base-05)'}`}>
                                    <div className={`text-sm font-medium ${active ? 'text-(--accent-light)' : 'text-(--base-09)'}`}>{opt.label}</div>
                                    <div className="text-xs text-(--base-06) mt-0.5">{opt.desc}</div>
                                </button>
                            );
                        })}
                    </div>
                    <p className="text-xs text-(--base-06) mt-0.5">
                        Who may switch their Beam desktop app to the dev (prerelease) update channel from their profile. Dev builds are unstable test releases.
                    </p>
                </div>
            </div>

            {/* ─── Throttle (enforced) ─── */}
            <div className="card p-5 space-y-4">
                <div>
                    <h3 className="text-sm font-display font-semibold text-(--base-08) mb-1">Bandwidth Throttle</h3>
                    <p className="text-xs text-(--base-06)">
                        Caps applied across all Beam transfers. Values in <span className="font-mono">Mbit/s</span> — <strong>0 = unlimited</strong>.
                        Fair sharing is automatic within each cap (max-min via rate.Limiter).
                    </p>
                    <p className="text-[11px] text-(--base-05) mt-1.5">
                        Note: until the per-direction limiter ships in node + relay, the lower of <em>Up Internal</em> and <em>Down Internal</em> is folded into the legacy single throttle and applied symmetrically on the Node. The external pair is stored for the upcoming relay-side throttle.
                    </p>
                </div>

                <div>
                    <h4 className="mono-label mb-2">Internal (Node ↔ Relay over the overlay)</h4>
                    <div className="grid grid-cols-2 gap-3">
                        <BwField
                            label="Up"
                            value={bpsToMbit(settings.bwUpInternal)}
                            refValue={settings.refUpInternal}
                            onChange={v => setBwField('bwUpInternal', v)}
                        />
                        <BwField
                            label="Down"
                            value={bpsToMbit(settings.bwDownInternal)}
                            refValue={settings.refDownInternal}
                            onChange={v => setBwField('bwDownInternal', v)}
                        />
                    </div>
                </div>

                <div>
                    <h4 className="mono-label mb-2">External (Relay ↔ Beam.exe over the internet)</h4>
                    <div className="grid grid-cols-2 gap-3">
                        <BwField
                            label="Up"
                            value={bpsToMbit(settings.bwUpExternal)}
                            refValue={settings.refUpExternal}
                            onChange={v => setBwField('bwUpExternal', v)}
                        />
                        <BwField
                            label="Down"
                            value={bpsToMbit(settings.bwDownExternal)}
                            refValue={settings.refDownExternal}
                            onChange={v => setBwField('bwDownExternal', v)}
                        />
                    </div>
                </div>
            </div>

            {/* ─── Upload limits (enforced node-side) ─── */}
            <div className="card p-5 space-y-4">
                <div>
                    <h3 className="text-sm font-display font-semibold text-(--base-08) mb-1">Upload Limits</h3>
                    <p className="text-xs text-(--base-06)">
                        Caps on files pushed to a node over Beam (server import, file browser). Values in <span className="font-mono">GiB</span> — <strong>0 = unlimited</strong>. Enforced on the node, which Core&apos;s HTTP body-size cap never sees. Downloads are not counted.
                    </p>
                </div>
                <div className="grid grid-cols-2 gap-3">
                    <div className="flex flex-col gap-[5px]">
                        <label className="input-label">Max per upload</label>
                        <input
                            type="number" min={0} step={0.1}
                            value={bytesToGiB(settings.maxUploadBytes) || ''}
                            onChange={e => setUploadLimit('maxUploadBytes', parseFloat(e.target.value) || 0)}
                            placeholder="0"
                            className="input-field input-mono"
                        />
                        <p className="text-xs text-(--base-06)">Rejects any single upload larger than this.</p>
                    </div>
                    <div className="flex flex-col gap-[5px]">
                        <label className="input-label">Daily per user</label>
                        <input
                            type="number" min={0} step={0.1}
                            value={bytesToGiB(settings.dailyUploadBytes) || ''}
                            onChange={e => setUploadLimit('dailyUploadBytes', parseFloat(e.target.value) || 0)}
                            placeholder="0"
                            className="input-field input-mono"
                        />
                        <p className="text-xs text-(--base-06)">Total a user may upload per calendar day (UTC).</p>
                    </div>
                </div>
            </div>

            {/* ─── Reference (host hardware — informational only) ─── */}
            <div className="card p-5 space-y-4">
                <div>
                    <h3 className="text-sm font-display font-semibold text-(--base-08) mb-1">Host Hardware Reference</h3>
                    <p className="text-xs text-(--base-06)">
                        Record what the host actually provides. Pure informational — never enforced. Helps you size the throttle values above against what the link can really do. Values in <span className="font-mono">Mbit/s</span>, leave at 0 if unknown.
                    </p>
                </div>

                <div>
                    <h4 className="mono-label mb-2">Internal (datacenter network)</h4>
                    <div className="grid grid-cols-2 gap-3">
                        <RefField
                            label="Up"
                            value={bpsToMbit(settings.refUpInternal)}
                            onChange={v => setRefField('refUpInternal', v)}
                        />
                        <RefField
                            label="Down"
                            value={bpsToMbit(settings.refDownInternal)}
                            onChange={v => setRefField('refDownInternal', v)}
                        />
                    </div>
                </div>

                <div>
                    <h4 className="mono-label mb-2">External (public uplink)</h4>
                    <div className="grid grid-cols-2 gap-3">
                        <RefField
                            label="Up"
                            value={bpsToMbit(settings.refUpExternal)}
                            onChange={v => setRefField('refUpExternal', v)}
                        />
                        <RefField
                            label="Down"
                            value={bpsToMbit(settings.refDownExternal)}
                            onChange={v => setRefField('refDownExternal', v)}
                        />
                    </div>
                </div>
            </div>

            </div>
        </div>

        {showDownloadLinkWarning && (
            <div className="modal-overlay animate-fade-in" onClick={() => setShowDownloadLinkWarning(false)}>
                <div className="modal-panel max-w-md" onClick={e => e.stopPropagation()}>
                    <div className="modal-header">
                        <h3 className="modal-title flex items-center gap-2 text-(--warning-light)">
                            <AlertTriangle size={16} /> Use your own download link?
                        </h3>
                    </div>
                    <div className="modal-body space-y-3 text-sm text-(--base-07)">
                        <p>
                            You only need this if you build and host Beam Desktop yourself, or run a fork.
                        </p>
                        <p>
                            Leaving it empty is the normal setup: users get the official build, and Core verifies
                            it against the signed release manifest. A custom URL bypasses that check — Core cannot
                            verify a build it does not publish, and everyone who installs from here trusts whatever
                            that URL serves.
                        </p>
                    </div>
                    <div className="modal-footer">
                        <button onClick={() => setShowDownloadLinkWarning(false)} className="btn btn-secondary">
                            Cancel
                        </button>
                        <button
                            onClick={() => { setDownloadLinkUnlocked(true); setShowDownloadLinkWarning(false); }}
                            className="btn btn-primary"
                        >
                            I build my own Beam
                        </button>
                    </div>
                </div>
            </div>
        )}
        </>
    );
}

// ─────────────────────────────────────────────
// Main export: standalone Beam settings tab
// ─────────────────────────────────────────────

export default function BeamTab() {
    const [toast, setToast] = useState<{ msg: string; ok: boolean } | null>(null);

    const showToast = (msg: string, ok = true) => {
        setToast({ msg, ok });
        setTimeout(() => setToast(null), 3500);
    };

    return (
        <>
            <BeamPanel showToast={showToast} />

            {toast && (
                <div className="toast-container">
                    <div className="toast">
                        <div className={`toast-bar ${toast.ok ? 'bg-(--success-light)' : 'bg-(--error-light)'}`}></div>
                        {toast.ok ? <CircleCheck size={14} /> : <CircleAlert size={14} />}
                        <span className="text-sm text-(--base-09)">{toast.msg}</span>
                    </div>
                </div>
            )}
        </>
    );
}
