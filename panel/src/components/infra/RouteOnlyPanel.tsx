"use client";

import React, { useCallback, useEffect, useState } from 'react';
import { Plus, Trash2, Loader2, Server, Link2, Copy, Check, ShieldCheck } from 'lucide-react';
import RouteDomainPicker, { DomainAvailability } from '@/components/RouteDomainPicker';
import {
    CreateRouteRequest, LinkRoute, LinkKit, MintedLinkKit,
    getLinkRoutes, createLinkRoute, deleteLinkRoute,
    listLinkKits, mintLinkKit, revokeLinkKit,
} from '@/lib/api';
import { confirmDialog } from '@/components/ui/ConfirmDialog';
import { SkeletonCard } from '@/components/Skeleton';
import { DeployKit, NotIncluded } from '@/components/infra/DeployKit';
import type { WarpDeployConfig } from '@/lib/api/warpDeployConfig';

// Named RouteOnlyPanel, not RoutesPanel: views/infrastructure/RoutesPanel is
// the ADMIN one (fleet routes across edges) and the two are easy to import by
// mistake for each other.
//
// Route-only ("via Link"): a DDoS-protected address pointed at a server the user
// runs on their OWN machine, reached through their own outbound Link tunnel. No
// managed node, no open ports - the customer runs warp + link with a "link kit"
// and the edge proxies through that tunnel to their LOCAL server.
//
// This was its own page at /routes while /nodes carried a SECOND, partial copy
// of the same mint flow: one product, minted in two places, with the deploy
// snippet on one page and the address form on the other. It is one tab now, and
// the whole sequence - create a link, deploy it, point an address at it - reads
// top to bottom in one place.
export default function RouteOnlyPanel({ enrollUrl, config, storeUrl, allowed, entitlementKnown, suspended, storeLinked }: {
    enrollUrl: string;
    config: WarpDeployConfig | null;
    storeUrl: string | null;
    /**
     * Resolved route-only entitlement. The old /routes page never checked this
     * at all, so someone without it got a fully working-looking form and only
     * found out at mint time.
     */
    allowed: boolean;
    entitlementKnown: boolean;
    suspended: boolean;
    /** null = not known yet; see NotIncluded for why that differs from false. */
    storeLinked?: boolean | null;
}) {
    const [kits, setKits] = useState<LinkKit[]>([]);
    const [used, setUsed] = useState(0);
    const [limit, setLimit] = useState(0);
    const [routes, setRoutes] = useState<LinkRoute[]>([]);
    const [loading, setLoading] = useState(true);

    // Mint-link flow
    const [linkName, setLinkName] = useState('');
    const [minting, setMinting] = useState(false);
    const [minted, setMinted] = useState<MintedLinkKit | null>(null);

    // Create-route flow
    const [linkId, setLinkId] = useState('');
    const [domainReq, setDomainReq] = useState<CreateRouteRequest>({ targetPort: 25565 });
    const [availability, setAvailability] = useState<DomainAvailability>('idle');
    const [targetHost, setTargetHost] = useState('127.0.0.1');
    const [targetPort, setTargetPort] = useState(25565);
    const [creating, setCreating] = useState(false);
    const [error, setError] = useState('');
    const [toast, setToast] = useState('');

    const load = useCallback(async () => {
        try {
            const [k, r] = await Promise.all([listLinkKits(), getLinkRoutes()]);
            const kitList = k?.kits ?? [];
            setKits(kitList);
            setUsed(k?.used ?? kitList.length);
            setLimit(k?.limit ?? 0);
            setRoutes(Array.isArray(r) ? r : []);
            // Default the route form to the first link if none chosen yet.
            setLinkId(prev => prev || (kitList[0]?.link_id ?? ''));
        } finally {
            setLoading(false);
        }
    }, []);

    // Only fetch once the caller is allowed to have any of this: an unentitled
    // tenant would otherwise fire two requests to render a refusal.
    useEffect(() => { if (allowed) load(); }, [allowed, load]);

    const flashToast = (msg: string) => {
        setToast(msg);
        setTimeout(() => setToast(''), 3000);
    };

    const mint = async () => {
        setMinting(true);
        try {
            const res = await mintLinkKit(linkName.trim());
            // Mirrors revoke's res.success check below: fetchAPI resolves (does not
            // throw) on an HTTP error with a JSON body, so an unchecked res here
            // would render the one-time-secret panel with WARP_API_KEY=undefined -
            // looking like success for a security-critical mint.
            if (!res.success) throw new Error((res as { message?: string }).message || 'Failed to create link');
            setMinted(res);
            setLinkName('');
            await load();
            setLinkId(res.link_id);
        } catch (e) {
            flashToast(e instanceof Error ? e.message : 'Failed to create link');
        } finally {
            setMinting(false);
        }
    };

    const revoke = async (linkIdToRevoke: string) => {
        if (!(await confirmDialog({ title: 'Revoke link', message: 'Revoke this link? Its tunnel drops and it can no longer connect.', confirmLabel: 'Revoke' }))) return;
        try {
            // fetchAPI resolves (does not throw) on an HTTP error whose body is JSON, so a
            // 404/500 arrives here as { success: false }. Check it, or a failed revoke of a
            // destructive, security-relevant action would falsely report "Link revoked".
            const res = await revokeLinkKit(linkIdToRevoke);
            if (!res.success) throw new Error((res as { message?: string }).message || 'Failed to revoke link');
            flashToast('Link revoked');
            await load();
        } catch (e) {
            flashToast(e instanceof Error ? e.message : 'Failed to revoke link');
        }
    };

    const submitRoute = async () => {
        setError('');
        if (!linkId) { setError('Create a link first, then select it'); return; }
        if (!targetHost.trim()) { setError('Enter the local address of your server'); return; }
        if (availability === 'taken') { setError('That domain is already taken'); return; }
        setCreating(true);
        try {
            const res = await createLinkRoute({ ...domainReq, linkId, targetPort, targetHost: targetHost.trim() });
            if (!res.success) throw new Error((res as { message?: string }).message || 'Failed to create route');
            // On a custom domain the route is accepted but provisional: a four-hour
            // clock is now running and missing it deletes the route. Core says so in
            // ownershipNotice, and a fixed "Route created" would hide it.
            const notice = (res as { ownershipNotice?: string }).ownershipNotice;
            flashToast(notice ? `Route created. ${notice}` : 'Route created');
            await load();
        } catch (e) {
            setError(e instanceof Error ? e.message : 'Failed to create route');
        } finally {
            setCreating(false);
        }
    };

    const removeRoute = async (domain: string) => {
        if (!(await confirmDialog({ title: 'Delete route', message: `Delete the route ${domain}?` }))) return;
        try {
            const res = await deleteLinkRoute(domain);
            if (!res.success) throw new Error((res as { message?: string }).message || 'Failed to delete route');
            await load();
        } catch (e) {
            flashToast(e instanceof Error ? e.message : 'Failed to delete route');
        }
    };

    // null means "not fetched yet". Rendering a refusal during that window tells
    // an entitled tenant they have nothing and then takes it back.
    if (!entitlementKnown) return <SkeletonCard height="h-24" />;
    if (!allowed) return <NotIncluded what="route only" storeUrl={storeUrl} suspended={suspended} storeLinked={storeLinked} />;

    return (
        <div className="space-y-6">
            <p className="text-sm text-(--base-07) max-w-2xl">
                <strong>You</strong> run the Minecraft server, on your own panel or none at all. Dylaris gives it
                a protected address and takes the attack traffic. Your machine makes outbound connections
                only — nothing is exposed.
            </p>

            {/* Links */}
            <div className="card p-5 space-y-4">
                <div className="flex items-center gap-2">
                    <Link2 size={16} className="text-(--accent-light)" />
                    <h2 className="font-medium text-(--base-09)">Your links</h2>
                </div>

                {minted && (
                    <div className="space-y-3">
                        <MintReveal kit={minted} onCopy={flashToast} onClose={() => setMinted(null)} />
                        {/* Right here, not on another page. The key is shown once
                            and the compose file is what it goes into; splitting
                            those across two pages is what made this confusing. */}
                        <DeployKit kind="route-only" warpKey={minted.warp_key} enrollUrl={enrollUrl} config={config} />
                    </div>
                )}

                {kits.length > 0 && (
                    <div className="space-y-2">
                        <p className="text-xs text-(--base-06) font-mono">
                            {limit > 0 ? `${used} of ${limit} links used` : `${used} link${used === 1 ? '' : 's'} used`}
                        </p>
                        {kits.map(k => (
                            <div key={k.id} className="flex items-center gap-3 px-3 py-2 rounded-md bg-(--base-01)">
                                <ShieldCheck size={14} className="text-(--success-light) shrink-0" />
                                <span className="text-sm text-(--base-09) truncate">{k.name}</span>
                                <span className="text-xs text-(--base-06) font-mono truncate ml-auto">{k.link_id}</span>
                                <button
                                    onClick={() => revoke(k.link_id)}
                                    title="Revoke link"
                                    className="p-1.5 text-(--base-06) hover:text-(--error-light) transition-colors shrink-0"
                                >
                                    <Trash2 size={14} />
                                </button>
                            </div>
                        ))}
                    </div>
                )}

                <div className="grid grid-cols-[1fr_auto] gap-3 max-w-md">
                    <div className="flex flex-col gap-1.5">
                        <label className="input-label">New link name</label>
                        <input
                            type="text"
                            value={linkName}
                            onChange={e => setLinkName(e.target.value)}
                            placeholder="Home PC"
                            disabled={suspended}
                            className="input-field text-sm w-full"
                        />
                    </div>
                    <div className="flex flex-col gap-1.5 justify-end">
                        <button onClick={mint} disabled={minting || suspended} className="btn btn-secondary inline-flex items-center gap-2 disabled:opacity-60">
                            {minting ? <Loader2 size={16} className="animate-spin" /> : <Plus size={16} />}
                            {minting ? 'Creating…' : 'Create link'}
                        </button>
                    </div>
                </div>
                <p className="text-xs text-(--base-06)">A link is the kit you run on your machine (warp + link). It opens an outbound tunnel — nothing is exposed.</p>

                {/* Everything except the secret is reproducible, and someone who
                    saved their key still needs the compose file the next time
                    they rebuild the machine. */}
                {kits.length > 0 && !minted && (
                    <details className="group">
                        <summary className="cursor-pointer text-xs text-(--base-06) hover:text-(--base-08) transition-colors select-none">
                            Show the deploy steps again
                        </summary>
                        <div className="pt-3">
                            <p className="text-xs text-(--base-06) mb-2">
                                The key itself cannot be shown again — only its hash is stored. Paste the one you
                                saved where the snippet says <code className="font-mono">&lt;your-warp-key&gt;</code>,
                                or revoke the link and create a new one.
                            </p>
                            <DeployKit kind="route-only" warpKey={null} enrollUrl={enrollUrl} config={config} />
                        </div>
                    </details>
                )}
            </div>

            {/* Create route */}
            <div className="card p-5 space-y-4">
                <h2 className="font-medium text-(--base-09)">New route</h2>

                <div className="flex flex-col gap-1.5">
                    <label className="input-label">Link</label>
                    <select
                        value={linkId}
                        onChange={e => setLinkId(e.target.value)}
                        disabled={kits.length === 0}
                        className="input-field text-sm w-full max-w-md disabled:opacity-60"
                    >
                        {kits.length === 0 && <option value="">Create a link first</option>}
                        {kits.map(k => <option key={k.id} value={k.link_id}>{k.name}</option>)}
                    </select>
                </div>

                <div className="flex flex-col gap-1.5">
                    <label className="input-label">Your domain</label>
                    <RouteDomainPicker value={domainReq} onChange={setDomainReq} onAvailabilityChange={setAvailability} />
                </div>

                <div className="grid grid-cols-[1fr_auto] gap-3 max-w-md">
                    <div className="flex flex-col gap-1.5">
                        <label className="input-label">Local server address</label>
                        <div className="flex items-center gap-2">
                            <Server size={14} className="text-(--base-06) shrink-0" />
                            <input
                                type="text"
                                value={targetHost}
                                onChange={e => setTargetHost(e.target.value)}
                                placeholder="127.0.0.1 or 192.168.1.50"
                                className="input-field text-sm w-full"
                            />
                        </div>
                    </div>
                    <div className="flex flex-col gap-1.5">
                        <label className="input-label">Port</label>
                        <input
                            type="number"
                            value={targetPort}
                            onChange={e => setTargetPort(Number(e.target.value))}
                            className="input-field text-sm w-24 text-center"
                        />
                    </div>
                </div>
                <p className="text-xs text-(--base-06)">This is the address your link dials on its own machine — loopback or LAN. Your link only dials addresses you allow it to (LINK_ALLOWED_TARGETS).</p>

                {error && <p className="text-sm text-(--error-light)">{error}</p>}

                <button onClick={submitRoute} disabled={creating || kits.length === 0 || suspended} className="btn btn-primary inline-flex items-center gap-2 w-fit disabled:opacity-60">
                    {creating ? <Loader2 size={16} className="animate-spin" /> : <Plus size={16} />}
                    {creating ? 'Creating…' : 'Create route'}
                </button>
            </div>

            {/* List routes */}
            <div className="space-y-2">
                <h2 className="font-medium text-(--base-09)">Your routes</h2>
                {loading ? (
                    <div className="card p-5 text-sm text-(--base-06)">Loading…</div>
                ) : routes.length === 0 ? (
                    <div className="card p-5 text-sm text-(--base-06)">No routes yet.</div>
                ) : (
                    routes.map(rt => (
                        <div key={rt.domain} className="card px-4 py-3 flex items-center justify-between gap-3">
                            <div className="min-w-0">
                                <div className="font-mono text-sm text-(--base-09) truncate">{rt.domain}</div>
                                <div className="text-xs text-(--base-06) font-mono">→ {rt.target_ip}:{rt.target_port}</div>
                            </div>
                            <button onClick={() => removeRoute(rt.domain)} title="Delete" className="p-2 text-(--base-06) hover:text-(--error-light) transition-colors shrink-0">
                                <Trash2 size={16} />
                            </button>
                        </div>
                    ))
                )}
            </div>

            {toast && (
                <div className="fixed bottom-6 left-1/2 -translate-x-1/2 px-4 py-2.5 rounded-md bg-(--success) text-white text-sm shadow-lg">{toast}</div>
            )}
        </div>
    );
}

