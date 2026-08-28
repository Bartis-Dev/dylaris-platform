"use client";

import React, { useState, useEffect, useRef } from 'react';
import {
    getGatewaySettings, saveGatewaySettings, GatewaySettings, HosterDomain, HosterValidation,
    getRoutingMode, saveRoutingMode, getRoutingMigrationStatus,
    bulkDeleteRoutesBySuffix,
    RoutingMode, FileAccessMode,
    API_URL,
} from '@/lib/api';
import { RefreshCw, Save, CircleCheck, CircleAlert, Router, AlertTriangle, EyeOff, Globe, Plus, Trash2, X, Shield, Copy, Check, Search, Network } from 'lucide-react';
import { SkeletonHeader, SkeletonCard, SkeletonTable } from '@/components/Skeleton';
import Spinner from '@/components/Spinner';
import { LimitField } from '@/components/settings/LimitField';
import { useUnsavedChanges } from '@/components/settings/UnsavedChanges';
import GuardedTabs from '@/components/settings/GuardedTabs';
import { useTabParam } from '@/lib/useTabParam';
import { toast } from '@/components/ui/Toast';
import { checkDns, DnsCheckResult, DnsRecord, DnsRecordCategory, DnsRecordStatus } from '@/lib/api/dns';
import GatewayDnsCard from '@/components/settings/GatewayDnsCard';
import SettingsCard, { SettingsGroup } from '@/components/settings/SettingsCard';
import { useAppData } from '@/lib/AppDataContext';
import { useBusy } from '@/lib/useBusy';
import { cnameTargetsFor } from '@/lib/cnameTargets';
import HelpTip from '@/components/ui/HelpTip';
import SettingsPage from '@/components/settings/SettingsPage';

// ─────────────────────────────────────────────
// Gateway settings
// ─────────────────────────────────────────────

type LimitKey = 'global' | 'userDefault' | 'perServer' | 'portMc';
type ModeOption<T extends string> = { value: T; label: string; desc: string };
type SubTab = 'gateway' | 'xdp';

// Exported so the settings-index test can prove every tab the search points at
// actually exists here.
export const GATEWAY_TABS: readonly SubTab[] = ['gateway', 'xdp'];

const ROUTING_OPTIONS: ModeOption<RoutingMode>[] = [
    { value: 'ip_port', label: 'IP : Port', desc: 'Direct host port binding — players connect via Node IP + port' },
    { value: 'both', label: 'Both', desc: 'Allow both IP:Port and Gateway routes simultaneously' },
    { value: 'gateway', label: 'Gateway', desc: 'Route-only mode — no host ports exposed, traffic via Gate → Link' },
];

const FILE_OPTIONS: ModeOption<FileAccessMode>[] = [
    { value: 'sftp', label: 'SFTP', desc: 'Users access server files via SFTP on the Node IP' },
    { value: 'both', label: 'Both', desc: 'Allow both SFTP and Beam file access' },
    { value: 'beam', label: 'Beam', desc: 'File access only via Beam relay — no direct Node IP needed' },
];

const NAV_ITEMS: { id: SubTab; label: string; icon: React.ElementType }[] = [
    { id: 'gateway', label: 'Gateway', icon: Router },
    { id: 'xdp', label: 'DDoS Protection', icon: Shield },
    // Not the Hub's own web interface, which is off by default and stays off.
    // This mints the Redis ACL user the Hub needs to reach Core's Redis at all,
    // which is how a Hub deployed alongside the platform is bootstrapped.
];

// ─────────────────────────────────────────────
// DNS & Domains check card
// ─────────────────────────────────────────────

// Plain-language, one-line explanation per record category. Shown under the
// record name so a non-expert operator knows what each row is for.
const DNS_CATEGORY_BLURB: Record<DnsRecordCategory, string> = {
    player: 'Player base domain — the address customers connect to.',
    wildcard: 'Wildcard so every server subdomain resolves automatically — you never touch DNS per customer.',
    cname: 'Custom-domain target — where customers point a CNAME for their own domain.',
    panel: 'Panel domain — where this admin/web interface is served.',
    api: 'API domain — what the panel in the browser calls for every request. The panel loads without it and then does nothing.',
    beam: 'Beam relay — the hostname the desktop client dials for REMOTE access. LAN and direct connections do not use it.',
};

const DNS_CATEGORY_LABEL: Record<DnsRecordCategory, string> = {
    player: 'Player base',
    wildcard: 'Wildcard',
    cname: 'Custom CNAME',
    panel: 'Panel',
    api: 'API',
    beam: 'Beam relay',
};

// Map the check verdict onto the shared .badge-* utilities + a label.
const DNS_STATUS_BADGE: Record<DnsRecordStatus, { cls: string; label: string }> = {
    ok: { cls: 'badge-success', label: 'OK' },
    mismatch: { cls: 'badge-warning', label: 'Mismatch' },
    missing: { cls: 'badge-error', label: 'Missing' },
    unresolved: { cls: 'badge-error', label: 'Unresolved' },
    info: { cls: 'badge-neutral', label: 'Optional' },
};

// One copyable record value (the expected DNS target). Shows a transient
// check mark on copy, matching the api-keys / Warp copy pattern.
function CopyValue({ value }: { value: string }) {
    const [copied, setCopied] = useState(false);
    const copy = () => {
        navigator.clipboard.writeText(value).then(() => {
            setCopied(true);
            setTimeout(() => setCopied(false), 1500);
        }).catch(() => { /* clipboard blocked — silent, value is still visible */ });
    };
    return (
        <div className="flex items-center gap-1.5">
            <code className="font-mono text-xs text-(--base-09) bg-(--base-03) px-1.5 py-0.5 rounded break-all">{value}</code>
            <button
                type="button"
                onClick={copy}
                className="text-(--base-06) hover:text-(--accent-light) transition-colors shrink-0"
                title="Copy value"
                aria-label={`Copy ${value}`}
            >
                {copied ? <Check size={12} className="text-(--success-light)" /> : <Copy size={12} />}
            </button>
        </div>
    );
}

