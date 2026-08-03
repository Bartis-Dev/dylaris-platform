"use client";

import { useState, useEffect, useRef, useCallback } from 'react';
import {
    AlertTriangle,
    CircleCheck,
    CircleAlert,
    Info,
    Loader2,
    Plus,
    RefreshCw,
    Lock,
    X,
} from 'lucide-react';
import { API_URL } from '@/lib/api';
import { SkeletonHeader, SkeletonCard } from '@/components/Skeleton';
import { useUnsavedChanges } from '@/components/settings/UnsavedChanges';
import Select from '@/components/ui/Select';
import {
    zoneHint,
    resolveZone,
    normalizeDNSName,
    originLabel,
    addZoneTo,
    removeZoneFrom,
    type DNSZonesResponse,
    type DNSZoneState,
} from './dnsZones';

// ─────────────────────────────────────────────
// DNS updater settings
// ─────────────────────────────────────────────

interface DNSStatus {
    lastRunAt: string;
    ok: boolean;
    error?: string;
    managedNames: string[];
    recordCount: number;
}

// ManagedName is one name in effect, labelled by where it came from. Without
// this display the failure mode is silent: an admin ticks domains in the panel
// while a leftover EDGE_WILDCARD quietly keeps winning, and the screen looks
// correct either way.
interface ManagedName {
    name: string;
    region: string;
    zone: string;
    // 'relay' names come from a beam relay's own BEAM_PUBLIC_HOST and are not
    // selectable here, so they render as read-only entries.
    origin: 'panel' | 'edge' | 'relay';
    // False when the name sits outside every managed zone. It is advertised and
    // will never be written, which is exactly what has to be visible.
    routable: boolean;
}

interface DNSSettings {
    enabled: boolean;
    provider: string;
    zones: string[];
    regionNames: Record<string, string[]>;
    graceMinutes: number;
    managedNames: ManagedName[];
    tokenSet: boolean;
    // Which configuration is in effect. 'env' means the environment supplies the
    // credential and the panel cannot change it.
    source: 'env' | 'panel' | 'none';
    envManaged: boolean;
    providers: string[];
    status?: DNSStatus;
}

function authHeaders(): Record<string, string> {
    const token = localStorage.getItem('authToken') || localStorage.getItem('token');
    return { Authorization: `Bearer ${token}` };
}

async function getDNSSettings(): Promise<{ success: boolean; settings?: DNSSettings }> {
    try {
        const res = await fetch(`${API_URL}/settings/dns`, { headers: authHeaders() });
        const body = await res.json();
        return { success: !!body.success, settings: body.success ? body : undefined };
    } catch {
        return { success: false };
    }
}

async function saveDNSSettings(payload: {
    enabled: boolean;
    provider: string;
    zones: string[];
    regionNames: Record<string, string[]>;
    graceMinutes: number;
    token: string;
    clearToken: boolean;
}): Promise<{ success: boolean; message?: string; settings?: DNSSettings }> {
    try {
        const res = await fetch(`${API_URL}/settings/dns`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json', ...authHeaders() },
            body: JSON.stringify(payload),
        });
        const body = await res.json();
        if (!res.ok || !body.success) {
            return { success: false, message: body.error || body.message || 'Save failed.' };
        }
        return { success: true, settings: body };
    } catch {
        return { success: false, message: 'Network error' };
    }
}

async function listDNSZones(): Promise<DNSZonesResponse> {
    try {
        const res = await fetch(`${API_URL}/settings/dns/zones`, { headers: authHeaders() });
        const body = await res.json();
        return {
            success: !!body.success,
            state: (body.state as DNSZoneState) ?? 'error',
            zones: Array.isArray(body.zones) ? body.zones : [],
            error: body.error,
        };
    } catch {
        return { success: false, state: 'error', zones: [], error: 'Network error' };
    }
}

