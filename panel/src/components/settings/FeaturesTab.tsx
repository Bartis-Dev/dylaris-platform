"use client";

import React, { useState, useEffect, useRef } from 'react';
import { getFeatureSettings, saveFeatureSettings, FeatureSettings } from '@/lib/api';
import { getSystemFeaturesAdmin, updateSystemFeatures, FeatureFlagsAdminPayload } from '@/lib/api/featureFlags';
import { getTabProxySettings, setTabProxySettings, type TabProxySettings } from '@/lib/api/tabProxySettings';
import { getCoreStorage } from '@/lib/api/coreStorage';
import { canSaveCoreStorage } from '@/lib/coreStorage';
import { Network, Globe, LifeBuoy, Package, Move, AlertTriangle, Server, Key, ChevronDown, ChevronRight } from 'lucide-react';
import { getCatalog, type CatalogScope } from '@/lib/api/authzCatalog';
import { SkeletonHeader, SkeletonCard } from '@/components/Skeleton';
import { useUnsavedChanges } from '@/components/settings/UnsavedChanges';
import { useAppData } from '@/lib/AppDataContext';
import { toast } from '@/components/ui/Toast';
import HelpTip from '@/components/ui/HelpTip';

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

    // Platform-wide subsystem toggles (tickets, modpacks, ...), behind
    // /api/admin/settings/features.
    //
    // These used to persist on every click, and say nothing when they worked.
    // Three save models sat in one scroll on this page - these seven flags
    // autosaving silently, the two tab-proxy switches autosaving on click and
    // its numbers on blur, and everything else waiting for the save bar - on
    // controls that look identical to each other. Now all three are dirty
    // states and the bar commits them.
    const [platformFlags, setPlatformFlags] = useState<FeatureFlagsAdminPayload>({ tickets: false, modpacks: true, modpackAuthoring: false, autoMove: false, byon: false, userApiKeys: false, userApiKeyAllowedCaps: '' });
    const [platformSaving, setPlatformSaving] = useState(false);
    const platformSnapshot = useRef<FeatureFlagsAdminPayload | null>(null);

    // What the NEXT flip of "open authoring to users" should do to per-user
    // overrides an admin set by hand. Component state on purpose, not a stored
    // setting: it describes one action, so persisting it would silently widen a
    // later toggle nobody connected to this checkbox.
    const [applyToManual, setApplyToManual] = useState(false);
    // Rows the last authoring flip actually rewrote, straight from the server, so
    // the admin sees what the switch did instead of inferring it.
    const [lastBulk, setLastBulk] = useState<number | null>(null);

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
    const tabProxySnapshot = useRef<TabProxySettings | null>(null);

    // The capability whitelist for user API keys, rendered from the authz
    // catalog rather than a hardcoded list, so a capability added to the
    // catalog shows up here without a second edit. PANEL caps are left out
    // because a key can never carry one (authz.ValidKeyCap rejects them).
    const [keyCatalog, setKeyCatalog] = useState<CatalogScope[]>([]);
    const [keyCapsOpen, setKeyCapsOpen] = useState(false);

    // Snapshot of last-saved settings for dirty detection.
    const snapshotRef = useRef<FeatureSettings | null>(null);

    const showToast = (msg: string, ok = true) => toast(msg, ok);

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
            if (res.success && res.settings) {
                setTabProxy(res.settings);
                tabProxySnapshot.current = res.settings;
            }
        });
        getCatalog().then(res => {
            if (res.success && res.catalog) {
                setKeyCatalog(res.catalog.filter(sc => sc.scope !== 'panel'));
            }
        });
    }, []);

    // An edit to a platform flag. Local; the bar commits it.
    const editPlatformFlag = (key: keyof FeatureFlagsAdminPayload, value: boolean | string) => {
        setPlatformFlags(prev => ({ ...prev, [key]: value }));
        setLastBulk(null);
    };

    const platformDirty =
        platformSnapshot.current !== null &&
        JSON.stringify(platformFlags) !== JSON.stringify(platformSnapshot.current);

    const savePlatform = async (): Promise<boolean> => {
        const prev = platformSnapshot.current;
        if (!prev) return false;
        // applyAuthoringToManual rides along only when the toggle it describes
        // actually moved; sending it otherwise would be a standing instruction
        // the admin never gave.
        const payload: FeatureFlagsAdminPayload =
            platformFlags.modpackAuthoring !== prev.modpackAuthoring
                ? { ...platformFlags, applyAuthoringToManual: applyToManual }
                : platformFlags;
        setPlatformSaving(true);
        try {
            const res = await updateSystemFeatures(payload);
            if (!res.success) {
                showToast(res.message || 'Save failed.', false);
                return false;
            }
            const stored = res.features ?? platformFlags;
            setPlatformFlags(stored);
            platformSnapshot.current = stored;
            if (typeof res.usersChanged === 'number') setLastBulk(res.usersChanged);
            showToast('Feature switches saved.');
            return true;
        } finally {
            setPlatformSaving(false);
        }
    };

    const discardPlatform = () => {
        if (platformSnapshot.current) setPlatformFlags(platformSnapshot.current);
        setLastBulk(null);
    };

    useUnsavedChanges({ dirty: platformDirty, save: savePlatform, discard: discardPlatform, saving: platformSaving });

    const tabProxyDirty =
        tabProxySnapshot.current !== null &&
        JSON.stringify(tabProxy) !== JSON.stringify(tabProxySnapshot.current);

    const saveTabProxy = async (): Promise<boolean> => {
        setTabProxySaving(true);
        try {
            const res = await setTabProxySettings(tabProxy);
            if (!res.success) {
                showToast(res.message || 'Save failed.', false);
                return false;
            }
            const stored = res.settings ?? tabProxy;
            setTabProxy(stored);
            tabProxySnapshot.current = stored;
            showToast('Tab proxy settings saved.');
            return true;
        } finally {
            setTabProxySaving(false);
        }
    };

    const discardTabProxy = () => {
        if (tabProxySnapshot.current) setTabProxy(tabProxySnapshot.current);
    };

    useUnsavedChanges({ dirty: tabProxyDirty, save: saveTabProxy, discard: discardTabProxy, saving: tabProxySaving });

    const handleSave = async (): Promise<boolean> => {
        setSaving(true);
        try {
            const res = await saveFeatureSettings(settings);
            if (res.success) {
                showToast('Feature settings saved.');
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

    // The whitelist travels as a comma-separated string because that is what the
    // setting stores; the picker works on a Set and writes it back the same way.
    const allowedKeyCaps = new Set(
        platformFlags.userApiKeyAllowedCaps.split(',').map(c => c.trim()).filter(Boolean),
    );
    const toggleKeyCap = (id: string) => {
        const next = new Set(allowedKeyCaps);
        if (next.has(id)) next.delete(id); else next.add(id);
        editPlatformFlag('userApiKeyAllowedCaps', Array.from(next).join(','));
    };

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
                        disabled={(!platformFlags.tickets && storageConfigured !== true)}
                        onClick={() => editPlatformFlag('tickets', !platformFlags.tickets)}
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

            {/* Two switches, one subsystem. "Modpacks" turns it on for ADMINS;
                "Open authoring to users" widens it to everyone else. Nested
                rather than side by side because the second is meaningless without
                the first, and the backend folds it to false when Modpacks goes
                off - so the nesting is the real relationship, not decoration.
                The Modpacks navbar entry follows both: it appears with the
                subsystem and switches from admin-only to everyone with authoring. */}
            <div className="card p-5 space-y-4">
                <div className="flex items-center justify-between">
                    <div className="flex items-center gap-3">
                        <div className="w-9 h-9 rounded-md bg-(--base-03) flex items-center justify-center">
                            <Package size={18} className="text-(--accent-light)" />
                        </div>
                        <div>
                            <div className="font-medium text-sm text-(--base-09)">Modpacks</div>
                            <div className="text-xs text-(--base-06)">
                                Turns on the modpack builder, storage and Solder endpoints for <strong>admins</strong>. When off, modpack write endpoints return 503 and the Modpacks nav entry is hidden. Existing modpacks stay readable and downloadable.
                            </div>
                        </div>
                    </div>
                    <button
                        type="button"
                        role="switch"
                        aria-checked={platformFlags.modpacks}
                        
                        onClick={() => editPlatformFlag('modpacks', !platformFlags.modpacks)}
                        className={`toggle-track ${platformFlags.modpacks ? 'toggle-track-on' : 'toggle-track-off'}`}
                    >
                        <span className={`toggle-knob ${platformFlags.modpacks ? 'toggle-knob-on' : 'toggle-knob-off'}`} />
                    </button>
                </div>

                <div className={`border-t border-(--base-03) pt-4 ${platformFlags.modpacks ? '' : 'opacity-60'}`}>
                    <div className="flex items-center justify-between gap-4">
                        <div className="min-w-0">
                            <div className="text-sm font-medium text-(--base-09)">Open authoring to users</div>
                            <p className="text-xs text-(--base-06)">
                                Lets non-admin users create and publish their own modpacks. Off means admins only. You can still revoke a single user afterwards under Settings -&gt; Users.
                            </p>
                        </div>
                        <button
                            type="button"
                            role="switch"
                            aria-checked={platformFlags.modpackAuthoring}
                            disabled={!platformFlags.modpacks}
                            onClick={() => editPlatformFlag('modpackAuthoring', !platformFlags.modpackAuthoring)}
                            className={`toggle-track ${platformFlags.modpackAuthoring ? 'toggle-track-on' : 'toggle-track-off'} disabled:cursor-not-allowed`}
                        >
                            <span className={`toggle-knob ${platformFlags.modpackAuthoring ? 'toggle-knob-on' : 'toggle-knob-off'}`} />
                        </button>
                    </div>

                    {/* Checked = the next flip of the switch above also rewrites
                        users an admin set by hand. Unchecked (the default) keeps
                        those decisions. Deliberately NOT a stored setting: it
                        describes what one change should do, so remembering it
                        across sessions would silently widen a later toggle. */}
                    <label className={`flex items-start gap-2 mt-3 text-xs ${platformFlags.modpacks ? 'text-(--base-07) cursor-pointer' : 'text-(--base-06)'}`}>
                        <input
                            type="checkbox"
                            checked={applyToManual}
                            disabled={!platformFlags.modpacks}
                            onChange={e => setApplyToManual(e.target.checked)}
                            className="checkbox mt-0.5 shrink-0"
                        />
                        <span>
                            Also apply to users I set by hand. Off keeps every per-user override; on resets them to follow this switch from now on.
                        </span>
                    </label>

                    {lastBulk !== null && (
                        <p className="mt-2 text-xs font-mono text-(--base-06)">
                            {lastBulk === 0
                                ? 'No user rows needed changing.'
                                : `${lastBulk} user${lastBulk === 1 ? '' : 's'} updated.`}
                        </p>
                    )}
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
                        disabled={gatewayOff}
                        onClick={() => editPlatformFlag('autoMove', !platformFlags.autoMove)}
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
            {/* Disabled while routing is on IP:Port, same as Auto-Move above. An
                external node FORCES gateway routing locally (NODE_EXTERNAL), so
                with no gateway a tenant node has nothing to join: the flag would
                switch on an enrolment surface for machines that can never
                connect. The panel therefore ANDs this flag with the live routing
                mode (see byonEnabled in AppDataContext). */}
            <div className={`card p-5 ${gatewayOff ? 'opacity-60' : ''}`}>
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
                        disabled={gatewayOff}
                        onClick={() => editPlatformFlag('byon', !platformFlags.byon)}
                        className={`toggle-track ${platformFlags.byon ? 'toggle-track-on' : 'toggle-track-off'} disabled:cursor-not-allowed`}
                    >
                        <span className={`toggle-knob ${platformFlags.byon ? 'toggle-knob-on' : 'toggle-knob-off'}`} />
                    </button>
                </div>
                {gatewayOff && (
                    <p className="flex items-start gap-1.5 text-xs text-(--warning-light) mt-3">
                        <AlertTriangle size={12} className="mt-0.5 shrink-0" />
                        <span>Requires gateway routing. A tenant node forces gateway routing on its own side, so it has nothing to join while Game Traffic is on IP:Port.</span>
                    </p>
                )}
            </div>

            {/* User API keys. Two controls, because "may users hold keys" and
                "which capabilities may they put on one" are different decisions:
                an operator can open the feature without opening the whole
                capability catalogue. Both are enforced at MINT and at USE - a
                key created before the switch was turned off stops working, which
                is what an operator who turned it off means. */}
            <div className="card p-5">
                <div className="flex items-center justify-between">
                    <div className="flex items-center gap-3">
                        <div className="w-9 h-9 rounded-md bg-(--base-03) flex items-center justify-center">
                            <Key size={18} className="text-(--accent-light)" />
                        </div>
                        <div>
                            <div className="font-medium text-sm text-(--base-09)">User API Keys</div>
                            <div className="text-xs text-(--base-06)">
                                Lets non-admins mint scoped keys for the <code className="font-mono">/api/external</code> automation surface. Admins can always mint their own.
                            </div>
                        </div>
                    </div>
                    <button
                        type="button"
                        role="switch"
                        aria-checked={platformFlags.userApiKeys}
                        
                        onClick={() => editPlatformFlag('userApiKeys', !platformFlags.userApiKeys)}
                        className={`toggle-track ${platformFlags.userApiKeys ? 'toggle-track-on' : 'toggle-track-off'} disabled:cursor-not-allowed`}
                    >
                        <span className={`toggle-knob ${platformFlags.userApiKeys ? 'toggle-knob-on' : 'toggle-knob-off'}`} />
                    </button>
                </div>

                {platformFlags.userApiKeys && (
                    <div className="mt-4 pt-4 border-t border-(--base-03)">
                        <button
                            type="button"
                            onClick={() => setKeyCapsOpen(o => !o)}
                            aria-expanded={keyCapsOpen}
                            className="flex items-center gap-2 w-full text-left group"
                        >
                            {keyCapsOpen
                                ? <ChevronDown size={14} className="text-(--base-06)" />
                                : <ChevronRight size={14} className="text-(--base-06)" />}
                            <span className="text-sm font-medium text-(--base-09) group-hover:text-(--accent-light) transition-colors">Allowed capabilities</span>
                            <HelpTip label="About allowed capabilities">
                                <p className="mb-2">
                                    <strong>Selecting none is not &quot;none allowed&quot;.</strong> An empty
                                    list means no EXTRA restriction: a user may put any capability on
                                    a key that they already hold themselves.
                                </p>
                                <p className="mb-2">
                                    Selecting some narrows that further - a key may then carry only
                                    what is ticked here, and still only what its creator holds. It can
                                    never exceed the person who made it either way.
                                </p>
                                <p>
                                    Enforced when a key is minted AND every time one is used, so
                                    tightening this stops keys that already exist.
                                </p>
                            </HelpTip>
                            <span className="ml-auto text-xs text-(--base-06)">
                                {allowedKeyCaps.size === 0 ? 'No restriction' : `${allowedKeyCaps.size} selected`}
                            </span>
                        </button>
                        <p className="text-xs text-(--base-06) mt-2 ml-6">
                            {allowedKeyCaps.size === 0
                                ? 'Users may put any capability on a key that they already hold themselves. Select some to narrow that further.'
                                : 'Users may only put these on a key, and still only ones they already hold themselves.'}
                        </p>

                        {keyCapsOpen && (
                            <div className="mt-3 ml-6 space-y-4">
                                {allowedKeyCaps.size > 0 && (
                                    <button
                                        type="button"
                                        
                                        onClick={() => editPlatformFlag('userApiKeyAllowedCaps', '')}
                                        className="btn btn-ghost btn-sm"
                                    >
                                        Clear restriction
                                    </button>
                                )}
                                {keyCatalog.map(sc => (
                                    <div key={sc.scope} className="space-y-3">
                                        <div className="text-xs font-semibold uppercase tracking-wide text-(--base-06)">
                                            {sc.scope === 'server' ? 'Per server' : 'Account-wide'}
                                        </div>
                                        {sc.categories.map(cat => (
                                            <div key={cat.category}>
                                                <div className="text-xs text-(--base-07) mb-1">{cat.category}</div>
                                                <div className="flex flex-wrap gap-x-4 gap-y-1.5">
                                                    {cat.capabilities.map(c => (
                                                        <label key={c.id} className="flex items-center gap-2 text-xs text-(--base-08) cursor-pointer">
                                                            <input
                                                                type="checkbox"
                            className="checkbox"
                                                                checked={allowedKeyCaps.has(c.id)}
                                                                
                                                                onChange={() => toggleKeyCap(c.id)}
                                                            />
                                                            <span>{c.label}</span>
                                                            <code className="font-mono text-(--base-06)">{c.id}</code>
                                                        </label>
                                                    ))}
                                                </div>
                                            </div>
                                        ))}
                                    </div>
                                ))}
                            </div>
                        )}
                    </div>
                )}
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
                    <button type="button" role="switch" aria-checked={tabProxy.enabled} 
                        onClick={() => setTabProxy(t => ({ ...t, enabled: !t.enabled }))}
                        className={`toggle-track ${tabProxy.enabled ? 'toggle-track-on' : 'toggle-track-off'}`}>
                        <span className={`toggle-knob ${tabProxy.enabled ? 'toggle-knob-on' : 'toggle-knob-off'}`} />
                    </button>
                </div>
                <div className="flex items-center justify-between border-t border-(--base-03) pt-3">
                    <div>
                        <div className="text-sm font-medium text-(--base-09)">Allow public share links</div>
                        <p className="text-xs text-(--base-06)">Let owners publish anonymous (no-login) share links.</p>
                    </div>
                    <button type="button" role="switch" aria-checked={tabProxy.allowPublicLinks} 
                        onClick={() => setTabProxy(t => ({ ...t, allowPublicLinks: !t.allowPublicLinks }))}
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
                        <input type="number" min={1} value={tabProxy.maxPerServer} 
                            onChange={e => setTabProxy({ ...tabProxy, maxPerServer: Number(e.target.value) })}
                            className="input-field w-full" />
                    </div>
                    <div>
                        <label className="input-label">Max share links / user</label>
                        <input type="number" min={1} value={tabProxy.maxShareLinksPerUser} 
                            onChange={e => setTabProxy({ ...tabProxy, maxShareLinksPerUser: Number(e.target.value) })}
                            className="input-field w-full" />
                    </div>
                </div>
            </div>

            {/* Toast */}
        </div>
    );
}