function DnsCheckCard() {
    // The records table is populated by the check itself (the backend computes
    // the required records from the operator's config). null = before first
    // check, [] only happens once a check returns with zero records.
    const [result, setResult] = useState<DnsCheckResult | null>(null);
    const [checking, setChecking] = useState(false);
    const [error, setError] = useState<string | null>(null);

    const runCheck = async () => {
        setChecking(true);
        setError(null);
        const res = await checkDns();
        if (res.success) {
            setResult(res);
        } else {
            setError(res.message || 'DNS check failed. Try again.');
        }
        setChecking(false);
    };

    const checked = result && !checking;

    return (
        <div className="card p-5 space-y-5">
            <div className="flex items-start justify-between gap-4">
                <div className="flex items-center gap-3">
                    <div className="w-9 h-9 rounded-md bg-(--base-03) flex items-center justify-center">
                        <Network size={18} className="text-(--accent-light)" />
                    </div>
                    <div>
                        <div className="font-medium text-sm text-(--base-09)">DNS &amp; Domains</div>
                        <div className="text-xs text-(--base-06)">The records to create at your DNS provider — then verify they resolve and the ingress is reachable</div>
                    </div>
                </div>
                <button
                    type="button"
                    onClick={runCheck}
                    disabled={checking}
                    className="btn btn-primary btn-sm shrink-0 disabled:opacity-40"
                >
                    {checking ? <><Spinner size="xs" /> Checking…</> : <><Search size={13} /> Check DNS</>}
                </button>
            </div>

            {/* Error state */}
            {error && (
                <div className="alert alert-error text-xs">
                    <AlertTriangle size={14} className="shrink-0 mt-0.5" />
                    <span>{error}</span>
                </div>
            )}

            {/* Checking state — skeleton (lookups can take a few seconds) */}
            {checking && !result && (
                <div className="space-y-3">
                    <SkeletonTable rows={4} cols={4} />
                </div>
            )}

            {/* Empty state — before the first check */}
            {!checking && !result && !error && (
                <div className="flex flex-col items-center gap-2 px-4 py-8 rounded-md bg-(--base-02) border border-(--base-03) text-center">
                    <Network size={22} className="text-(--base-05)" />
                    <p className="text-sm text-(--base-08)">Run a check to see your required DNS records</p>
                    <p className="text-xs text-(--base-06) max-w-md">
                        The records are computed from your configured hoster domains, custom-domain CNAME target and panel URL. Each is resolved against a public resolver and the ingress is dialled for reachability.
                    </p>
                </div>
            )}

            {/* Results — records table + reachability */}
            {result && (
                <div className={`space-y-5 transition-opacity ${checking ? 'opacity-50' : 'opacity-100'}`}>
                    {result.records.length === 0 ? (
                        <div className="flex items-start gap-2 p-3 rounded-md bg-(--base-02) border border-(--base-03) text-xs text-(--base-07)">
                            <AlertTriangle size={14} className="text-(--warning-light) mt-0.5 shrink-0" />
                            <span>No DNS records to verify. Configure at least one hoster domain above (and the panel URL via <span className="font-mono">FRONTEND_URL</span>) to populate this table.</span>
                        </div>
                    ) : (
                        <div>
                            <h3 className="mono-label mb-3">Required Records</h3>
                            <div className="border border-(--base-03) rounded-md overflow-hidden">
                                {/* Header */}
                                <div className="hidden md:grid grid-cols-[auto_1fr_auto] gap-4 px-4 py-2.5 bg-(--base-02) border-b border-(--base-03)">
                                    <span className="mono-label">Type</span>
                                    <span className="mono-label">Name &amp; expected target</span>
                                    <span className="mono-label text-right">{checked ? 'Status' : ''}</span>
                                </div>
                                {result.records.map((rec, idx) => (
                                    <DnsRecordRow key={`${rec.name}-${rec.type}-${idx}`} rec={rec} checked={!!checked} />
                                ))}
                            </div>
                        </div>
                    )}

                    {/* Reachability */}
                    {result.reachability.length > 0 && (
                        <div>
                            <h3 className="mono-label mb-3">Reachability</h3>
                            <div className="space-y-2">
                                {result.reachability.map((r, idx) => (
                                    <div key={`${r.target}-${idx}`} className="flex items-start justify-between gap-4 p-3 rounded-md bg-(--base-02)">
                                        <div className="min-w-0">
                                            <code className="font-mono text-xs text-(--base-09) break-all">{r.target}</code>
                                            {r.hint && <p className="text-xs text-(--base-06) mt-0.5">{r.hint}</p>}
                                        </div>
                                        <span className={`badge ${r.ok ? 'badge-success' : 'badge-error'} shrink-0`}>
                                            {r.ok ? <><CircleCheck size={11} /> Reachable</> : <><CircleAlert size={11} /> Unreachable</>}
                                        </span>
                                    </div>
                                ))}
                            </div>
                        </div>
                    )}

                    {result.checkedAt && (
                        <p className="text-[11px] font-mono text-(--base-05)">
                            Last checked {new Date(result.checkedAt).toLocaleString()}
                        </p>
                    )}
                </div>
            )}
        </div>
    );
}

// One record row — name + expected target(s) with copy buttons + a category
// blurb. After a check, the actual resolved value(s), a status badge and the
// hint are shown. Before the first check the row only carries the expected
// config (status badge withheld).
function DnsRecordRow({ rec, checked }: { rec: DnsRecord; checked: boolean }) {
    const badge = DNS_STATUS_BADGE[rec.status];
    const showStatus = checked && badge;
    return (
        <div className="grid grid-cols-1 md:grid-cols-[auto_1fr_auto] gap-2 md:gap-4 px-4 py-3 border-b border-(--base-03) last:border-b-0 items-start">
            <span className="font-mono text-[11px] px-1.5 py-0.5 rounded bg-(--base-03) text-(--base-07) w-fit h-fit mt-0.5">{rec.type}</span>

            <div className="min-w-0 space-y-1.5">
                <div className="flex items-center gap-2 flex-wrap">
                    <code className="font-mono text-xs text-(--base-09) break-all">{rec.name}</code>
                    <span className="badge badge-neutral">{DNS_CATEGORY_LABEL[rec.category]}</span>
                </div>
                <p className="text-xs text-(--base-06)">{DNS_CATEGORY_BLURB[rec.category]}</p>

                {/* Expected targets — copyable */}
                {rec.expected.length > 0 && (
                    <div className="flex flex-col gap-1 pt-0.5">
                        <span className="text-[10px] font-mono uppercase tracking-[0.08em] text-(--base-05)">Expected</span>
                        {rec.expected.map((v, i) => <CopyValue key={i} value={v} />)}
                    </div>
                )}

                {/* Actual resolved values + hint (post-check) */}
                {checked && (
                    <div className="flex flex-col gap-1 pt-0.5">
                        <span className="text-[10px] font-mono uppercase tracking-[0.08em] text-(--base-05)">Resolved</span>
                        {rec.actual.length > 0 ? (
                            rec.actual.map((v, i) => (
                                <code key={i} className="font-mono text-xs text-(--base-08) break-all">{v}</code>
                            ))
                        ) : (
                            <code className="font-mono text-xs text-(--base-06) italic">no answer</code>
                        )}
                        {rec.hint && <p className="text-xs text-(--base-06) mt-0.5">{rec.hint}</p>}
                    </div>
                )}
            </div>

            <div className="md:text-right">
                {showStatus && <span className={`badge ${badge.cls}`}>{badge.label}</span>}
            </div>
        </div>
    );
}

// ─────────────────────────────────────────────
// Gateway panel
// ─────────────────────────────────────────────

// Inline notice shown above the operational gateway controls (routes, XDP)
// whenever Game Traffic is still on IP:Port. The routing-mode selector
// itself is never gated — it's the only way to turn the gateway on — so the
// copy points the operator back at it. `here` = shown on the Gateway sub-tab
// (selector is right above); otherwise it points back to the Gateway sub-tab.
function GatewayDisabledNotice({ here = false }: { here?: boolean }) {
    return (
        <div className="alert alert-warning text-xs">
            <AlertTriangle size={14} className="shrink-0 mt-0.5" />
            <span>
                Gateway routing is disabled. Switch <span className="font-medium">Game Traffic</span> to{' '}
                <span className="font-medium">Gateway</span> or <span className="font-medium">Both</span>{' '}
                {here ? 'above' : 'in the Gateway sub-tab'} to manage routes and XDP.
            </span>
        </div>
    );
}

