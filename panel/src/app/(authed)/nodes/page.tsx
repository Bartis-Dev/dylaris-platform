"use client";

import { Suspense, useState, useEffect, useCallback } from 'react';
import { useRouter, useSearchParams } from 'next/navigation';
import {
    HardDrive, Globe, Server, Plus, Trash2, AlertTriangle, Clock,
    ExternalLink, ShoppingCart,
} from 'lucide-react';
import { useAppData } from '@/lib/AppDataContext';
import {
    getNodes,
    listNodeWarpKeys, mintNodeWarpKey, revokeNodeWarpKey, type NodeWarpKey,
} from '@/lib/api';
import { getStoreStatus } from '@/lib/api/store';
import { getMyUsage } from '@/lib/api/usage';
import { mintEnrollToken, listEnrollTokens, revokeEnrollToken } from '@/lib/api/nodeAdmission';
import type { NodeEnrollToken } from '@/lib/api/types';
import { nodeLabel } from '@/lib/nodeLabel';
import { nodeConnectivity, dotFor } from '@/lib/connectivity';
import { nodeIdFromLabel } from '@/lib/warpDeploy';
import { isLocationName } from '@/lib/validation';
import { getWarpDeployConfig, type WarpDeployConfig } from '@/lib/api/warpDeployConfig';
import { SkeletonCard } from '@/components/Skeleton';
import { resolveInfraTab, showInfraTabBar, type InfraTab } from '@/lib/infraTab';
import { DeployKit, NotIncluded, SecretField, usageLabel } from '@/components/infra/DeployKit';
import RouteOnlyPanel from '@/components/infra/RouteOnlyPanel';
import ExternalNodesPanel from '@/components/infra/ExternalNodesPanel';

// ---------------------------------------------------------------------------
// "My infrastructure" - hardware that is not in the cluster, in three tabs.
//
//   External nodes      - the OPERATOR's own machines outside the swarm.
//                         Admin only, and their default.
//   Bring your own node - Dylaris runs Minecraft servers ON a customer's
//                         machine.
//   Protected addresses - the customer runs the server; Dylaris gives it an
//                         address and absorbs the attack traffic.
//
// The first two are different sets of machines with different owners, not two
// views of one list, which is why they are separate tabs rather than a filter.
// Before they were split, an admin opening "my machines" got the entire fleet -
// every swarm host included - because the node list was only ever scoped for
// non-admins. Core now scopes it with ?scope=, so the split is enforced where it
// matters and not only where it is drawn.
//
// The last two were two PAGES until the split stopped following a seam in the
// product: route-only was minted here AND on /routes, with the deploy snippet on
// one and the address form on the other.
//
// Both customer-facing halves are per-LOCATION and can be held several times
// over: a box at home and one in a datacenter needs one of each. So neither is a
// yes/no - each shows how many are in use against the plan's cap, and the create
// control disables on the cap rather than on the entitlement alone.
//
// The store link is ALWAYS present, entitled or not: someone who already has
// BYON is exactly the person who buys a second one.
// ---------------------------------------------------------------------------

interface OwnNode {
    id: number;
    name: string;
    displayName?: string;
    status: string;
    lastSeenAt?: string;
    serverCount?: number;
    region?: string;
}

const NAME_RULE = '4 to 20 characters: letters, digits and hyphens, not starting or ending with a hyphen.';