// MintReveal shows the one-time secrets for a freshly minted link kit. The warp
// key is hashed server-side and can never be shown again, so we make copying it
// prominent and warn the user.
function MintReveal({ kit, onCopy, onClose }: { kit: MintedLinkKit; onCopy: (m: string) => void; onClose: () => void }) {
    // The link fetches everything else (its tunnel token + Redis credential) from
    // Core at boot using this warp key, so the only secret to paste is WARP_API_KEY.
    return (
        <div className="rounded-md border border-(--accent)/30 bg-(--accent)/5 p-4 space-y-3">
            <div className="flex items-center justify-between">
                <span className="text-sm font-medium text-(--base-09)">Link created - copy this now</span>
                <button onClick={onClose} className="text-xs text-(--base-06) hover:text-(--base-09)">Dismiss</button>
            </div>
            <p className="text-xs text-(--base-07) leading-relaxed">
                Shown once. It is already filled into the compose file below. The link fetches its tunnel token
                and Redis credential from Core on its own. The warp key cannot be retrieved later.
            </p>
            <CopyRow label="WARP_API_KEY" value={kit.warp_key} onCopy={onCopy} />
            <button
                onClick={() => { navigator.clipboard?.writeText(`WARP_API_KEY=${kit.warp_key}`); onCopy('Copied .env line'); }}
                className="btn btn-secondary inline-flex items-center gap-2 text-xs"
            >
                <Copy size={13} /> Copy .env line
            </button>
        </div>
    );
}

function CopyRow({ label, value, onCopy }: { label: string; value: string; onCopy: (m: string) => void }) {
    const [copied, setCopied] = useState(false);
    const copy = () => {
        navigator.clipboard?.writeText(value);
        setCopied(true);
        onCopy(`Copied ${label}`);
        setTimeout(() => setCopied(false), 1500);
    };
    return (
        <div className="flex items-center gap-2">
            <span className="input-label w-32 shrink-0">{label}</span>
            <code className="text-xs font-mono text-(--base-08) bg-(--base-01) px-2 py-1 rounded truncate flex-1">{value}</code>
            <button onClick={copy} title={`Copy ${label}`} className="p-1.5 text-(--base-06) hover:text-(--accent-light) transition-colors shrink-0">
                {copied ? <Check size={14} className="text-(--success-light)" /> : <Copy size={14} />}
            </button>
        </div>
    );
}