function GatewayPanel({ showToast }: { showToast: (msg: string, ok?: boolean) => void }) {
    // Auto-move is gateway-only; switching back to IP:Port force-disables it
    // server-side. Surface that in the confirm step so the admin isn't
    // surprised that every server's auto-move opt-in gets cleared.
    const { featureFlags } = useAppData();

    const [settings, setSettings] = useState<GatewaySettings>({
        limits: {
            global: -1, userDefault: -1, perServer: -1,
            portMc: -1, portMcEnabled: true,
        },
        hosterDomains: [],
        customDomainsEnabled: false,
        cnameTarget: '',
        blockedRoutePrefixes: [],
    });
    // Preview of what users will actually be told to point their domain at:
    // the label combined with every hoster base, one target per region.
    const cnameTargets = cnameTargetsFor(settings.cnameTarget, settings.hosterDomains);
    const [loading, setLoading] = useState(true);
    const [saving, setSaving] = useState(false);

    const [routingMode, setRoutingMode] = useState<RoutingMode>('ip_port');
    const [fileMode, setFileMode] = useState<FileAccessMode>('sftp');
    const [origRoutingMode, setOrigRoutingMode] = useState<RoutingMode>('ip_port');
    const [origFileMode, setOrigFileMode] = useState<FileAccessMode>('sftp');
    const [confirmModal, setConfirmModal] = useState(false);
    const [applyingRouting, runSaveRouting] = useBusy();
    const [savingRouting, setSavingRouting] = useState(false);
    const [migration, setMigration] = useState<{ running: boolean; total: number; done: number; failed: number } | null>(null);
    const pollRef = useRef<ReturnType<typeof setInterval> | null>(null);

    // Snapshot of last-saved gateway settings for dirty detection. The routing
    // mode has its own explicit "Apply Routing" flow and is tracked separately.
    const snapshotRef = useRef<GatewaySettings | null>(null);

    useEffect(() => {
        Promise.all([getGatewaySettings(), getRoutingMode()]).then(([gwRes, rmRes]) => {
            if (gwRes.success && gwRes.settings) {
                const loaded: GatewaySettings = {
                    ...gwRes.settings,
                    hosterDomains: gwRes.settings.hosterDomains || [],
                    customDomainsEnabled: !!gwRes.settings.customDomainsEnabled,
                    cnameTarget: gwRes.settings.cnameTarget || '',
                    blockedRoutePrefixes: gwRes.settings.blockedRoutePrefixes || [],
                };
                setSettings(loaded);
                snapshotRef.current = loaded;
            }
            if (rmRes.success) {
                const m: RoutingMode = rmRes.mode || 'ip_port';
                const f: FileAccessMode = rmRes.fileMode || 'sftp';
                setRoutingMode(m); setOrigRoutingMode(m);
                setFileMode(f); setOrigFileMode(f);
            }
        }).finally(() => setLoading(false));
    }, []);

    // The migration poll only stops itself once the run reports finished, so
    // leaving this tab mid-migration otherwise left it running: a request every
    // 3s and a setState into an unmounted component, for as long as the
    // migration lasts. The interval eight lines below already does this.
    useEffect(() => () => {
        if (pollRef.current) { clearInterval(pollRef.current); pollRef.current = null; }
    }, []);

    const startPolling = () => {
        if (pollRef.current) return;
        pollRef.current = setInterval(async () => {
            const res = await getRoutingMigrationStatus();
            if (res.success) {
                setMigration({ running: res.running, total: res.total, done: res.done, failed: res.failed });
                if (!res.running) { clearInterval(pollRef.current!); pollRef.current = null; }
            }
        }, 3000);
    };

    const handleSaveRouting = async () => {
        setSavingRouting(true);
        setConfirmModal(false);
        const res = await saveRoutingMode({ mode: routingMode, fileMode });
        if (res.success) {
            setOrigRoutingMode(routingMode);
            setOrigFileMode(fileMode);
            // The mode saves even when the fleet migration cannot start, and
            // "Routing mode saved." alone is indistinguishable from a platform
            // that had no servers to migrate - while every server is in fact
            // still on the old routing.
            if (res.migrationError) showToast(res.migrationError, false);
            else showToast(`Routing mode saved.${res.serversQueued > 0 ? ` Redeploying ${res.serversQueued} servers...` : ''}`);
            if (res.serversQueued > 0) {
                setMigration({ running: true, total: res.serversQueued, done: 0, failed: 0 });
                startPolling();
            }
        } else {
            showToast(res.message || 'Failed to save routing mode.', false);
        }
        setSavingRouting(false);
    };

    const handleSave = async (): Promise<boolean> => {
        setSaving(true);
        try {
            const res = await saveGatewaySettings(settings);
            showToast(res.success ? 'Gateway settings saved.' : (res.message || 'Save failed.'), res.success);
            if (res.success) snapshotRef.current = settings;
            return !!res.success;
        } finally {
            setSaving(false);
        }
    };

    const handleDiscard = () => {
        if (snapshotRef.current) setSettings(snapshotRef.current);
    };

    const setLimit = (key: LimitKey, value: number | null) =>
        setSettings(prev => ({ ...prev, limits: { ...prev.limits, [key]: value } }));

    const routingChanged = routingMode !== origRoutingMode || fileMode !== origFileMode;

    // Gate the operational route-management UI on the *applied* routing mode
    // (origRoutingMode), not the in-progress selection — the controls below
    // only do anything once Gateway/Both is actually live. The mode selector
    // above always stays usable.
    const gatewayOff = origRoutingMode === 'ip_port';

    const addHoster = () => {
        setSettings(prev => ({
            ...prev,
            hosterDomains: [...prev.hosterDomains, { domain: '', validation: 'alphanumeric' }],
        }));
    };

    // Removing a hoster domain — when the entry has a real domain value we
    // route through a confirm modal with an optional cascade-delete-routes
    // checkbox. Blank/unsaved entries (just-added rows) skip the prompt.
    const [removeTarget, setRemoveTarget] = useState<{ idx: number; domain: string } | null>(null);
    const [removeCascade, setRemoveCascade] = useState(false);
    const [removeCountdown, setRemoveCountdown] = useState(0);
    const [removeBusy, setRemoveBusy] = useState(false);

    useEffect(() => {
        if (!removeTarget || !removeCascade) {
            setRemoveCountdown(0);
            return;
        }
        setRemoveCountdown(5);
        const id = setInterval(() => {
            setRemoveCountdown(c => (c <= 1 ? 0 : c - 1));
        }, 1000);
        return () => clearInterval(id);
    }, [removeTarget, removeCascade]);

    const removeHoster = (idx: number) => {
        const target = settings.hosterDomains[idx];
        if (!target || !target.domain.trim()) {
            // Brand-new empty row — drop immediately, no confirm.
            setSettings(prev => ({
                ...prev,
                hosterDomains: prev.hosterDomains.filter((_, i) => i !== idx),
            }));
            return;
        }
        setRemoveCascade(false);
        setRemoveCountdown(0);
        setRemoveTarget({ idx, domain: target.domain });
    };

    const confirmRemoveHoster = async () => {
        if (!removeTarget) return;
        if (removeCascade && removeCountdown > 0) return; // timer still running
        setRemoveBusy(true);
        let cascadeMessage = '';
        if (removeCascade) {
            const res = await bulkDeleteRoutesBySuffix(removeTarget.domain);
            if (res.success) {
                cascadeMessage = ` (${res.deleted} route${res.deleted !== 1 ? 's' : ''} deleted)`;
            } else {
                cascadeMessage = ' (route cascade failed)';
            }
        }
        setSettings(prev => ({
            ...prev,
            hosterDomains: prev.hosterDomains.filter((_, i) => i !== removeTarget.idx),
        }));
        setRemoveBusy(false);
        setRemoveTarget(null);
        // Nothing has been written yet - the removal is a local edit, and the
        // card's own Save commits it along with everything else in it.
        showToast(`Removed ${removeTarget.domain}${cascadeMessage}. Save this card to apply it.`);
    };
    const updateHoster = (idx: number, patch: Partial<HosterDomain>) => {
        setSettings(prev => ({
            ...prev,
            hosterDomains: prev.hosterDomains.map((h, i) => i === idx ? { ...h, ...patch } : h),
        }));
    };

    const VALIDATION_LABELS: Record<HosterValidation, string> = {
        letters: 'Letters only',
        alphanumeric: 'Letters + numbers',
        dns: 'Full DNS (a-z, 0-9, -)',
    };

    const allocationFields: { key: LimitKey; label: string; desc: string }[] = [
        { key: 'global', label: 'Global Max Routes', desc: 'Total routes across all users and servers' },
        { key: 'userDefault', label: 'Default Per-User Max', desc: 'Default limit for users without a custom override' },
        { key: 'perServer', label: 'Per-Server Max', desc: 'Max routes per individual MC server' },
    ];

    const dirty =
        snapshotRef.current !== null &&
        JSON.stringify(settings) !== JSON.stringify(snapshotRef.current);

    useUnsavedChanges({ dirty, save: handleSave, discard: handleDiscard, saving });

    if (loading) return (
        <div className="space-y-6">
            <SkeletonHeader />
            <SkeletonCard height="h-72" />
            <SkeletonCard height="h-80" />
            <SkeletonCard height="h-96" />
        </div>
    );

    return (
        <SettingsPage
            title="Gateway configuration"
            icon={Router}
            width="full"
            description="Gateway routing, link defaults and route limits for gates and links."
        >
            {/* Routing Mode */}
            <div className="card p-5 space-y-5">
                <div className="flex items-center gap-3">
                    <div className="w-9 h-9 rounded-md bg-(--base-03) flex items-center justify-center">
                        <Router size={18} className="text-(--accent-light)" />
                    </div>
                    <div>
                        <div className="font-medium text-sm text-(--base-09)">Traffic Routing</div>
                        <div className="text-xs text-(--base-06)">How game traffic and file access are routed to servers</div>
                    </div>
                </div>

                <div>
                    <h3 className="mono-label mb-3 flex items-center gap-1.5">
                        Game Traffic
                        <HelpTip label="About the routing mode">
                            <p className="mb-2">
                                Where players connect. This is the highest-consequence setting on
                                this page: applying it <strong>redeploys every server</strong>, because
                                each container has to be recreated with different networking.
                            </p>
                            <p className="mb-2">
                                <strong>IP:Port</strong> - players connect straight to the node&apos;s
                                public address. Works with no gateway at all, and exposes the machine.
                                <br />
                                <strong>Gateway</strong> - players connect to a domain on 25565 and
                                the edge forwards it. The node&apos;s address is never given out.
                                <br />
                                <strong>Both</strong> - either route works. Useful while migrating;
                                the node address stays exposed while it is on.
                            </p>
                            <p>
                                The node IP is only fully hidden with Gateway <em>and</em> File
                                Access on Beam - SFTP would otherwise hand out the same address.
                            </p>
                        </HelpTip>
                    </h3>
                    <div className="grid grid-cols-3 gap-2">
                        {ROUTING_OPTIONS.map(opt => (
                            <button key={opt.value} type="button" onClick={() => setRoutingMode(opt.value)}
                                className={`p-3 rounded-md border text-left transition-colors ${routingMode === opt.value ? 'border-(--accent) bg-(--accent)/10' : 'border-(--base-03) bg-(--base-02) hover:border-(--base-05)'}`}>
                                <div className={`text-sm font-medium ${routingMode === opt.value ? 'text-(--accent-light)' : 'text-(--base-09)'}`}>{opt.label}</div>
                                <div className="text-xs text-(--base-06) mt-0.5">{opt.desc}</div>
                            </button>
                        ))}
                    </div>
                </div>

                <div>
                    <h3 className="mono-label mb-3 flex items-center gap-1.5">
                        File Access
                        <HelpTip label="About file access">
                            <p className="mb-2">
                                How people reach their files.
                            </p>
                            <p className="mb-2">
                                <strong>SFTP</strong> is the familiar one and works with any client -
                                but the client connects to the node directly, so it learns the
                                machine&apos;s address.
                                <br />
                                <strong>Beam</strong> is the desktop app. It works over the local
                                network without a gateway, and remotely through the relay, which is
                                what keeps the node address private.
                            </p>
                            <p>
                                Switching to Beam does not remove existing SFTP credentials; it stops
                                offering the SFTP route in the panel.
                            </p>
                        </HelpTip>
                    </h3>
                    <div className="grid grid-cols-3 gap-2">
                        {FILE_OPTIONS.map(opt => (
                            <button key={opt.value} type="button" onClick={() => setFileMode(opt.value)}
                                className={`p-3 rounded-md border text-left transition-colors ${fileMode === opt.value ? 'border-(--accent) bg-(--accent)/10' : 'border-(--base-03) bg-(--base-02) hover:border-(--base-05)'}`}>
                                <div className={`text-sm font-medium ${fileMode === opt.value ? 'text-(--accent-light)' : 'text-(--base-09)'}`}>{opt.label}</div>
                                <div className="text-xs text-(--base-06) mt-0.5">{opt.desc}</div>
                            </button>
                        ))}
                    </div>
                </div>

                <div className={`flex items-start gap-3 p-3 rounded-md border ${routingMode === 'gateway' && fileMode === 'beam' ? 'border-(--success)/30 bg-(--success)/5' : 'border-(--base-04) bg-(--base-02)'}`}>
                    <EyeOff size={15} className={`mt-0.5 shrink-0 ${routingMode === 'gateway' && fileMode === 'beam' ? 'text-(--success-light)' : 'text-(--base-06)'}`} />
                    <p className="text-xs text-(--base-07)">
                        The public Node IP is only fully hidden when both <span className="text-(--base-09) font-medium">Game Traffic</span> is set to <span className="text-(--base-09) font-medium">Gateway</span> and <span className="text-(--base-09) font-medium">File Access</span> is set to <span className="text-(--base-09) font-medium">Beam</span>.
                        {routingMode === 'gateway' && fileMode === 'beam' && <span className="text-(--success-light) font-medium ml-1">Node IPs are currently fully hidden.</span>}
                    </p>
                </div>

                {migration && (
                    <div className="p-3 rounded-md bg-(--base-02) border border-(--base-04) space-y-2">
                        <div className="flex items-center justify-between">
                            <div className="flex items-center gap-2">
                                {migration.running ? <Spinner size="xs" className="text-(--accent-light)" /> : <RefreshCw size={13} className="text-(--accent-light)" />}
                                <span className="text-xs text-(--base-09)">{migration.running ? 'Redeploying servers...' : 'Redeploy complete'}</span>
                            </div>
                            <span className="font-mono text-xs text-(--base-06)">{migration.done} / {migration.total} done{migration.failed > 0 ? ` · ${migration.failed} failed` : ''}</span>
                        </div>
                        <div className="h-1.5 rounded-full bg-(--base-03) overflow-hidden">
                            <div className={`h-full rounded-full transition-all duration-300 ${migration.failed > 0 ? 'bg-(--error-light)' : 'bg-(--accent)'}`}
                                style={{ width: migration.total > 0 ? `${Math.round((migration.done / migration.total) * 100)}%` : '0%' }} />
                        </div>
                    </div>
                )}

                <div className="flex items-center gap-3 pt-1 border-t border-(--base-03)">
                    <button onClick={() => setConfirmModal(true)} disabled={!routingChanged || savingRouting}
                        className="btn btn-primary disabled:opacity-40">
                        <Save size={14} />
                        {savingRouting ? 'Applying...' : 'Apply Routing'}
                    </button>
                    {routingChanged && (
                        <span className="text-xs text-(--base-06) flex items-center gap-1.5">
                            <AlertTriangle size={12} className="text-(--warning-light)" />
                            Changing routing mode will trigger a server redeploy
                        </span>
                    )}
                </div>
            </div>

            {/* Operational gateway content — only meaningful once Gateway/Both
                is the applied routing mode. Greyed + disabled otherwise; the
                mode selector above stays usable to turn it on. */}
            {gatewayOff && <GatewayDisabledNotice here />}
            <fieldset disabled={gatewayOff} className="space-y-6 disabled:opacity-50 border-0 p-0 m-0">

            {/* Domains and route limits are ONE payload behind one endpoint, so
                they are one card with one save. They used to be two cards with
                the DNS diagnostics wedged between them, which read as three
                separate things and left no way to tell what the single Save
                button was about to write. */}
            <SettingsCard
                title="Domains and route limits"
                icon={Globe}
                form={{ dirty, saving, save: handleSave, discard: handleDiscard }}
                description="The domains users pick from, and how many routes and ports they may take."
            >
                <SettingsGroup title="Hoster domains" first>
                    <div>
                    <p className="text-xs text-(--base-06) mb-3">
                        Users only enter a subdomain — these base domains appear as a dropdown next to the input. Pick which characters are allowed in the subdomain per domain.
                    </p>
                    <div className="space-y-2">
                        {settings.hosterDomains.length === 0 && (
                            <p className="text-xs text-(--base-05) italic px-3 py-3 rounded-md bg-(--base-02)">
                                No hoster domains configured. Add one to enable the subdomain picker.
                            </p>
                        )}
                        {settings.hosterDomains.map((hd, idx) => (
                            <div key={idx} className="flex items-center gap-2 p-2 rounded-md bg-(--base-02)">
                                <input
                                    type="text"
                                    value={hd.domain}
                                    onChange={e => updateHoster(idx, { domain: e.target.value.toLowerCase().trim() })}
                                    placeholder="dylaris.com"
                                    className="input-field input-mono flex-1 text-sm"
                                />
                                <select
                                    value={hd.validation}
                                    onChange={e => updateHoster(idx, { validation: e.target.value as HosterValidation })}
                                    className="input-field text-sm w-52"
                                >
                                    {(Object.keys(VALIDATION_LABELS) as HosterValidation[]).map(k => (
                                        <option key={k} value={k}>{VALIDATION_LABELS[k]}</option>
                                    ))}
                                </select>
                                <button
                                    type="button"
                                    onClick={() => removeHoster(idx)}
                                    className="text-(--base-06) hover:text-(--error-light) transition-colors p-2"
                                    title="Remove"
                                >
                                    <Trash2 size={14} />
                                </button>
                            </div>
                        ))}
                    </div>
                    <button
                        type="button"
                        onClick={addHoster}
                        className="btn btn-secondary btn-sm mt-3"
                    >
                        <Plus size={12} /> Add domain
                    </button>
                </div>

                <div className="border-t border-(--base-03) pt-5 space-y-4">
                    <div className="flex items-center justify-between gap-4">
                        <div>
                            <h3 className="mono-label">Custom Domains</h3>
                            <p className="text-xs text-(--base-06) mt-1">Allow users to bring their own domain via a CNAME record.</p>
                        </div>
                        <button
                            type="button"
                            role="switch"
                            aria-checked={settings.customDomainsEnabled}
                            onClick={() => setSettings(prev => ({ ...prev, customDomainsEnabled: !prev.customDomainsEnabled }))}
                            className={`toggle-track ${settings.customDomainsEnabled ? 'toggle-track-on' : 'toggle-track-off'}`}
                        >
                            <span className={`toggle-knob ${settings.customDomainsEnabled ? 'toggle-knob-on' : 'toggle-knob-off'}`} />
                        </button>
                    </div>
                    {settings.customDomainsEnabled && (
                        <div className="flex flex-col gap-[5px]">
                            <label className="input-label">CNAME Label</label>
                            <input
                                type="text"
                                value={settings.cnameTarget}
                                onChange={e => setSettings(prev => ({ ...prev, cnameTarget: e.target.value.toLowerCase() }))}
                                placeholder="route"
                                className="input-field input-mono text-sm"
                            />
                            <p className="text-xs text-(--base-06)">
                                A single label, not a full domain. It is combined with every hoster domain above, so one
                                entry covers all regions and each user picks the target for the region they want.
                            </p>
                            {cnameTargets.length > 0 && (
                                <div className="mt-1 flex flex-col gap-1">
                                    <span className="text-xs text-(--base-06)">Users will be told to point their domain at:</span>
                                    {cnameTargets.map(t => (
                                        <code key={t} className="font-mono text-xs text-(--accent-light) bg-(--base-02) px-1.5 py-0.5 rounded w-fit">{t}</code>
                                    ))}
                                    <span className="text-xs text-(--base-06)">
                                        Each of these needs its own A record pointing at that region&apos;s edge IPs. The
                                        wildcard does not cover them.
                                    </span>
                                </div>
                            )}
                            {settings.cnameTarget.trim() !== '' && settings.hosterDomains.length === 0 && (
                                <span className="text-xs text-(--warning-light)">
                                    Add a hoster domain above — without one there is nothing to combine this label with.
                                </span>
                            )}
                        </div>
                    )}
                </div>

                <div className="border-t border-(--base-03) pt-5 space-y-3">
                    <div>
                        <h3 className="mono-label">Reserved Route Prefixes</h3>
                        <p className="text-xs text-(--base-06) mt-1">
                            Leftmost labels users cannot register (subdomain picker and the first label of custom domains). One per line.
                        </p>
                    </div>
                    <textarea
                        value={settings.blockedRoutePrefixes.join('\n')}
                        onChange={e => setSettings(prev => ({
                            ...prev,
                            blockedRoutePrefixes: e.target.value
                                .split(/[\n,]/)
                                .map(s => s.trim().toLowerCase())
                                .filter(Boolean),
                        }))}
                        rows={4}
                        spellCheck={false}
                        placeholder={'admin\ndylaris\napp\napi'}
                        className="input-field input-mono text-sm w-full resize-y"
                    />
                    <p className="text-xs text-(--base-06)">
                        Leave empty to allow everything. Saving an empty list disables the built-in defaults.
                    </p>
                </div>
                </SettingsGroup>

                <SettingsGroup title="Route limits" description="Maximum route allocations and port access.">

                <div>
                    <h3 className="mono-label mb-3">Route Allocation</h3>
                    <div className="space-y-3">
                        {allocationFields.map(({ key, label, desc }) => (
                            <div key={key} className="flex items-center justify-between gap-4 p-3 rounded-md bg-(--base-02)">
                                <div className="flex-1 min-w-0">
                                    <p className="text-sm text-(--base-09)">{label}</p>
                                    <p className="text-xs text-(--base-06)">{desc}</p>
                                </div>
                                <LimitField value={settings.limits[key]} onChange={v => setLimit(key, v)} />
                            </div>
                        ))}
                    </div>
                    <p className="text-xs text-(--base-05) mt-2"><span className="font-mono">0</span> = no routes allowed; switch off <span className="font-mono">No limit</span> to type a number. Per-user overrides can be set in user settings.</p>
                </div>

                <div className="border-t border-(--base-03) pt-5">
                    <h3 className="mono-label mb-3">Port Configuration</h3>
                    <div className="space-y-3">
                        {/* MC Port */}
                        <div className={`p-3 rounded-md bg-(--base-02) ${!settings.limits.portMcEnabled ? 'opacity-60' : ''}`}>
                            <div className="flex items-center justify-between mb-2">
                                <div className="flex items-center gap-2">
                                    <span className="text-sm font-semibold text-(--base-09)">Minecraft</span>
                                    <span className="font-mono text-[10px] px-1.5 py-0.5 rounded bg-(--base-03) text-(--base-06)">25565</span>
                                </div>
                                <button type="button" role="switch" aria-checked={settings.limits.portMcEnabled}
                                    onClick={() => setSettings(prev => ({ ...prev, limits: { ...prev.limits, portMcEnabled: !prev.limits.portMcEnabled } }))}
                                    className={`toggle-track ${settings.limits.portMcEnabled ? 'toggle-track-on' : 'toggle-track-off'}`}>
                                    <span className={`toggle-knob ${settings.limits.portMcEnabled ? 'toggle-knob-on' : 'toggle-knob-off'}`} />
                                </button>
                            </div>
                            {settings.limits.portMcEnabled && (
                                <div className="flex items-center justify-between mt-2 pt-2 border-t border-(--base-03)">
                                    <span className="text-xs text-(--base-06)">Max routes on this port</span>
                                    <LimitField value={settings.limits.portMc} onChange={v => setLimit('portMc', v)} />
                                </div>
                            )}
                        </div>
                    </div>
                    <p className="text-xs text-(--base-05) mt-2">Disabled ports block all route creation on that port.</p>
                </div>
                </SettingsGroup>
            </SettingsCard>

            {/* Diagnostics for the records derived from the hoster domains,
                CNAME target and panel URL configured above. Each owns its own
                state, so they sit after the settings rather than between them. */}
            <GatewayDnsCard />

            <DnsCheckCard />

            </fieldset>

            {/* Routing confirmation modal */}
            {confirmModal && (
                <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 backdrop-blur-sm">
                    <div className="card p-6 max-w-md w-full mx-4 space-y-4">
                        <div className="flex items-start gap-3">
                            <div className="w-9 h-9 rounded-md bg-(--warning)/10 border border-(--warning)/20 flex items-center justify-center shrink-0">
                                <AlertTriangle size={18} className="text-(--warning-light)" />
                            </div>
                            <div>
                                <h3 className="font-display font-bold text-(--base-09) text-base">Confirm Routing Change</h3>
                                <p className="text-xs text-(--base-06) mt-0.5">This action will redeploy all active servers</p>
                            </div>
                        </div>
                        <div className="space-y-2 text-sm text-(--base-07)">
                            {routingMode === 'gateway' && origRoutingMode !== 'gateway' && (
                                <p>Switching to <span className="text-(--base-09) font-medium">Gateway</span> mode: all host port bindings will be removed and servers will be redeployed without exposed ports.</p>
                            )}
                            {routingMode !== 'gateway' && origRoutingMode === 'gateway' && (
                                <p>Switching away from <span className="text-(--base-09) font-medium">Gateway</span> mode: new host ports will be assigned to all servers during redeploy.</p>
                            )}
                            {routingMode === 'both' && origRoutingMode !== 'both' && origRoutingMode !== 'gateway' && (
                                <p>Switching to <span className="text-(--base-09) font-medium">Both</span> mode: servers will keep or receive host ports while also supporting gateway routes.</p>
                            )}
                            {fileMode !== origFileMode && (
                                <p>File access mode is changing to <span className="text-(--base-09) font-medium">{FILE_OPTIONS.find(o => o.value === fileMode)?.label}</span>.</p>
                            )}
                            {routingMode === 'ip_port' && origRoutingMode !== 'ip_port' && featureFlags.autoMove && (
                                <div className="alert alert-warning text-xs">
                                    <AlertTriangle size={14} className="shrink-0 mt-0.5" />
                                    <span>Disabling the gateway will turn off Auto-Move and clear every server&apos;s auto-move opt-in.</span>
                                </div>
                            )}
                            <p className="text-(--base-06) text-xs pt-1">Servers are redeployed in batches of 4 with 15s between batches. Each container has a 60s timeout before a force-kill is issued.</p>
                        </div>
                        <div className="flex gap-3 pt-2">
                            <button onClick={() => runSaveRouting(handleSaveRouting)} disabled={applyingRouting} className="btn btn-primary flex-1 disabled:opacity-40">Confirm & Apply</button>
                            <button onClick={() => setConfirmModal(false)} className="btn px-5 py-2 text-sm flex-1">Cancel</button>
                        </div>
                    </div>
                </div>
            )}

            {/* Hoster-domain remove confirmation (optional cascade) */}
            {removeTarget && (
                <div className="modal-overlay animate-fade-in">
                    <div className="modal-panel w-full max-w-md">
                        <div className="modal-header flex items-center justify-between">
                            <h3 className="modal-title flex items-center gap-2 text-(--error-light)">
                                <AlertTriangle size={18} />
                                Remove Hoster Domain
                            </h3>
                            <button onClick={() => setRemoveTarget(null)} className="text-(--base-06) hover:text-(--error-light)" disabled={removeBusy}>
                                <X size={18} />
                            </button>
                        </div>
                        <div className="modal-body space-y-3">
                            <p className="text-sm text-(--base-08)">
                                Remove <code className="font-mono text-(--base-09) bg-(--base-02) px-1.5 py-0.5 rounded">{removeTarget.domain}</code> from the hoster-domain list?
                            </p>
                            <p className="text-xs text-(--base-06)">
                                Users will no longer be able to register new subdomains under it. Existing routes pointing to this domain stay active by default.
                            </p>

                            <label className="flex items-start gap-2 cursor-pointer pt-1">
                                <input
                                    type="checkbox"
                                    checked={removeCascade}
                                    onChange={e => setRemoveCascade(e.target.checked)}
                                    className="checkbox mt-0.5"
                                />
                                <span className="text-sm text-(--base-08)">
                                    Also delete all related routes ending in <code className="font-mono">.{removeTarget.domain}</code>
                                </span>
                            </label>
                            {removeCascade && (
                                <div className="alert alert-error text-xs">
                                    <AlertTriangle size={14} className="shrink-0 mt-0.5" />
                                    <span>
                                        Cascade is permanent. Every server route under this domain will be deleted from the gateway. The confirm button is locked for {removeCountdown}s so you can re-read this.
                                    </span>
                                </div>
                            )}
                        </div>
                        <div className="modal-footer">
                            <button
                                onClick={() => setRemoveTarget(null)}
                                disabled={removeBusy}
                                className="btn btn-secondary"
                            >
                                Cancel
                            </button>
                            <button
                                onClick={confirmRemoveHoster}
                                disabled={removeBusy || (removeCascade && removeCountdown > 0)}
                                className={`btn disabled:opacity-40 ${removeCascade ? 'btn-danger' : 'btn-primary'}`}
                            >
                                {removeBusy
                                    ? <><Spinner size="xs" /> Removing…</>
                                    : (removeCascade && removeCountdown > 0)
                                        ? `Confirm cascade (${removeCountdown}s)`
                                        : removeCascade
                                            ? <><Trash2 size={13} /> Remove + delete routes</>
                                            : 'Remove domain'}
                            </button>
                        </div>
                    </div>
                </div>
            )}
        </SettingsPage>
    );
}

