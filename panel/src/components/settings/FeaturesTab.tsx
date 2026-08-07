"use client";

import React, { useState, useEffect, useRef } from 'react';
import { getFeatureSettings, saveFeatureSettings, FeatureSettings } from '@/lib/api';
import { getSystemFeaturesAdmin, updateSystemFeatures, FeatureFlagsAdminPayload } from '@/lib/api/featureFlags';
import { getTabProxySettings, setTabProxySettings, type TabProxySettings } from '@/lib/api/tabProxySettings';
import { getCoreStorage } from '@/lib/api/coreStorage';
import { canSaveCoreStorage } from '@/lib/coreStorage';
import { CircleCheck, CircleAlert, Network, Globe, LifeBuoy, Package, Move, AlertTriangle, Server } from 'lucide-react';
import { SkeletonHeader, SkeletonCard } from '@/components/Skeleton';
import { useUnsavedChanges } from '@/components/settings/UnsavedChanges';
import { useAppData } from '@/lib/AppDataContext';

export default function FeaturesTab() {
    // Auto-move is gateway-only — gate its toggle on the live routing mode.
    // ip_port means the gateway is off, so enabling auto-move would 409 on the
    // backend; we disable the control instead of letting that happen.
    const { routingMode, coreInfo } = useAppData();
    const gatewayOff = routingMode === 'ip_port';

    // Seeded with the SERVER's default, not an optimistic one: Core reads proxy
    // as `val != "false"` (default on). This is only ever visible if the load
    // below fails - the normal path renders a skeleton until the fetch
    // resolves - but a settings screen that shows "on" for a subsystem that is
    // off is worse than one that errs the other way. Same reasoning as
    // storageConfigured's `null` below.
    const [settings, setSettings] = useState<FeatureSettings>({ proxyEnabled: true });
    const [loading, setLoading] = useState(true);
    const [saving, setSaving] = useState(false);
    const [toast, setToast] = useState<{ msg: string; ok: boolean } | null>(null);

    // Platform-wide subsystem toggles (tickets, modpacks) — these live behind
    // /api/admin/settings/features and save-on-click independently of the
    // proxy/gateway settings above. Each flip persists immediately so the
    // admin doesn't have to remember a Save bar for a dangerous gate.
    const [platformFlags, setPlatformFlags] = useState<FeatureFlagsAdminPayload>({ tickets: false, modpacks: true, autoMove: false, byon: false });
    const [platformSaving, setPlatformSaving] = useState<keyof FeatureFlagsAdminPayload | null>(null);

    // Tickets requires Core file storage (attachments + backups need a durable
    // off-host home) - the backend 409s ("core_storage_required") on enable
    // otherwise. Fetched once so the toggle can be disabled with a hint
    // instead of letting the admin hit a failed save. Starts as `null`
    // (unknown) rather than defaulting to "configured", so there is no window
    // where the toggle is optimistically enabled before the fetch resolves;
    // `storageConfigured !== true` treats both "unknown" and "confirmed not
    // configured" as disabled. If the fetch itself fails, storageConfigured
    // is set to `true` (usable) rather than left stuck at "unknown" forever -
    // the backend still correctly 409s on enable if storage really isn't
    // configured, so failing open here just avoids a dead toggle.
    const [storageConfigured, setStorageConfigured] = useState<boolean | null>(null);

    // WS5 custom-tab reverse proxy toggles - same save-on-click/blur pattern
    // as the platform flags above, but its own admin settings endpoint.
    const [tabProxy, setTabProxy] = useState<TabProxySettings>({ enabled: false, allowPublicLinks: false, maxPerServer: 10, maxShareLinksPerUser: 20 });
    const [tabProxySaving, setTabProxySaving] = useState(false);

    // Snapshot of last-saved settings for dirty detection.
    const snapshotRef = useRef<FeatureSettings | null>(null);

    const showToast = (msg: string, ok = true) => {
        setToast({ msg, ok });
        setTimeout(() => setToast(null), 3500);
    };

    useEffect(() => {
        getFeatureSettings().then(res => {
            if (res.success && res.settings) {
                setSettings(res.settings);
                snapshotRef.current = res.settings;
            } else {
                // Say so instead of rendering the seed values as if they were
                // fetched. snapshotRef stays null on this path, which keeps
                // `dirty` false and the save bar hidden - so the admin cannot
                // write these unconfirmed values back over the real ones - but
                // without a message the screen just quietly lies about what is
                // enabled.
                showToast(res.message || 'Failed to load feature settings - shown values are unconfirmed', false);
            }
            setLoading(false);
        });
        getSystemFeaturesAdmin().then(res => {
            if (res.success && res.features) setPlatformFlags(res.features);
        });
        getCoreStorage().then(res => {
            if (!res.success) {
                // Fetch/parse failed - fail open so the toggle isn't stuck
                // disabled indefinitely; the backend still enforces the 409.
                setStorageConfigured(true);
                return;
            }
            setStorageConfigured(!!res.settings && canSaveCoreStorage(res.settings));
        });
        getTabProxySettings().then(res => {
            if (res.success && res.settings) setTabProxy(res.settings);
        });
    }, []);

    // Save-on-click for the platform-wide bundle. We send BOTH keys every
    // time so the wire shape stays predictable even when only one toggle
    // flipped; cheaper than tracking partial dirty state for a 2-bool form.
    const savePlatformFlag = async (key: keyof FeatureFlagsAdminPayload, value: boolean) => {
        if (platformSaving) return;
        const prev = platformFlags;
        const next = { ...platformFlags, [key]: value };
        setPlatformFlags(next);
        setPlatformSaving(key);
        const res = await updateSystemFeatures(next);
        if (!res.success) {
            setPlatformFlags(prev);
            showToast(res.message || 'Save failed.', false);
        } else if (res.features) {
            setPlatformFlags(res.features);
        }
        setPlatformSaving(null);
    };

    // Save-on-click/blur for the tab-proxy settings bundle - sends the full
    // payload every round-trip (mirrors savePlatformFlag's shape discipline).
    const saveTabProxy = async (next: TabProxySettings) => {
        if (tabProxySaving) return;
        const prev = tabProxy;
        setTabProxy(next);
        setTabProxySaving(true);
        const res = await setTabProxySettings(next);
        if (!res.success) {
            setTabProxy(prev);
            showToast(res.message || 'Save failed.', false);
        } else if (res.settings) {
            setTabProxy(res.settings);
        }
        setTabProxySaving(false);
    };

    const handleSave = async () => {
        setSaving(true);
        const res = await saveFeatureSettings(settings);
        if (res.success) {
            showToast('Feature settings saved.');
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

    if (loading) return (
        <div className="max-w-2xl space-y-6">
            <SkeletonHeader />
            <SkeletonCard height="h-20" />
            <SkeletonCard height="h-20" />
        </div>
    );

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

            {/* No Gateway toggle here: the gateway is on exactly when Game
                Traffic routes through it, so the routing-mode selector in the
                Gateway sub-tab is its only switch. */}

            {/* Platform-wide subsystem toggles. Save on click — each flip
                immediately blocks/restores the whole API surface so an
                explicit Save bar would be a footgun. */}
            <div className="card p-5">
                <div className="flex items-center justify-between">
                    <div className="flex items-center gap-3">
                        <div className="w-9 h-9 rounded-md bg-(--base-03) flex items-center justify-center">
                            <LifeBuoy size={18} className="text-(--accent-light)" />
                        </div>
                        <div>
                            <div className="font-medium text-sm text-(--base-09)">Ticket System</div>
                            <div className="text-xs text-(--base-06)">
                                Enables the tickets module, inbox, attachments, canned responses and notifications. When off, all <code className="font-mono">/api/tickets/*</code> endpoints return 503 and the Tickets nav entry is hidden.
                            </div>
                        </div>
                    </div>
                    <button
                        type="button"
                        role="switch"
                        aria-checked={platformFlags.tickets}
                        disabled={platformSaving !== null || (!platformFlags.tickets && storageConfigured !== true)}
                        onClick={() => savePlatformFlag('tickets', !platformFlags.tickets)}
                        className={`toggle-track ${platformFlags.tickets ? 'toggle-track-on' : 'toggle-track-off'} disabled:cursor-not-allowed`}
                    >
                        <span className={`toggle-knob ${platformFlags.tickets ? 'toggle-knob-on' : 'toggle-knob-off'}`} />
                    </button>
                </div>
                {!platformFlags.tickets && storageConfigured !== true && (
                    <p className="flex items-start gap-1.5 text-xs text-(--warning-light) mt-3">
                        <AlertTriangle size={12} className="mt-0.5 shrink-0" />
                        <span>Requires Core file storage. Configure and save it under Settings -&gt; Core Storage first.</span>
                    </p>
                )}
            </div>

            <div className="card p-5">
                <div className="flex items-center justify-between">
                    <div className="flex items-center gap-3">
                        <div className="w-9 h-9 rounded-md bg-(--base-03) flex items-center justify-center">
                            <Package size={18} className="text-(--accent-light)" />
                        </div>
                        <div>
                            <div className="font-medium text-sm text-(--base-09)">Modpack Authoring</div>
                            <div className="text-xs text-(--base-06)">
                                When off, modpack write endpoints return 503 and the Modpacks UI is hidden for non-admins. Existing modpacks stay readable and downloadable.
                            </div>
                        </div>
                    </div>
                    <button
                        type="button"
                        role="switch"
                        aria-checked={platformFlags.modpacks}
                        disabled={platformSaving !== null}
                        onClick={() => savePlatformFlag('modpacks', !platformFlags.modpacks)}
                        className={`toggle-track ${platformFlags.modpacks ? 'toggle-track-on' : 'toggle-track-off'}`}
                    >
                        <span className={`toggle-knob ${platformFlags.modpacks ? 'toggle-knob-on' : 'toggle-knob-off'}`} />
                    </button>
                </div>
            </div>

            {/* Auto-Move — gateway-only. Greyed + the toggle is hard-disabled
                while routing is on IP:Port, since enabling it then would 409
                on the backend (gateway_required). */}
            <div className={`card p-5 ${gatewayOff ? 'opacity-60' : ''}`}>
                <div className="flex items-center justify-between">
                    <div className="flex items-center gap-3">
                        <div className="w-9 h-9 rounded-md bg-(--base-03) flex items-center justify-center">
                            <Move size={18} className="text-(--accent-light)" />
                        </div>
                        <div>
                            <div className="font-medium text-sm text-(--base-09)">Auto-Move</div>
                            <div className="text-xs text-(--base-06)">
                                Lets the rebalance worker migrate opted-in servers to a less-loaded node when their current node is overloaded. Per-server opt-in lives in each server&apos;s resource settings. Migration is gateway-only — the route keeps the player address stable across the node change.
                            </div>
                        </div>
                    </div>
                    <button
                        type="button"
                        role="switch"
                        aria-checked={platformFlags.autoMove}
                        disabled={platformSaving !== null || gatewayOff}
                        onClick={() => savePlatformFlag('autoMove', !platformFlags.autoMove)}
                        className={`toggle-track ${platformFlags.autoMove ? 'toggle-track-on' : 'toggle-track-off'} disabled:cursor-not-allowed`}
                    >
                        <span className={`toggle-knob ${platformFlags.autoMove ? 'toggle-knob-on' : 'toggle-knob-off'}`} />
                    </button>
                </div>
                {gatewayOff && (
                    <p className="flex items-start gap-1.5 text-xs text-(--warning-light) mt-3">
                        <AlertTriangle size={12} className="mt-0.5 shrink-0" />
                        <span>Requires gateway routing. Switch Game Traffic to Gateway or Both first.</span>
                    </p>
                )}
            </div>

            {/* BYON (Bring Your Own Node). Platform-wide subsystem toggle wired
                exactly like tickets/modpacks/auto-move above: same read of the
                feature-settings payload, same save-on-click via savePlatformFlag.
                The PUT publishes features.changed, which AppDataContext consumes
                to refresh featureFlags, so the Settings nav's BYON group
                (Usage/Billing/Plans) appears/disappears without a manual reload. */}
            <div className="card p-5">
                <div className="flex items-center justify-between">
                    <div className="flex items-center gap-3">
                        <div className="w-9 h-9 rounded-md bg-(--base-03) flex items-center justify-center">
                            <Server size={18} className="text-(--accent-light)" />
                        </div>
                        <div>
                            <div className="font-medium text-sm text-(--base-09)">BYON (Bring Your Own Node)</div>
                            <div className="text-xs text-(--base-06)">
                                Enabling BYON turns on tenant node enrollment, traffic metering and the Usage, Billing and Plans admin settings (which stay hidden while it is off).
                            </div>
                        </div>
                    </div>
                    <button
                        type="button"
                        role="switch"
                        aria-checked={platformFlags.byon}
                        disabled={platformSaving !== null}
                        onClick={() => savePlatformFlag('byon', !platformFlags.byon)}
                        className={`toggle-track ${platformFlags.byon ? 'toggle-track-on' : 'toggle-track-off'} disabled:cursor-not-allowed`}
                    >
                        <span className={`toggle-knob ${platformFlags.byon ? 'toggle-knob-on' : 'toggle-knob-off'}`} />
                    </button>
                </div>
            </div>

            {/* WS5 custom-tab reverse proxy */}
            <div className="card p-5 space-y-4">
                <div className="flex items-center justify-between">
                    <div className="flex items-center gap-3">
                        <div className="w-9 h-9 rounded-md bg-(--base-03) flex items-center justify-center">
                            <Globe size={18} className="text-(--accent-light)" />
                        </div>
                        <div>
                            <div className="font-medium text-sm text-(--base-09)">Custom-Tab Reverse Proxy</div>
                            <div className="text-xs text-(--base-06)">
                                Streams a server container&apos;s web UI (BlueMap, squaremap, Dynmap) through Dylaris so no public port is needed. When off, proxied tabs and share links stop serving.
                            </div>
                        </div>
                    </div>
                    <button type="button" role="switch" aria-checked={tabProxy.enabled} disabled={tabProxySaving}
                        onClick={() => saveTabProxy({ ...tabProxy, enabled: !tabProxy.enabled })}
                        className={`toggle-track ${tabProxy.enabled ? 'toggle-track-on' : 'toggle-track-off'}`}>
                        <span className={`toggle-knob ${tabProxy.enabled ? 'toggle-knob-on' : 'toggle-knob-off'}`} />
                    </button>
                </div>
                <div className="flex items-center justify-between border-t border-(--base-03) pt-3">
                    <div>
                        <div className="text-sm font-medium text-(--base-09)">Allow public share links</div>
                        <p className="text-xs text-(--base-06)">Let owners publish anonymous (no-login) share links.</p>
                    </div>
                    <button type="button" role="switch" aria-checked={tabProxy.allowPublicLinks} disabled={tabProxySaving}
                        onClick={() => saveTabProxy({ ...tabProxy, allowPublicLinks: !tabProxy.allowPublicLinks })}
                        className={`toggle-track ${tabProxy.allowPublicLinks ? 'toggle-track-on' : 'toggle-track-off'}`}>
                        <span className={`toggle-knob ${tabProxy.allowPublicLinks ? 'toggle-knob-on' : 'toggle-knob-off'}`} />
                    </button>
                </div>
                {/* Same-origin security note (WS5 C1 follow-up, closed by spec B5):
                    when origin isolation is active, proxied content is served from
                    a dedicated, different-origin proxy host, so a compromised/
                    malicious container can no longer read the viewer's panel
                    session. When it is NOT active (older Core, or disabled),
                    proxied content still runs on the panel's own origin and the
                    original warning applies. */}
                {coreInfo?.tabProxyIsolationActive ? (
                    <p className="flex items-start gap-1.5 text-xs text-(--success-light)">
                        <AlertTriangle size={12} className="mt-0.5 shrink-0" />
                        <span>
                            Origin isolation is active: proxied tab content is served from a
                            dedicated, different-origin proxy host, not the panel&apos;s own origin.
                            Public share links are safe to enable on a multi-tenant instance.
                        </span>
                    </p>
                ) : (
                    <p className="flex items-start gap-1.5 text-xs text-(--warning-light)">
                        <AlertTriangle size={12} className="mt-0.5 shrink-0" />
                        <span>
                            Proxied tab content runs on this panel&apos;s own origin. On a shared or
                            multi-tenant instance, a malicious server container could read a viewer&apos;s
                            panel session. Safe for single-operator / self-host. Do not enable public
                            share links (or expose proxied tabs to users who do not fully trust the
                            target container) on a multi-user instance until origin-isolated proxying ships.
                        </span>
                    </p>
                )}
                <div className="grid grid-cols-2 gap-3 border-t border-(--base-03) pt-3">
                    <div>
                        <label className="input-label">Max proxied tabs / server</label>
                        <input type="number" min={1} value={tabProxy.maxPerServer} disabled={tabProxySaving}
                            onChange={e => setTabProxy({ ...tabProxy, maxPerServer: Number(e.target.value) })}
                            onBlur={() => saveTabProxy(tabProxy)}
                            className="input-field w-full" />
                    </div>
                    <div>
                        <label className="input-label">Max share links / user</label>
                        <input type="number" min={1} value={tabProxy.maxShareLinksPerUser} disabled={tabProxySaving}
                            onChange={e => setTabProxy({ ...tabProxy, maxShareLinksPerUser: Number(e.target.value) })}
                            onBlur={() => saveTabProxy(tabProxy)}
                            className="input-field w-full" />
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