function MyNodesInner() {
    const { featureFlags, entitlement, user, gatewayEnabled } = useAppData();
    const router = useRouter();
    const searchParams = useSearchParams();

    const isAdmin = user?.isAdmin ?? false;

    // What this reader HAS - separate from whether this ACCOUNT is entitled to
    // it, which the panels below answer for themselves.
    const have = { external: isAdmin, machines: featureFlags.byon, routes: gatewayEnabled };

    // The tab lives in the URL so Create can deep-link straight into the half it
    // means, and so a reload or a shared link lands where it left off.
    const requestedTab = searchParams.get('tab');
    const tab = resolveInfraTab(requestedTab, have);
    const selectTab = useCallback((next: InfraTab) => {
        router.replace(`/nodes?tab=${next}`, { scroll: false });
    }, [router]);

    // A tab that was asked for and not granted - a tenant typing ?tab=external -
    // gets the URL corrected too, not just different content underneath it. An
    // address bar that still says "external" while showing something else is the
    // kind of thing someone reports as a bug in the wrong place.
    useEffect(() => {
        if (!tab || !requestedTab || requestedTab === tab) return;
        router.replace(`/nodes?tab=${tab}`, { scroll: false });
    }, [tab, requestedTab, router]);

    const [nodes, setNodes] = useState<OwnNode[]>([]);
    // Its own fetch, so its own loading flag and its own read time. Sharing the
    // BYON list's would have this panel claim "no external machine" the moment
    // the OTHER request came back.
    const [external, setExternal] = useState<{ nodes: OwnNode[]; loading: boolean; readAt: number }>(
        () => ({ nodes: [], loading: true, readAt: Date.now() })
    );
    const [tokens, setTokens] = useState<NodeEnrollToken[]>([]);
    // The node cap is a LIMIT, not an entitlement: entitlement answers "may
    // they", limits answer "how many". They live in different endpoints because
    // they are set in different places (plan kind vs plan caps).
    const [nodeLimit, setNodeLimit] = useState<number | undefined>(undefined);
    const [loading, setLoading] = useState(true);
    const [error, setError] = useState('');
    // When the node list was last read. Connectivity is "how long since the last
    // heartbeat", so it needs a clock - but reading one during render makes the
    // rendered output depend on when React happened to re-render. Stamped at
    // load time instead, which is also the only moment the answer can change.
    const [nodesReadAt, setNodesReadAt] = useState(() => Date.now());

    const [nodeLabelDraft, setNodeLabelDraft] = useState('');
    const [minting, setMinting] = useState(false);
    // A BYON machine needs BOTH secrets: a warp key to reach the overlay and an
    // enroll token to become a node. They are minted together here so the deploy
    // snippet is complete - handing over one and a placeholder for the other was
    // the gap this closes.
    const [revealedNode, setRevealedNode] = useState<{ token: string; warpKey: string; label: string; grpcTlsFingerprint?: string } | null>(null);
    const [nodeKeys, setNodeKeys] = useState<NodeWarpKey[]>([]);
    const [nodeUsage, setNodeUsage] = useState<{ used: number; limit?: number } | null>(null);

    // Overlay addresses for the deploy snippets. Resolved by Core, which is on
    // that network; there is nowhere else a customer could look them up.
    const [deployConfig, setDeployConfig] = useState<WarpDeployConfig | null>(null);

    // The real storefront origin, not a hardcoded dylaris.com: a self-host
    // install with the store wired points somewhere else, and a link that goes to
    // the wrong shop is worse than no link.
    const [storeUrl, setStoreUrl] = useState<string | null>(null);
    // Core's own public origin, used in the deploy snippets as ENROLL_URL. The
    // browser is talking to it right now, so it is the one address known to be
    // reachable from outside - guessing it would be worse.
    const [enrollUrl, setEnrollUrl] = useState('<core-url>');

    const suspended = entitlement?.source === 'suspended';
    // Admins bypass the entitlement gate: their own account may hold no plan at
    // all, and locking the operator out of their own infrastructure page is not a
    // billing decision anyone made.
    const byonAllowed = isAdmin || (entitlement?.byon ?? false);
    const routeOnlyAllowed = isAdmin || (entitlement?.routeOnly ?? false);
    // null means "not fetched yet". Rendering a refusal during that window tells
    // an entitled tenant they have nothing and then takes it back.
    const entitlementKnown = entitlement !== null || isAdmin;

    const load = useCallback(async () => {
        const [n, t] = await Promise.all([getNodes('byon'), listEnrollTokens()]);
        if (n.success && Array.isArray(n.nodes)) setNodes(n.nodes as OwnNode[]);
        if (t.success && Array.isArray(t.tokens)) setTokens(t.tokens);
        setNodesReadAt(Date.now());
        setLoading(false);
    }, []);

    useEffect(() => { load(); }, [load]);

    // Only an admin may ask for this scope; Core answers 403 to anyone else, so
    // there is no point spending the request.
    useEffect(() => {
        if (!isAdmin) {
            setExternal({ nodes: [], loading: false, readAt: Date.now() });
            return;
        }
        let cancelled = false;
        getNodes('external').then(res => {
            if (cancelled) return;
            const list = res.success && Array.isArray(res.nodes) ? (res.nodes as OwnNode[]) : [];
            setExternal({ nodes: list, loading: false, readAt: Date.now() });
        }).catch(() => {
            if (!cancelled) setExternal({ nodes: [], loading: false, readAt: Date.now() });
        });
        return () => { cancelled = true; };
    }, [isAdmin]);

    useEffect(() => {
        if (typeof window !== 'undefined') {
            setEnrollUrl(window.location.origin);
        }
    }, []);

    useEffect(() => {
        let cancelled = false;
        getWarpDeployConfig().then(res => {
            if (!cancelled && res.success && res.config) setDeployConfig(res.config);
        }).catch(() => { /* the snippet falls back to its placeholders */ });
        return () => { cancelled = true; };
    }, []);

    useEffect(() => {
        let cancelled = false;
        getMyUsage().then(res => {
            if (!cancelled && res.success && res.usage?.limits) setNodeLimit(res.usage.limits.maxNodes);
        }).catch(() => { /* the cap is a hint here; the backend enforces it anyway */ });
        return () => { cancelled = true; };
    }, []);

    useEffect(() => {
        if (!featureFlags.store) return;
        let cancelled = false;
        getStoreStatus().then(res => {
            if (!cancelled && res.success && res.storeUrl) setStoreUrl(res.storeUrl);
        });
        return () => { cancelled = true; };
    }, [featureFlags.store]);

    const loadNodeKeys = useCallback(async () => {
        if (!gatewayEnabled) return;
        const res = await listNodeWarpKeys();
        if (res.success) {
            setNodeKeys(res.keys || []);
            setNodeUsage({ used: res.used ?? 0, limit: res.limit });
        }
    }, [gatewayEnabled]);

    // Required and to a fixed shape. It used to default to "my machine" for
    // everyone who left it blank, which made a list of machines unreadable the
    // moment there was more than one - and the name is what the snippet slugs
    // into NODE_ID, where a leading hyphen reads as a flag. Core rejects the same
    // shapes; this is the version that says so before the round trip.
    const draft = nodeLabelDraft.trim();
    const nameValid = isLocationName(draft);
    const nameError = draft !== '' && !nameValid;

    const handleMintNodeKey = async () => {
        if (!nameValid) {
            setError(`Name this location first — ${NAME_RULE}`);
            return;
        }
        setMinting(true);
        setError('');
        // Warp key first: it is the one with a cap, so a refusal happens before an
        // enroll token is created that nobody could use.
        const warp = await mintNodeWarpKey(draft);
        if (!warp.success || !warp.warp_key) {
            setMinting(false);
            setError(warp.message || 'Could not create the overlay key.');
            return;
        }
        const res = await mintEnrollToken({ label: draft, expiresDays: 7 });
        setMinting(false);
        if (!res.success || !res.token) {
            setError(res.message || 'The overlay key was created but the enrollment key failed. Revoke the key below and try again.');
            loadNodeKeys();
            return;
        }
        // Core returns the fingerprint only while its gRPC channel is TLS, so it
        // travels with the enroll token and lands in the snippet unchanged.
        setRevealedNode({ token: res.token, warpKey: warp.warp_key, label: draft, grpcTlsFingerprint: res.grpcTlsFingerprint });
        setNodeLabelDraft('');
        load();
        loadNodeKeys();
    };

    useEffect(() => { loadNodeKeys(); }, [loadNodeKeys]);

    const handleRevokeNodeKey = async (nodeId: string) => {
        const res = await revokeNodeWarpKey(nodeId);
        if (!res.success) {
            setError('Could not revoke that key.');
            return;
        }
        loadNodeKeys();
    };

    const handleRevokeToken = async (id: string) => {
        const res = await revokeEnrollToken(id);
        if (!res.success) {
            setError(res.message || 'Could not revoke that key.');
            return;
        }
        load();
    };

    // Only when NOTHING is available. Returning on BYON alone stranded route-only
    // customers on a platform with BYON off: the top bar offers this page
    // whenever any part exists, and they would land on "your own hardware is
    // turned off" with the routes tab unreachable behind it.
    if (tab === null) {
        return (
            <div className="p-6 max-w-2xl">
                <h1 className="text-lg font-display font-bold text-(--base-09) mb-2">My infrastructure</h1>
                <p className="text-sm text-(--base-07)">
                    Running Minecraft on your own hardware, and pointing protected addresses at your own
                    server, are both turned off on this platform.
                </p>
            </div>
        );
    }

    // Both from /warp/node-keys when it answered: it counts connected nodes AND
    // unredeemed keys, which is exactly what the mint endpoint enforces. Showing
    // a different number would make the refusal look arbitrary the moment it hits.
    const nodesUsed = nodeUsage?.used ?? nodes.length;
    const effectiveNodeLimit = nodeUsage?.limit ?? nodeLimit;
    const nodesAtCap = typeof effectiveNodeLimit === 'number' && effectiveNodeLimit > 0 && nodesUsed >= effectiveNodeLimit;

    const TABS: { id: InfraTab; label: string; icon: typeof HardDrive }[] = [
        { id: 'external', label: 'External nodes', icon: Server },
        { id: 'machines', label: 'Bring your own node', icon: HardDrive },
        { id: 'routes', label: 'Protected addresses', icon: Globe },
    ].filter(t => have[t.id as keyof typeof have]) as { id: InfraTab; label: string; icon: typeof HardDrive }[];

    return (
        <div className="p-6 max-w-6xl space-y-6 overflow-y-auto">
            <div className="flex flex-wrap items-start justify-between gap-3">
                <div className="min-w-0">
                    <h1 className="text-lg font-display font-bold text-(--base-09) mb-1">My infrastructure</h1>
                    <p className="text-sm text-(--base-07) max-w-2xl">
                        Hardware that is not in the cluster. Everything here connects outwards through an
                        encrypted tunnel, so nothing needs a public IP or port forwarding — and you can
                        hold several of each, one per location.
                    </p>
                </div>
                {/* Always offered, entitled or not: the person most likely to buy a
                    second location is the one who already has the first. */}
                {featureFlags.store && storeUrl && (
                    <a href={storeUrl} target="_blank" rel="noopener noreferrer" className="btn btn-secondary btn-sm shrink-0">
                        <ShoppingCart size={13} /> Store <ExternalLink size={11} />
                    </a>
                )}
            </div>

            {/* A bar with one tab is decoration. It appears only where the reader
                actually has somewhere else to go. */}
            {showInfraTabBar(have) && (
                <div className="flex gap-1 border-b border-(--base-03)">
                    {TABS.map(t => {
                        const Icon = t.icon;
                        return (
                            <button
                                key={t.id}
                                type="button"
                                onClick={() => selectTab(t.id)}
                                aria-current={tab === t.id ? 'page' : undefined}
                                className={`flex items-center gap-2 px-4 py-2 text-sm font-medium border-b-2 -mb-px transition-colors ${
                                    tab === t.id
                                        ? 'border-(--accent) text-(--accent-light)'
                                        : 'border-transparent text-(--base-06) hover:text-(--base-08)'
                                }`}
                            >
                                <Icon size={14} />
                                {t.label}
                            </button>
                        );
                    })}
                </div>
            )}

            {error && (
                <div className="alert alert-error">
                    <AlertTriangle size={14} className="shrink-0 mt-0.5" />
                    <span>{error}</span>
                </div>
            )}

            {suspended && (
                <div className="alert alert-warning">
                    <AlertTriangle size={14} className="shrink-0 mt-0.5" />
                    <span>Your account is suspended. Everything below is paused until it is reactivated.</span>
                </div>
            )}

            {tab === 'external' ? (
                <ExternalNodesPanel nodes={external.nodes} loading={external.loading} readAt={external.readAt} />
            ) : tab === 'routes' ? (
                <RouteOnlyPanel
                    enrollUrl={enrollUrl}
                    config={deployConfig}
                    storeUrl={storeUrl}
                    allowed={routeOnlyAllowed}
                    entitlementKnown={entitlementKnown}
                    suspended={suspended}
                />
            ) : (
            /* The keys move into a column of their own once there are any, rather
               than pushing the form and the machine list down the page. There is
               room to the side, and the compose snippet is the widest thing here. */
            <div className={revealedNode ? 'grid gap-6 lg:grid-cols-2 items-start' : ''}>
            <section className="card p-5 space-y-4">
                <div className="flex items-start justify-between gap-3">
                    <div className="min-w-0">
                        <h2 className="text-sm font-display font-semibold text-(--accent-light) flex items-center gap-2">
                            <HardDrive size={15} /> Bring your own node
                        </h2>
                        <p className="text-sm text-(--base-07) mt-1 max-w-2xl">
                            Dylaris runs Minecraft servers <strong>on your machine</strong>. You keep the
                            hardware and the disk; the panel, backups and player routing stay ours.
                        </p>
                    </div>
                    <span className="mono-label shrink-0 pt-0.5">{usageLabel(nodesUsed, effectiveNodeLimit)}</span>
                </div>

                {!entitlementKnown ? (
                    <SkeletonCard height="h-16" />
                ) : !byonAllowed ? (
                    <NotIncluded what="bring your own node" storeUrl={storeUrl} suspended={suspended} />
                ) : (
                    <>
                        <div className="flex flex-col sm:flex-row gap-2 sm:items-start">
                            <div className="flex-1 flex flex-col gap-[5px]">
                                <label htmlFor="location-name" className="input-label">Name this location</label>
                                <input
                                    id="location-name"
                                    className="input-field w-full"
                                    value={nodeLabelDraft}
                                    onChange={e => setNodeLabelDraft(e.target.value)}
                                    placeholder="home-desktop"
                                    maxLength={20}
                                    aria-invalid={nameError || undefined}
                                    aria-describedby="location-name-rule"
                                    disabled={nodesAtCap || suspended}
                                />
                                <p
                                    id="location-name-rule"
                                    className={`text-xs ${nameError ? 'text-(--error-light)' : 'text-(--base-06)'}`}
                                >
                                    {NAME_RULE}
                                </p>
                            </div>
                            <button
                                onClick={handleMintNodeKey}
                                disabled={minting || !nameValid || nodesAtCap || suspended}
                                className="btn btn-primary disabled:opacity-40 disabled:cursor-not-allowed sm:mt-[22px]"
                                title={nodesAtCap ? 'You have used every node your plan includes' : undefined}
                            >
                                <Plus size={14} /> {minting ? 'Creating…' : 'Add a machine'}
                            </button>
                        </div>

                        {nodesAtCap && (
                            <p className="flex items-start gap-1.5 text-xs text-(--warning-light)">
                                <AlertTriangle size={12} className="mt-0.5 shrink-0" />
                                <span>
                                    Every node your plan includes is in use. Add another in the store to
                                    connect a second location.
                                </span>
                            </p>
                        )}

                        {nodeKeys.length > 0 && (
                            <div className="border-t border-(--base-03) pt-3 space-y-2">
                                <div className="mono-label">Overlay keys in use</div>
                                <p className="text-xs text-(--base-06)">
                                    Each counts towards your plan whether or not the machine has connected
                                    yet. Revoke one you never used to free the slot.
                                </p>
                                {nodeKeys.map(k => (
                                    <div key={k.id} className="flex items-center justify-between gap-3 text-sm">
                                        <div className="min-w-0">
                                            <div className="text-(--base-08) truncate">{k.name}</div>
                                            <div className="mono-label truncate">{k.node_id}</div>
                                        </div>
                                        <button
                                            onClick={() => handleRevokeNodeKey(k.node_id)}
                                            className="text-(--base-06) hover:text-(--error-light) p-1.5 rounded-md transition-colors"
                                            title="Revoke this overlay key"
                                        >
                                            <Trash2 size={14} />
                                        </button>
                                    </div>
                                ))}
                            </div>
                        )}

                        {tokens.length > 0 && (
                            <div className="border-t border-(--base-03) pt-3 space-y-2">
                                <div className="mono-label">Keys waiting to be used</div>
                                {tokens.map(t => (
                                    <div key={t.id} className="flex items-center justify-between gap-3 text-sm">
                                        <div className="min-w-0">
                                            <div className="text-(--base-08) truncate">{t.label || '(unnamed)'}</div>
                                            {t.expiresAt && (
                                                <div className="text-xs text-(--base-06) flex items-center gap-1">
                                                    <Clock size={10} /> expires {new Date(t.expiresAt).toLocaleDateString()}
                                                </div>
                                            )}
                                        </div>
                                        <button
                                            onClick={() => handleRevokeToken(t.id)}
                                            className="text-(--base-06) hover:text-(--error-light) p-1.5 rounded-md transition-colors"
                                            title="Revoke this key"
                                        >
                                            <Trash2 size={14} />
                                        </button>
                                    </div>
                                ))}
                            </div>
                        )}

                        <div className="border-t border-(--base-03) pt-3 space-y-2">
                            <div className="mono-label">Your machines</div>
                            {loading ? (
                                <SkeletonCard height="h-16" />
                            ) : nodes.length === 0 ? (
                                <p className="text-sm text-(--base-06)">
                                    No machine connected yet. Create a key above and follow the deploy steps.
                                </p>
                            ) : (
                                nodes.map(n => {
                                    const { tier } = nodeConnectivity(n.status, n.lastSeenAt, nodesReadAt);
                                    return (
                                        <div key={n.id} className="flex items-center justify-between gap-3 rounded-md bg-(--base-02) border border-(--base-03) px-3 py-2.5">
                                            <div className="flex items-center gap-2.5 min-w-0">
                                                <div className={`w-2 h-2 rounded-full shrink-0 ${dotFor(tier, 'bg-(--success-light)')}`} />
                                                <div className="min-w-0">
                                                    <div className="text-sm text-(--base-09) truncate">{nodeLabel(n)}</div>
                                                    <div className="mono-label">
                                                        {n.status}
                                                        {typeof n.serverCount === 'number' && ` · ${n.serverCount} server${n.serverCount === 1 ? '' : 's'}`}
                                                    </div>
                                                </div>
                                            </div>
                                        </div>
                                    );
                                })
                            )}
                        </div>
                    </>
                )}
            </section>

            {/* Shown once. The keys are stored as hashes, so this is the only
                moment they exist anywhere the reader can see them - the copy has
                to say so before they close it. */}
            {revealedNode && (
                <aside className="card p-5 space-y-3 border-(--accent-border) bg-(--accent-ghost) lg:sticky lg:top-6">
                    <div className="text-sm font-medium text-(--base-09)">
                        {revealedNode.label} — two keys, shown once.
                    </div>
                    <p className="text-xs text-(--base-07)">
                        Both are already filled into the compose file below, so the normal path never
                        needs them by hand. They are blurred because this page is one people have open
                        while sharing a screen; click either to read it.
                    </p>
                    <SecretField label="Overlay key (warp API_KEY)" value={revealedNode.warpKey} />
                    <SecretField
                        label="Enrollment key (NODE_ENROLL_TOKEN)"
                        value={revealedNode.token}
                        note="It expires in 7 days and can be used once."
                    />
                    <DeployKit
                        kind="node"
                        warpKey={revealedNode.warpKey}
                        enrollUrl={enrollUrl}
                        nodeEnrollToken={revealedNode.token}
                        grpcTlsFingerprint={revealedNode.grpcTlsFingerprint}
                        nodeId={nodeIdFromLabel(revealedNode.label)}
                        config={deployConfig}
                    />
                    <button type="button" onClick={() => setRevealedNode(null)} className="btn btn-secondary btn-sm">
                        I saved them
                    </button>
                </aside>
            )}
            </div>
            )}
        </div>
    );
}

// useSearchParams needs a Suspense boundary, the same as every other page here
// that reads one. /nodes builds dynamic today so it slipped through, but the
// boundary is what keeps that true: without it, a later change that makes the
// route statically analyzable fails the build instead of the page.
export default function MyNodesPage() {
    return (
        <Suspense fallback={
            <div className="p-6 max-w-6xl space-y-6">
                <SkeletonCard height="h-10" />
                <SkeletonCard height="h-64" />
            </div>
        }>
            <MyNodesInner />
        </Suspense>
    );
}