// ─────────────────────────────────────────────
// XDP / DDoS Protection panel
// ─────────────────────────────────────────────

interface XDPConfig {
    enabled: boolean;
    host_mode: boolean;
    interface?: string;
    protected_ports: string;
    rate_limit: number;
    rate_window_ms: number;
    ban_duration_min: number;
    mc_malformed_limit: number;
    mc_malformed_window_min: number;
    mc_invalid_host_limit: number;
    mc_invalid_host_window_min: number;
    mc_ban_duration_min: number;
    whitelist?: string;
}

const XDP_DEFAULTS: XDPConfig = {
    enabled: false,
    host_mode: false,
    interface: '',
    protected_ports: '25565',
    rate_limit: 1000,
    rate_window_ms: 1000,
    ban_duration_min: 30,
    mc_malformed_limit: 20,
    mc_malformed_window_min: 2,
    mc_invalid_host_limit: 100,
    mc_invalid_host_window_min: 2,
    mc_ban_duration_min: 5,
    whitelist: '',
};

async function getXDPConfig(): Promise<{ success: boolean; config?: XDPConfig; present?: boolean }> {
    try {
        const token = localStorage.getItem('authToken') || localStorage.getItem('token');
        const res = await fetch(`${API_URL}/admin/xdp/config`, {
            headers: { Authorization: `Bearer ${token}` },
        });
        return await res.json();
    } catch {
        return { success: false };
    }
}