function formatWhen(iso: string): string {
    const t = new Date(iso).getTime();
    if (!Number.isFinite(t)) return 'unknown';
    const secs = Math.max(0, Math.round((Date.now() - t) / 1000));
    if (secs < 60) return `${secs}s ago`;
    if (secs < 3600) return `${Math.round(secs / 60)}m ago`;
    if (secs < 86400) return `${Math.round(secs / 3600)}h ago`;
    return `${Math.round(secs / 86400)}d ago`;
}

interface DNSSnapshot {
    enabled: boolean;
    provider: string;
    zones: string[];
    regionNames: Record<string, string[]>;
    graceMinutes: number;
    token: string;
    clearToken: boolean;
}

// RegionNameEditor edits the names one region serves. Several names per region
// is the point of the feature: a hoster offering two brands lists both, and
// every online edge in the region answers for both.
function RegionNameEditor({
    region,
    names,
    zones,
    onChange,
}: {
    region: string;
    names: string[];
    zones: string[];
    onChange: (names: string[]) => void;
}) {
    const [draft, setDraft] = useState('');
    const normalized = normalizeDNSName(draft);
    // A name outside every managed zone is rejected on save, so it is refused
    // here with the reason next to the field rather than after a round trip.
    const outsideZones = normalized !== '' && resolveZone(normalized, zones) === '';
    const duplicate = normalized !== '' && names.includes(normalized);

    const add = () => {
        if (!normalized || outsideZones || duplicate) return;
        onChange([...names, normalized].sort());
        setDraft('');
    };

    return (
        <div className="rounded-md border border-(--base-03) bg-(--base-02) p-3 flex flex-col gap-2">
            <span className="font-mono text-[11px] uppercase tracking-[0.08em] text-(--base-07)">
                {region}
            </span>

            {names.length > 0 ? (
                <ul className="flex flex-wrap gap-1.5">
                    {names.map(n => (
                        <li
                            key={n}
                            className="inline-flex items-center gap-1.5 rounded border border-(--base-03) bg-(--base-01) px-2 py-1"
                        >
                            <span className="font-mono text-[11px] text-(--base-08)">{n}</span>
                            <button
                                type="button"
                                onClick={() => onChange(names.filter(x => x !== n))}
                                aria-label={`Remove ${n} from ${region}`}
                                className="focus-ring text-(--base-06) hover:text-(--error-light) transition-colors"
                            >
                                <X size={11} />
                            </button>
                        </li>
                    ))}
                </ul>
            ) : (
                <p className="text-[11px] text-(--base-06)">
                    Falling back to this region&apos;s <span className="font-mono">EDGE_WILDCARD</span>.
                </p>
            )}

            <div className="flex items-center gap-2">
                <input
                    type="text"
                    value={draft}
                    onChange={e => setDraft(e.target.value)}
                    onKeyDown={e => {
                        if (e.key === 'Enter') {
                            e.preventDefault();
                            add();
                        }
                    }}
                    placeholder={`*.${region}.example.com`}
                    className="input-field input-mono flex-1 text-xs py-1.5"
                    aria-label={`Add a name for ${region}`}
                />
                <button
                    type="button"
                    onClick={add}
                    disabled={!normalized || outsideZones || duplicate}
                    className="btn btn-secondary text-xs py-1.5 px-3 disabled:opacity-40 disabled:cursor-not-allowed"
                >
                    Add
                </button>
            </div>

            {outsideZones && (
                <p className="flex items-start gap-1.5 text-[11px] text-(--warning-light)">
                    <AlertTriangle size={11} className="mt-0.5 shrink-0" />
                    <span>Not inside any managed zone. Add its zone above first.</span>
                </p>
            )}
            {duplicate && (
                <p className="text-[11px] text-(--base-06)">Already listed for this region.</p>
            )}
        </div>
    );
}

