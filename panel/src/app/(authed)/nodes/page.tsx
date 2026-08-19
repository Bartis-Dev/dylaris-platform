"use client";

import { Suspense, useState, useEffect, useCallback } from 'react';
import { useRouter, useSearchParams } from 'next/navigation';
import {
    HardDrive, Globe, Plus, Trash2, AlertTriangle, Clock,
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
import { getWarpDeployAddrs, type WarpDeployAddrs } from '@/lib/api/warpDeployConfig';
import { SkeletonCard } from '@/components/Skeleton';
import { DeployKit, NotIncluded, CopyButton, usageLabel } from '@/components/infra/DeployKit';
import RouteOnlyPanel from '@/components/infra/RouteOnlyPanel';
import AddNodeModal from '@/components/AddNodeModal';

// ---------------------------------------------------------------------------
// "My infrastructure" - the TENANT side of bring-your-own-node and route-only.
//
// Two DIFFERENT products, one per tab:
//
//   Bring your own node - Dylaris runs Minecraft servers ON your machine.
//   Route only          - you run the server; Dylaris gives it a protected
//                         address and absorbs the attack traffic.
//
// They were two PAGES until the split stopped following a seam in the product:
// route-only was minted here AND on /routes, with the deploy snippet on one and
// the address form on the other. The route-only half lives in RouteOnlyPanel
// now and owns that whole sequence; this file keeps the machines.
//
// Both are per-LOCATION and both can be held several times over: a customer with
// a box at home and one in a datacenter needs one of each. So neither tab is a
// yes/no - each shows how many are in use against the plan's cap, and the create
// control disables on the cap rather than on the entitlement alone.
//
// The store link is ALWAYS present, entitled or not: someone who already has
// BYON is exactly the person who buys a second one.
//
// Everything here uses already tenant-scoped endpoints (/api/nodes,
// /api/nodes/enroll-token, /api/warp/link-kits), so no admin route is widened.
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

type InfraTab = 'machines' | 'routes';

function MyNodesInner() {
    const { featureFlags, entitlement, user, gatewayEnabled } = useAppData();
    const router = useRouter();
    const searchParams = useSearchParams();

    // The tab lives in the URL so Create can deep-link straight into the half it
    // means, and so a reload or a shared link lands where it left off. It used
    // to be two separate pages with a bar that navigated between them, which is
    // what made one product feel like two.
    // Resolved against gatewayEnabled, not taken from the URL as given. Hiding
    // the tab bar is not enough: a bookmark or an old link to ?tab=routes would
    // still open a panel whose two endpoints 409 without gateway routing, so
    // the reader gets failures instead of the product simply not being there.
    const tab: InfraTab =
        gatewayEnabled && searchParams.get('tab') === 'routes' ? 'routes' : 'machines';
    const selectTab = useCallback((next: InfraTab) => {
        router.replace(next === 'routes' ? '/nodes?tab=routes' : '/nodes', { scroll: false });
    }, [router]);

    const [showAddFleetNode, setShowAddFleetNode] = useState(false);

    const [nodes, setNodes] = useState<OwnNode[]>([]);
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
    const [revealedNode, setRevealedNode] = useState<{ token: string; warpKey: string; label: string } | null>(null);
    const [nodeKeys, setNodeKeys] = useState<NodeWarpKey[]>([]);
    const [nodeUsage, setNodeUsage] = useState<{ used: number; limit?: number } | null>(null);


    // Overlay addresses for the deploy snippets. Resolved by Core, which is on
    // that network; there is nowhere else a customer could look them up.
    const [deployAddrs, setDeployAddrs] = useState<WarpDeployAddrs | null>(null);

    // The real storefront origin, not a hardcoded dylaris.com: a self-host
    // install with the store wired points somewhere else, and a link that goes to
    // the wrong shop is worse than no link.
    const [storeUrl, setStoreUrl] = useState<string | null>(null);
    // Core's own public origin, used in the deploy snippets as ENROLL_URL. The
    // browser is talking to it right now, so it is the one address known to be
    // reachable from outside - guessing it would be worse.
    const [enrollUrl, setEnrollUrl] = useState('<core-url>');

    const isAdmin = user?.isAdmin ?? false;
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
        const [n, t] = await Promise.all([getNodes(), listEnrollTokens()]);
        if (n.success && Array.isArray(n.nodes)) setNodes(n.nodes as OwnNode[]);
        if (t.success && Array.isArray(t.tokens)) setTokens(t.tokens);
        setNodesReadAt(Date.now());
        setLoading(false);
    }, []);

    useEffect(() => { load(); }, [load]);

    useEffect(() => {
        if (typeof window !== 'undefined') {
            setEnrollUrl(window.location.origin);
        }
    }, []);

    useEffect(() => {
        let cancelled = false;
        getWarpDeployAddrs().then(res => {
            if (!cancelled && res.success && res.addrs) setDeployAddrs(res.addrs);
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

    const handleMintNodeKey = async () => {
        setMinting(true);
        setError('');
        const label = nodeLabelDraft.trim() || 'my machine';
        // Warp key first: it is the one with a cap, so a refusal happens before an
        // enroll token is created that nobody could use.
        const warp = await mintNodeWarpKey(label);
        if (!warp.success || !warp.warp_key) {
            setMinting(false);
            setError(warp.message || 'Could not create the overlay key.');
            return;
        }
        const res = await mintEnrollToken({ label, expiresDays: 7 });
        setMinting(false);
        if (!res.success || !res.token) {
            setError(res.message || 'The overlay key was created but the enrollment key failed. Revoke the key below and try again.');
            loadNodeKeys();
            return;
        }
        setRevealedNode({ token: res.token, warpKey: warp.warp_key, label });
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

    if (!featureFlags.byon) {
        return (
            <div className="p-6 max-w-2xl">
                <h1 className="text-lg font-display font-bold text-(--base-09) mb-2">My machines</h1>
                <p className="text-sm text-(--base-07)">
                    Running Minecraft on your own hardware is turned off on this platform.
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
        { id: 'machines', label: 'My machines', icon: HardDrive },
        { id: 'routes', label: 'Protected addresses', icon: Globe },
    ];

    return (
        <div className="p-6 max-w-4xl space-y-6 overflow-y-auto">
            <div className="flex flex-wrap items-start justify-between gap-3">
                <div className="min-w-0">
                    <h1 className="text-lg font-display font-bold text-(--base-09) mb-1">My infrastructure</h1>
                    <p className="text-sm text-(--base-07) max-w-2xl">
                        Two ways to use hardware you already own. Both connect outwards through an
                        encrypted tunnel, so neither needs a public IP or port forwarding — and you can
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

            {/* With gateway routing off there is no route-only product at all, so
                a second tab would lead to a panel that can only refuse. */}
            {gatewayEnabled && (
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

            {tab === 'routes' ? (
                <RouteOnlyPanel
                    enrollUrl={enrollUrl}
                    addrs={deployAddrs}
                    storeUrl={storeUrl}
                    allowed={routeOnlyAllowed}
                    entitlementKnown={entitlementKnown}
                    suspended={suspended}
                />
            ) : (
            <>
            {/* ── Bring your own node ─────────────────────────────────────── */}
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
                        <div className="flex flex-col sm:flex-row gap-2 sm:items-end">
                            <div className="flex-1 flex flex-col gap-[5px]">
                                <label className="input-label">Name this location</label>
                                <input
                                    className="input-field w-full"
                                    value={nodeLabelDraft}
                                    onChange={e => setNodeLabelDraft(e.target.value)}
                                    placeholder="home-desktop"
                                    disabled={nodesAtCap || suspended}
                                />
                            </div>
                            <button
                                onClick={handleMintNodeKey}
                                disabled={minting || nodesAtCap || suspended}
                                className="btn btn-primary disabled:opacity-40 disabled:cursor-not-allowed"
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

                        {/* Shown once. The key is not retrievable afterwards, so the
                            copy has to say so before the user closes it. */}
                        {revealedNode && (
                            <div className="rounded-md border border-(--accent-border) bg-(--accent-ghost) p-4 space-y-3">
                                <div className="text-sm font-medium text-(--base-09)">
                                    Your machine&apos;s two keys. Both are shown once.
                                </div>
                                <div className="space-y-1">
                                    <label className="mono-label">Overlay key (warp API_KEY)</label>
                                    <div className="flex items-center gap-2">
                                        <code className="input-mono flex-1 min-w-0 break-all bg-(--base-02) border border-(--base-03) rounded-md px-3 py-2 text-xs text-(--base-08) select-all">
                                            {revealedNode.warpKey}
                                        </code>
                                        <CopyButton value={revealedNode.warpKey} />
                                    </div>
                                </div>
                                <div className="space-y-1">
                                    <label className="mono-label">Enrollment key (NODE_ENROLL_TOKEN)</label>
                                    <div className="flex items-center gap-2">
                                        <code className="input-mono flex-1 min-w-0 break-all bg-(--base-02) border border-(--base-03) rounded-md px-3 py-2 text-xs text-(--base-08) select-all">
                                            {revealedNode.token}
                                        </code>
                                        <CopyButton value={revealedNode.token} />
                                    </div>
                                    <p className="text-xs text-(--base-07)">
                                        It expires in 7 days and can be used once.
                                    </p>
                                </div>
                                {/* Both are already filled into the snippet below,
                                    so the usual copy-paste needs neither of the
                                    fields above - they are there for the record. */}
                                <DeployKit
                                    kind="node"
                                    warpKey={revealedNode.warpKey}
                                    enrollUrl={enrollUrl}
                                    nodeEnrollToken={revealedNode.token}
                                    nodeId={nodeIdFromLabel(revealedNode.label)}
                                    addrs={deployAddrs}
                                />
                                <button type="button" onClick={() => setRevealedNode(null)} className="btn btn-secondary btn-sm">
                                    I saved them
                                </button>
                            </div>
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

            {/* Joining a machine to the FLEET is an operator action and a
                different flow from a tenant enrolling theirs. It used to hang
                off the sidebar's Create menu, which is why one entry opened a
                modal and its neighbour opened a page. */}
            {isAdmin && (
                <div className="flex justify-end">
                    <button type="button" onClick={() => setShowAddFleetNode(true)} className="btn btn-secondary btn-sm">
                        <Plus size={13} /> Add a fleet node
                    </button>
                </div>
            )}
            </>
            )}

            {showAddFleetNode && <AddNodeModal onClose={() => setShowAddFleetNode(false)} />}
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
            <div className="p-6 max-w-4xl space-y-6">
                <SkeletonCard height="h-10" />
                <SkeletonCard height="h-64" />
            </div>
        }>
            <MyNodesInner />
        </Suspense>
    );
}