async function saveXDPConfig(cfg: XDPConfig): Promise<{ success: boolean; message?: string }> {
    try {
        const token = localStorage.getItem('authToken') || localStorage.getItem('token');
        const res = await fetch(`${API_URL}/admin/xdp/config`, {
            method: 'PUT',
            headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${token}` },
            body: JSON.stringify(cfg),
        });
        return await res.json();
    } catch {
        return { success: false, message: 'Network error' };
    }
}

function XDPPanel({ showToast }: { showToast: (msg: string, ok?: boolean) => void }) {
    // XDP runs on the Edges, which only carry traffic in gateway routing
    // mode. Gate the config on the applied mode.
    const { routingMode } = useAppData();
    const gatewayOff = routingMode === 'ip_port';

    const [cfg, setCfg] = useState<XDPConfig>(XDP_DEFAULTS);
    const [loading, setLoading] = useState(true);
    const [saving, setSaving] = useState(false);
    const [present, setPresent] = useState(false);

    // Snapshot of last-saved config for dirty detection.
    const snapshotRef = useRef<XDPConfig | null>(null);

    useEffect(() => {
        getXDPConfig().then(res => {
            if (res.success && res.config) {
                setCfg(res.config);
                setPresent(!!res.present);
                snapshotRef.current = res.config;
            }
            setLoading(false);
        });
    }, []);

    const set = <K extends keyof XDPConfig>(key: K, value: XDPConfig[K]) =>
        setCfg(s => ({ ...s, [key]: value }));

    const handleSave = async (): Promise<boolean> => {
        setSaving(true);
        try {
            const res = await saveXDPConfig(cfg);
            showToast(res.success ? 'XDP config saved — Edges reconcile within 30s.' : (res.message || 'Save failed.'), res.success);
            if (res.success) {
                setPresent(true);
                snapshotRef.current = cfg;
            }
            return !!res.success;
        } finally {
            setSaving(false);
        }
    };

    const handleDiscard = () => {
        if (snapshotRef.current) setCfg(snapshotRef.current);
    };

    const dirty =
        snapshotRef.current !== null &&
        JSON.stringify(cfg) !== JSON.stringify(snapshotRef.current);

    useUnsavedChanges({ dirty, save: handleSave, discard: handleDiscard, saving });

    if (loading) return (
        <div className="space-y-6">
            <SkeletonHeader />
            <SkeletonCard height="h-80" />
            <SkeletonCard height="h-44" />
            <SkeletonCard height="h-72" />
            <SkeletonCard height="h-40" />
        </div>
    );

    return (
        <SettingsPage
            title="DDoS protection (XDP / eBPF)"
            icon={Shield}
            width="full"
            description="Kernel-level packet filtering on every Edge replica. Changes are written to Redis and picked up by all Edges within about 30 seconds; saving triggers an automatic sidecar recreate, which is one to three seconds of downtime for the XDP shield while the Edge proxy itself stays up."
        >
            <div>
                {!present && !gatewayOff && (
                    <div className="mt-3 flex items-start gap-2 p-3 rounded-md bg-(--accent)/5 border border-(--accent-border)/40 text-xs text-(--base-08)">
                        <AlertTriangle size={14} className="text-(--accent-light) mt-0.5 shrink-0" />
                        <span>No XDP config in Redis yet — these are the package defaults. Save once to commit them.</span>
                    </div>
                )}
            </div>

            {gatewayOff && <GatewayDisabledNotice />}
            <fieldset disabled={gatewayOff} className="space-y-6 disabled:opacity-50 border-0 p-0 m-0">

            <SettingsCard title="XDP shield" form={{ dirty, saving, save: handleSave, discard: handleDiscard }}>
            <SettingsGroup title="General" first>

                <div className="flex items-center justify-between">
                    <div>
                        <label className="input-label">XDP Enabled</label>
                        <p className="text-xs text-(--base-06) mt-0.5">Master switch — disables packet filtering when off</p>
                    </div>
                    <button
                        onClick={() => set('enabled', !cfg.enabled)}
                        className={`toggle-track ${cfg.enabled ? 'toggle-track-on' : 'toggle-track-off'}`}
                        role="switch"
                        aria-checked={cfg.enabled}
                    >
                        <span className={`toggle-knob ${cfg.enabled ? 'toggle-knob-on' : 'toggle-knob-off'}`} />
                    </button>
                </div>

                <div className="flex items-center justify-between">
                    <div>
                        <label className="input-label">Host Sidecar Mode</label>
                        <p className="text-xs text-(--base-06) mt-0.5">
                            Run XDP as a separate host-networked container (recommended for production).
                            Toggling this requires an Edge restart.
                        </p>
                    </div>
                    <button
                        onClick={() => set('host_mode', !cfg.host_mode)}
                        className={`toggle-track ${cfg.host_mode ? 'toggle-track-on' : 'toggle-track-off'}`}
                        role="switch"
                        aria-checked={cfg.host_mode}
                    >
                        <span className={`toggle-knob ${cfg.host_mode ? 'toggle-knob-on' : 'toggle-knob-off'}`} />
                    </button>
                </div>

                <div className="flex flex-col gap-[5px]">
                    <label className="input-label">Network Interface</label>
                    <p className="text-xs text-(--base-06) mb-1">Empty = auto-detect all non-loopback IPv4 interfaces (sidecar mode only)</p>
                    <input
                        type="text"
                        value={cfg.interface || ''}
                        onChange={e => set('interface', e.target.value)}
                        placeholder="eth0"
                        className="input-field w-48"
                    />
                </div>

                <div className="flex flex-col gap-[5px]">
                    <label className="input-label">Protected Ports</label>
                    <p className="text-xs text-(--base-06) mb-1">Comma-separated list of ports to apply rate-limiting to</p>
                    <input
                        type="text"
                        value={cfg.protected_ports}
                        onChange={e => set('protected_ports', e.target.value)}
                        placeholder="25565"
                        className="input-field w-72"
                    />
                </div>
            </SettingsGroup>

            <SettingsGroup title="Per-IP rate limiting" description="Drops packets from any source IP that exceeds the threshold within the window. Tripped IPs are blocked for the ban duration.">

                <div className="grid grid-cols-3 gap-4">
                    <div className="flex flex-col gap-[5px]">
                        <label className="input-label">Packets / Window</label>
                        <input type="number" min={1} max={1000000} value={cfg.rate_limit}
                            onChange={e => set('rate_limit', Math.max(1, parseInt(e.target.value) || 1))}
                            className="input-field" />
                    </div>
                    <div className="flex flex-col gap-[5px]">
                        <label className="input-label">Window (ms)</label>
                        <input type="number" min={100} value={cfg.rate_window_ms}
                            onChange={e => set('rate_window_ms', Math.max(100, parseInt(e.target.value) || 1000))}
                            className="input-field" />
                    </div>
                    <div className="flex flex-col gap-[5px]">
                        <label className="input-label">Ban Duration (min)</label>
                        <input type="number" min={1} value={cfg.ban_duration_min}
                            onChange={e => set('ban_duration_min', Math.max(1, parseInt(e.target.value) || 1))}
                            className="input-field" />
                    </div>
                </div>
            </SettingsGroup>

            <SettingsGroup
                title="Minecraft-aware filters"
                description="Protocol-level filters intended to catch scanners and malformed-packet floods that pass plain rate-limiting."
            >
                {/* These values are stored and reach the edge, but nothing feeds
                    the counter that would act on them, so saving a limit here
                    grants no protection. Said plainly rather than left implied:
                    a security control that looks armed and is not is worse than
                    one that is visibly off. */}
                <div className="alert alert-warning text-xs">
                    <AlertTriangle size={14} className="shrink-0 mt-0.5" />
                    <span>
                        <span className="font-medium">Not currently enforced.</span> These limits are
                        saved and delivered to the edge, but no handshake ever reaches the counter, so
                        setting them does not block anything today. Enforcement has to live in the
                        splice sidecar, which is the only component that sees the player&apos;s own IP
                        address. The rate limit and whitelist above are unaffected and do apply.
                    </span>
                </div>

                <div className="grid grid-cols-2 gap-4">
                    <div className="flex flex-col gap-[5px]">
                        <label className="input-label">Malformed / Window</label>
                        <input type="number" min={1} value={cfg.mc_malformed_limit}
                            onChange={e => set('mc_malformed_limit', Math.max(1, parseInt(e.target.value) || 1))}
                            className="input-field" />
                    </div>
                    <div className="flex flex-col gap-[5px]">
                        <label className="input-label">Window (min)</label>
                        <input type="number" min={1} value={cfg.mc_malformed_window_min}
                            onChange={e => set('mc_malformed_window_min', Math.max(1, parseInt(e.target.value) || 1))}
                            className="input-field" />
                    </div>
                    <div className="flex flex-col gap-[5px]">
                        <label className="input-label">Invalid Host / Window</label>
                        <input type="number" min={1} value={cfg.mc_invalid_host_limit}
                            onChange={e => set('mc_invalid_host_limit', Math.max(1, parseInt(e.target.value) || 1))}
                            className="input-field" />
                    </div>
                    <div className="flex flex-col gap-[5px]">
                        <label className="input-label">Window (min)</label>
                        <input type="number" min={1} value={cfg.mc_invalid_host_window_min}
                            onChange={e => set('mc_invalid_host_window_min', Math.max(1, parseInt(e.target.value) || 1))}
                            className="input-field" />
                    </div>
                    <div className="flex flex-col gap-[5px] col-span-2">
                        <label className="input-label">MC Ban Duration (min)</label>
                        <input type="number" min={1} value={cfg.mc_ban_duration_min}
                            onChange={e => set('mc_ban_duration_min', Math.max(1, parseInt(e.target.value) || 1))}
                            className="input-field w-48" />
                    </div>
                </div>
            </SettingsGroup>

            <SettingsGroup
                title="Whitelist"
                description="IPs and CIDRs that bypass all checks, comma-separated. Useful for monitoring services or known crawlers."
            >
                <textarea
                    value={cfg.whitelist || ''}
                    onChange={e => set('whitelist', e.target.value)}
                    placeholder="1.2.3.4, 10.0.0.0/8, 192.168.0.0/16"
                    rows={3}
                    className="input-field font-mono text-xs resize-y"
                />
            </SettingsGroup>
            </SettingsCard>

            </fieldset>
        </SettingsPage>
    );
}

// ─────────────────────────────────────────────
// Hub Admin panel (TP2b)
// ─────────────────────────────────────────────


// ─────────────────────────────────────────────
// Main export: Gateway with left-nav
// ─────────────────────────────────────────────

export default function GatewayTab() {
    const [subTab, setSubTab] = useTabParam<SubTab>(GATEWAY_TABS, 'gateway');


    const showToast = toast;




    // This used to be a SECOND left sidebar, next to the settings sidebar. Two
    // vertical navigations side by side read as one confused one; a tab bar is
    // the only sub-navigation in the panel now, and the deepest it goes.
    return (
        <div className="flex flex-col h-full min-h-0">
            <GuardedTabs items={NAV_ITEMS} active={subTab} onChange={setSubTab} ariaLabel="Gateway settings" />

            <div className="flex-1 overflow-y-auto pt-5">
                {subTab === 'gateway' && <GatewayPanel showToast={showToast} />}
                {subTab === 'xdp' && <XDPPanel showToast={showToast} />}
            </div>


        </div>
    );
}