function DNSPanel({ showToast }: { showToast: (msg: string, ok?: boolean) => void }) {
    const [settings, setSettings] = useState<DNSSettings | null>(null);
    const [loading, setLoading] = useState(true);
    const [saving, setSaving] = useState(false);

    // Editable state. token is write-only: it starts empty on every load and an
    // empty value means "keep the stored credential" server-side.
    const [enabled, setEnabled] = useState(false);
    const [provider, setProvider] = useState('cloudflare');
    const [zones, setZones] = useState<string[]>([]);
    const [regionNames, setRegionNames] = useState<Record<string, string[]>>({});
    const [graceMinutes, setGraceMinutes] = useState(15);
    const [token, setToken] = useState('');
    const [clearToken, setClearToken] = useState(false);

    const [discovered, setDiscovered] = useState<DNSZonesResponse | null>(null);
    const [zonesLoading, setZonesLoading] = useState(false);
    const [zoneDraft, setZoneDraft] = useState('');

    const snapshotRef = useRef<DNSSnapshot | null>(null);

    const applySettings = useCallback((s: DNSSettings) => {
        setSettings(s);
        setEnabled(s.enabled);
        setProvider(s.provider || 'cloudflare');
        setZones(s.zones ?? []);
        setRegionNames(s.regionNames ?? {});
        setGraceMinutes(s.graceMinutes || 15);
        setToken('');
        setClearToken(false);
        setZoneDraft('');
        snapshotRef.current = {
            enabled: s.enabled,
            provider: s.provider || 'cloudflare',
            zones: s.zones ?? [],
            regionNames: s.regionNames ?? {},
            graceMinutes: s.graceMinutes || 15,
            token: '',
            clearToken: false,
        };
    }, []);

    useEffect(() => {
        getDNSSettings().then(res => {
            if (res.success && res.settings) {
                applySettings(res.settings);
            } else {
                // Without this the panel renders the seed state - DNS disabled,
                // no zones - as though that were the stored config, on the one
                // screen where removing a zone later DELETES its records.
                // applySettings is what sets snapshotRef, so it stays null here
                // and `dirty` stays false: the save bar never appears and these
                // unconfirmed values cannot be written back. Only the display
                // was lying.
                showToast('Failed to load DNS settings - shown values are unconfirmed', false);
            }
            setLoading(false);
        });
    }, [applySettings]);

    const loadZones = async () => {
        setZonesLoading(true);
        setDiscovered(await listDNSZones());
        setZonesLoading(false);
    };

    const handleSave = async () => {
        setSaving(true);
        const res = await saveDNSSettings({
            enabled, provider, zones, regionNames, graceMinutes, token, clearToken,
        });
        showToast(res.success ? 'DNS settings saved.' : (res.message || 'Save failed.'), res.success);
        if (res.success && res.settings) applySettings(res.settings);
        setSaving(false);
    };

    const handleDiscard = () => {
        const snap = snapshotRef.current;
        if (!snap) return;
        setEnabled(snap.enabled);
        setProvider(snap.provider);
        setZones(snap.zones);
        setRegionNames(snap.regionNames);
        setGraceMinutes(snap.graceMinutes);
        setToken('');
        setClearToken(false);
        setZoneDraft('');
    };

    const addZone = (raw: string) => {
        setZones(prev => addZoneTo(prev, raw));
        setZoneDraft('');
    };

    // Removing a zone also drops every per-region name left without one. Keeping
    // them would produce a selection the server rejects on save, with the reason
    // two cards further up the page.
    const removeZone = (zone: string) => {
        const next = removeZoneFrom(zones, regionNames, zone);
        setZones(next.zones);
        setRegionNames(next.regionNames);
    };

    const dirty =
        snapshotRef.current !== null &&
        JSON.stringify({ enabled, provider, zones, regionNames, graceMinutes, token, clearToken }) !==
            JSON.stringify(snapshotRef.current);

    useUnsavedChanges({ dirty, save: handleSave, discard: handleDiscard, saving });

    if (loading) {
        return (
            <div className="space-y-6">
                <SkeletonHeader />
                <SkeletonCard height="h-64" />
                <SkeletonCard height="h-48" />
            </div>
        );
    }

    const envManaged = settings?.envManaged ?? false;
    const hint = discovered ? zoneHint(discovered) : null;
    const status = settings?.status;

    // Regions that actually have a live edge. Offering an override for a region
    // with no edge would invite a selection the reconciler skips anyway.
    const regionsInPlay = Array.from(
        new Set((settings?.managedNames ?? []).map(n => n.region).filter(Boolean)),
    ).sort();

    return (
        <div className="space-y-6">
            <div>
                <h2 className="text-base font-display font-bold text-(--base-09) mb-1">DNS</h2>
                <p className="text-sm text-(--base-07)">
                    Keeps each region&apos;s edge wildcard A record pointing at the live edges in that
                    region. Credentials live only in Core - never on an edge or a relay - and only the
                    elected Core writes records. Leave this off to manage DNS by hand.
                </p>
            </div>

            {/* ─── Configuration ─── */}
            <div className="card p-5 space-y-4">
                <div className="flex items-center justify-between">
                    <div className="pr-4">
                        <label className="input-label">Automatic DNS updates</label>
                        <p className="text-xs text-(--base-06) mt-0.5">
                            When on, Core creates and removes the A records for regional edge
                            wildcards. Saving with this enabled first verifies the credential against
                            the zone.
                        </p>
                    </div>
                    <button
                        onClick={() => setEnabled(v => !v)}
                        className={`focus-ring toggle-track ${enabled ? 'toggle-track-on' : 'toggle-track-off'}`}
                        role="switch"
                        aria-checked={enabled}
                        aria-label="Automatic DNS updates"
                    >
                        <span className={`toggle-knob ${enabled ? 'toggle-knob-on' : 'toggle-knob-off'}`} />
                    </button>
                </div>

                <div className="flex flex-col gap-[5px]">
                    <label className="input-label">Provider</label>
                    <Select
                        value={provider}
                        onChange={setProvider}
                        options={(settings?.providers ?? ['cloudflare']).map(p => ({
                            value: p,
                            label: p.charAt(0).toUpperCase() + p.slice(1),
                        }))}
                        ariaLabel="DNS provider"
                    />
                </div>

                {/* API token. Read-only whenever the environment supplies it: a value
                    typed here would be stored and never read, which looks applied. */}
                <div className="flex flex-col gap-[5px]">
                    <label className="input-label">API Token</label>
                    {envManaged ? (
                        <div className="flex items-start gap-2 rounded-md border border-(--base-03) bg-(--base-02) px-3 py-2.5">
                            <Lock size={13} className="mt-0.5 shrink-0 text-(--base-06)" />
                            <p className="text-xs text-(--base-07)">
                                Supplied by the environment (
                                <span className="font-mono">DNS_API_TOKEN</span>). Change it where the
                                stack is deployed - a value entered here would be ignored.
                            </p>
                        </div>
                    ) : (
                        <>
                            <input
                                type="password"
                                value={token}
                                onChange={e => {
                                    setToken(e.target.value);
                                    if (e.target.value) setClearToken(false);
                                }}
                                placeholder={settings?.tokenSet ? 'Stored - leave empty to keep' : 'Paste the API token'}
                                autoComplete="off"
                                className="input-field input-mono"
                                disabled={clearToken}
                            />
                            <div className="flex items-center justify-between gap-3 mt-0.5">
                                <p className="text-xs text-(--base-06)">
                                    {settings?.tokenSet
                                        ? 'A token is stored. Leave this empty to keep it.'
                                        : 'Needs permission to read and edit DNS records in the zone.'}
                                </p>
                                {settings?.tokenSet && (
                                    <button
                                        type="button"
                                        onClick={() => {
                                            setClearToken(v => !v);
                                            setToken('');
                                        }}
                                        className="focus-ring text-xs text-(--base-06) hover:text-(--error-light) transition-colors shrink-0"
                                    >
                                        {clearToken ? 'Keep token' : 'Remove token'}
                                    </button>
                                )}
                            </div>
                            {clearToken && (
                                <p className="flex items-start gap-1.5 text-xs text-(--warning-light) mt-1">
                                    <AlertTriangle size={12} className="mt-0.5 shrink-0" />
                                    <span>The stored token will be removed when you save.</span>
                                </p>
                            )}
                        </>
                    )}
                </div>

                {/* Zones. The picker is an aid, not a gate - every discovery
                    failure still leaves the field typeable, because three of the
                    four outcomes are resolved by entering the domain by hand. */}
                <div className="flex flex-col gap-[5px]">
                    <div className="flex items-center justify-between">
                        <label className="input-label">Managed Zones</label>
                        <button
                            type="button"
                            onClick={loadZones}
                            disabled={zonesLoading || !(settings?.tokenSet || token)}
                            className="focus-ring inline-flex items-center gap-1.5 text-xs text-(--base-06) hover:text-(--accent-light) transition-colors disabled:opacity-40 disabled:cursor-not-allowed"
                        >
                            {zonesLoading
                                ? <Loader2 size={12} className="animate-spin" />
                                : <RefreshCw size={12} />}
                            List zones
                        </button>
                    </div>
                    <p className="text-xs text-(--base-06) mb-1">
                        One credential can manage several zones, so you can offer more than one
                        domain. DYLARIS only ever touches the names it created inside these zones -
                        never anything else the zone carries.
                    </p>
                    {/* Removing a zone is destructive and does not look it: the names in it
                        stop being advertised, and the sweep then deletes the records DYLARIS
                        created there. Said here because the paragraph above ("only ever touches
                        the names it created") otherwise reads as a promise that removal is
                        merely disengagement. */}
                    <p className="flex items-start gap-1.5 text-xs text-(--base-06) mb-1">
                        <AlertTriangle size={12} className="mt-0.5 shrink-0 text-(--warning-light)" />
                        <span>
                            Removing a zone here does not just stop managing it: the records
                            DYLARIS created inside it are deleted once the grace period below
                            elapses.
                        </span>
                    </p>

                    {zones.length > 0 && (
                        <ul className="flex flex-col gap-1.5 mb-1">
                            {zones.map(z => (
                                <li
                                    key={z}
                                    className="flex items-center justify-between gap-3 rounded-md border border-(--base-03) bg-(--base-02) px-3 py-2"
                                >
                                    <span className="font-mono text-xs text-(--base-08) truncate">{z}</span>
                                    <button
                                        type="button"
                                        onClick={() => removeZone(z)}
                                        aria-label={`Remove zone ${z}`}
                                        className="focus-ring shrink-0 text-(--base-06) hover:text-(--error-light) transition-colors"
                                    >
                                        <X size={13} />
                                    </button>
                                </li>
                            ))}
                        </ul>
                    )}

                    {/* Discovered zones the admin has not added yet. */}
                    {discovered?.state === 'ok' && discovered.zones.some(z => !zones.includes(z)) && (
                        <div className="flex flex-wrap gap-1.5 mb-1">
                            {discovered.zones
                                .filter(z => !zones.includes(z))
                                .map(z => (
                                    <button
                                        key={z}
                                        type="button"
                                        onClick={() => addZone(z)}
                                        className="focus-ring inline-flex items-center gap-1 rounded-md border border-(--base-03) bg-(--base-02) px-2 py-1 font-mono text-[11px] text-(--base-07) hover:border-(--accent) hover:text-(--accent-light) transition-colors"
                                    >
                                        <Plus size={11} /> {z}
                                    </button>
                                ))}
                        </div>
                    )}

                    <div className="flex items-center gap-2">
                        <input
                            type="text"
                            value={zoneDraft}
                            onChange={e => setZoneDraft(e.target.value)}
                            onKeyDown={e => {
                                if (e.key === 'Enter') {
                                    e.preventDefault();
                                    addZone(zoneDraft);
                                }
                            }}
                            placeholder="example.com"
                            className="input-field input-mono flex-1"
                            aria-label="Add a zone"
                        />
                        <button
                            type="button"
                            onClick={() => addZone(zoneDraft)}
                            disabled={!zoneDraft.trim()}
                            className="btn btn-secondary text-xs py-2 px-4 disabled:opacity-40 disabled:cursor-not-allowed"
                        >
                            Add
                        </button>
                    </div>

                    {hint && (
                        <p
                            className={`flex items-start gap-1.5 text-xs mt-1 ${
                                hint.tone === 'error'
                                    ? 'text-(--error-light)'
                                    : hint.tone === 'warn'
                                      ? 'text-(--warning-light)'
                                      : 'text-(--base-06)'
                            }`}
                        >
                            {hint.tone === 'info'
                                ? <Info size={12} className="mt-0.5 shrink-0" />
                                : <AlertTriangle size={12} className="mt-0.5 shrink-0" />}
                            <span>{hint.message}</span>
                        </p>
                    )}
                </div>

                {/* Grace period before a name that stopped being advertised is
                    removed. This is what keeps a rolling edge restart from
                    taking a live region out of DNS. */}
                <div className="flex flex-col gap-[5px]">
                    <label className="input-label">Removal Grace Period</label>
                    <div className="flex items-center gap-2">
                        <input
                            type="number"
                            min={1}
                            step={1}
                            value={graceMinutes || ''}
                            onChange={e => setGraceMinutes(Math.max(1, parseInt(e.target.value) || 1))}
                            className="input-field input-mono w-28 text-right"
                        />
                        <span className="text-[11px] font-mono text-(--base-06)">minutes</span>
                    </div>
                    <p className="text-xs text-(--base-06) mt-0.5">
                        How long a name must go unadvertised before its records are removed. Short
                        enough and a rolling edge restart would delete a live region&apos;s address.
                    </p>
                </div>
            </div>

            {/* ─── Names in effect ─── */}
            <div className="card p-5 space-y-4">
                <div>
                    <h3 className="text-sm font-display font-semibold text-(--base-08) mb-1">
                        Names In Effect
                    </h3>
                    <p className="text-xs text-(--base-06)">
                        What the reconciler would act on right now, per region. A region with no
                        entry here falls back to its edges&apos; own{' '}
                        <span className="font-mono">EDGE_WILDCARD</span>.
                    </p>
                </div>

                {(settings?.managedNames ?? []).length === 0 ? (
                    <p className="flex items-start gap-1.5 text-xs text-(--base-06)">
                        <Info size={12} className="mt-0.5 shrink-0" />
                        <span>
                            No online edge is advertising a name. Records are only ever written for
                            regions that have a live edge.
                        </span>
                    </p>
                ) : (
                    <ul className="flex flex-col gap-1.5">
                        {(settings?.managedNames ?? []).map(n => (
                            <li
                                key={`${n.region}-${n.name}`}
                                className="flex items-center justify-between gap-3 rounded-md border border-(--base-03) bg-(--base-02) px-3 py-2"
                            >
                                <div className="min-w-0">
                                    <span className="font-mono text-xs text-(--base-08) block truncate">
                                        {n.name}
                                    </span>
                                    <span className="text-[10px] text-(--base-06)">
                                        {n.origin === 'relay'
                                            ? 'beam relay'
                                            : n.region || 'no region'}
                                        {n.routable ? ` · zone ${n.zone}` : ' · outside every managed zone'}
                                    </span>
                                </div>
                                <div className="flex items-center gap-2 shrink-0">
                                    {!n.routable && (
                                        <span className="inline-flex items-center gap-1 text-[10px] text-(--error-light)">
                                            <AlertTriangle size={11} /> not written
                                        </span>
                                    )}
                                    {/* Origin is the whole point of this list: a leftover
                                        EDGE_WILDCARD winning over a panel selection is
                                        otherwise invisible. */}
                                    <span
                                        className={`font-mono text-[10px] uppercase tracking-[0.08em] px-1.5 py-0.5 rounded ${
                                            n.origin === 'panel'
                                                ? 'text-(--accent-light) bg-(--accent)/10'
                                                : 'text-(--base-06) bg-(--base-03)'
                                        }`}
                                    >
                                        {originLabel(n.origin)}
                                    </span>
                                </div>
                            </li>
                        ))}
                    </ul>
                )}

                {/* Per-region overrides. Only offered for regions that actually
                    have a live edge, so the screen cannot invite a selection for
                    a region the reconciler would skip anyway. */}
                {regionsInPlay.length > 0 && (
                    <div className="flex flex-col gap-3 pt-1">
                        <span className="mono-label">Per-region names</span>
                        {regionsInPlay.map(region => (
                            <RegionNameEditor
                                key={region}
                                region={region}
                                names={regionNames[region] ?? []}
                                zones={zones}
                                onChange={names =>
                                    setRegionNames(prev => {
                                        const next = { ...prev };
                                        if (names.length === 0) delete next[region];
                                        else next[region] = names;
                                        return next;
                                    })
                                }
                            />
                        ))}
                    </div>
                )}
            </div>

            {/* ─── Status ─── */}
            <div className="card p-5 space-y-3">
                <h3 className="text-sm font-display font-semibold text-(--base-08)">Reconciler Status</h3>
                {!settings?.enabled ? (
                    <p className="text-xs text-(--base-06)">
                        Automatic updates are off. Records are whatever you set by hand.
                    </p>
                ) : !status ? (
                    <p className="flex items-start gap-1.5 text-xs text-(--base-06)">
                        <Info size={12} className="mt-0.5 shrink-0" />
                        <span>
                            No run recorded yet. The reconciler runs every 30 seconds on the elected
                            Core, and reports nothing until at least one edge advertises a wildcard.
                        </span>
                    </p>
                ) : (
                    <>
                        <div className="flex items-center gap-2">
                            {status.ok
                                ? <CircleCheck size={14} className="text-(--success-light) shrink-0" />
                                : <CircleAlert size={14} className="text-(--error-light) shrink-0" />}
                            <span className="text-sm text-(--base-09)">
                                {status.ok ? 'Last run succeeded' : 'Last run reported errors'}
                            </span>
                            <span className="text-xs font-mono text-(--base-06)">
                                {formatWhen(status.lastRunAt)}
                            </span>
                        </div>
                        {status.error && (
                            <p className="text-xs text-(--error-light) font-mono break-all">
                                {status.error}
                            </p>
                        )}
                        {status.managedNames.length > 0 && (
                            <div className="flex flex-col gap-1">
                                <span className="mono-label">Managed names</span>
                                <ul className="flex flex-col gap-0.5">
                                    {status.managedNames.map(n => (
                                        <li key={n} className="text-xs font-mono text-(--base-07)">{n}</li>
                                    ))}
                                </ul>
                                <p className="text-xs text-(--base-06) mt-0.5">
                                    {status.recordCount} record{status.recordCount === 1 ? '' : 's'} across{' '}
                                    {status.managedNames.length} name
                                    {status.managedNames.length === 1 ? '' : 's'}.
                                </p>
                            </div>
                        )}
                    </>
                )}
            </div>
        </div>
    );
}

export default function DNSTab() {
    const [toast, setToast] = useState<{ msg: string; ok: boolean } | null>(null);

    const showToast = (msg: string, ok = true) => {
        setToast({ msg, ok });
        setTimeout(() => setToast(null), 5000);
    };

    return (
        <>
            <DNSPanel showToast={showToast} />

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
