"use client";

import { useState, useEffect, useCallback } from 'react';
import {
    HardDrive, Globe, Plus, Copy, Check, Trash2, AlertTriangle, Clock,
    ExternalLink, Terminal, ShoppingCart, Lock,
} from 'lucide-react';
import { useAppData } from '@/lib/AppDataContext';
import { getNodes, listLinkKits, mintLinkKit, revokeLinkKit, type LinkKit } from '@/lib/api';
import { getStoreStatus } from '@/lib/api/store';
import { getMyUsage } from '@/lib/api/usage';
import { mintEnrollToken, listEnrollTokens, revokeEnrollToken } from '@/lib/api/nodeAdmission';
import type { NodeEnrollToken } from '@/lib/api/types';
import { nodeLabel } from '@/lib/nodeLabel';
import { nodeConnectivity, dotFor } from '@/lib/connectivity';
import { nodeCompose, routeOnlyCompose, deployCli } from '@/lib/warpDeploy';
import { SkeletonCard } from '@/components/Skeleton';

// ---------------------------------------------------------------------------
// "My machines" - the TENANT side of bring-your-own-node and route-only.
//
// These are two DIFFERENT products and the page now says so structurally, one
// card each, rather than mixing an entitlement summary with a single enrollment
// form and leaving the reader to work out which half applied to them:
//
//   Bring your own node - Dylaris runs Minecraft servers ON your machine.
//   Route only          - you run the server; Dylaris gives it a protected
//                         address and absorbs the attack traffic.
//
// Both are per-LOCATION and both can be held several times over: a customer with
// a box at home and one in a datacenter needs one of each. So neither card is a
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

function CopyButton({ value, label, className }: { value: string; label?: string; className?: string }) {
    const [copied, setCopied] = useState(false);
    return (
        <button
            type="button"
            onClick={async () => {
                try {
                    await navigator.clipboard.writeText(value);
                    setCopied(true);
                    setTimeout(() => setCopied(false), 1800);
                } catch { /* clipboard blocked (insecure context); the text is selectable anyway */ }
            }}
            className={className || 'btn btn-secondary btn-sm shrink-0'}
        >
            {copied ? <><Check size={13} /> Copied</> : <><Copy size={13} /> {label || 'Copy'}</>}
        </button>
    );
}

/** "3 of 5 in use", or "3 in use" when the plan sets no cap (limit <= 0). */
function usageLabel(used: number, limit: number | undefined): string {
    if (limit === undefined || limit <= 0) return `${used} in use`;
    return `${used} of ${limit} in use`;
}

/** A copyable code block. Used for both the compose file and the CLI steps. */
function Snippet({ title, body }: { title: string; body: string }) {
    return (
        <div className="space-y-1">
            <div className="flex items-center justify-between gap-2">
                <span className="mono-label">{title}</span>
                <CopyButton value={body} />
            </div>
            <pre className="p-3 rounded-md bg-(--base-02) border border-(--base-03) font-mono text-[11px] leading-relaxed overflow-x-auto text-(--base-08)">
                {body}
            </pre>
        </div>
    );
}

/**
 * The deploy instructions for one machine: what to run, in what order, and what
 * to check afterwards.
 *
 * `warpKey` is null whenever the secret is not (or no longer) available - it is
 * stored as a hash, so it is shown exactly once at mint time. The snippet then
 * carries an obvious placeholder instead of a plausible-looking wrong value.
 */
function DeployKit({ kind, warpKey, enrollUrl, nodeEnrollToken }: {
    kind: 'node' | 'route-only';
    warpKey: string | null;
    enrollUrl: string;
    nodeEnrollToken?: string;
}) {
    const input = {
        apiKey: warpKey ?? '<your-warp-key>',
        enrollUrl,
        nodeEnrollToken,
    };
    const compose = kind === 'node' ? nodeCompose(input) : routeOnlyCompose(input);

    return (
        <div className="space-y-3 rounded-md border border-(--base-03) bg-(--base-01) p-4">
            <div className="flex items-center gap-2 text-sm font-medium text-(--base-09)">
                <Terminal size={15} className="text-(--accent-light)" />
                Deploy it on your machine
            </div>
            <p className="text-xs text-(--base-06)">
                Linux only: the tunnel uses kernel WireGuard, which needs host networking and
                NET_ADMIN. There is no Windows or macOS path for the machine itself.
            </p>
            <Snippet title={kind === 'node' ? 'byon-node.yml' : 'route-only.yml'} body={compose} />
            <Snippet title="Commands" body={deployCli(kind)} />
        </div>
    );
}

/** Shown in place of a card's controls when the account does not include it. */
function NotIncluded({ what, storeUrl, suspended }: { what: string; storeUrl: string | null; suspended: boolean }) {
    return (
        <div className="rounded-md border border-(--base-03) bg-(--base-02) p-4 space-y-2.5">
            <div className="flex items-center gap-2 text-sm font-medium text-(--base-08)">
                <Lock size={14} className="text-(--base-06)" />
                Not on your account
            </div>
            <p className="text-sm text-(--base-07)">
                {suspended
                    ? 'Your account is suspended, which pauses this until it is reactivated.'
                    : `Add ${what} to your plan, or ask an admin to enable it for you.`}
            </p>
            {!suspended && storeUrl && (
                <a href={storeUrl} target="_blank" rel="noopener noreferrer" className="btn btn-primary btn-sm inline-flex w-fit">
                    <ShoppingCart size={13} /> Get {what} <ExternalLink size={11} />
                </a>
            )}
        </div>
    );
}

export default function MyNodesPage() {
    const { featureFlags, entitlement, user, gatewayEnabled } = useAppData();

    const [nodes, setNodes] = useState<OwnNode[]>([]);
    const [tokens, setTokens] = useState<NodeEnrollToken[]>([]);
    const [kits, setKits] = useState<LinkKit[]>([]);
    const [kitUsage, setKitUsage] = useState<{ used: number; limit?: number }>({ used: 0 });
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
    const [revealedToken, setRevealedToken] = useState<string | null>(null);

    const [kitNameDraft, setKitNameDraft] = useState('');
    const [kitBusy, setKitBusy] = useState(false);
    const [revealedKit, setRevealedKit] = useState<{ name: string; warpKey: string } | null>(null);

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

    const loadKits = useCallback(async () => {
        // Route-only lives on the overlay, which only exists in gateway routing
        // mode - the endpoint 409s otherwise, so don't call it.
        if (!gatewayEnabled) return;
        const res = await listLinkKits();
        if (res.success) {
            setKits(res.kits || []);
            setKitUsage({ used: res.used ?? (res.kits?.length ?? 0), limit: res.limit });
        }
    }, [gatewayEnabled]);

    useEffect(() => { load(); }, [load]);
    useEffect(() => { loadKits(); }, [loadKits]);

    useEffect(() => {
        if (typeof window !== 'undefined') {
            setEnrollUrl(window.location.origin);
        }
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

    const handleMintNodeKey = async () => {
        setMinting(true);
        setError('');
        const res = await mintEnrollToken({ label: nodeLabelDraft.trim() || 'my machine', expiresDays: 7 });
        setMinting(false);
        if (!res.success || !res.token) {
            setError(res.message || 'Could not create an enrollment key.');
            return;
        }
        setRevealedToken(res.token);
        setNodeLabelDraft('');
        load();
    };

    const handleRevokeToken = async (id: string) => {
        const res = await revokeEnrollToken(id);
        if (!res.success) {
            setError(res.message || 'Could not revoke that key.');
            return;
        }
        load();
    };

    const handleMintKit = async () => {
        setKitBusy(true);
        setError('');
        const name = kitNameDraft.trim() || 'My location';
        const res = await mintLinkKit(name);
        setKitBusy(false);
        if (!res.success || !res.warp_key) {
            setError((res as { message?: string }).message || 'Could not create the connection.');
            return;
        }
        setRevealedKit({ name, warpKey: res.warp_key });
        setKitNameDraft('');
        loadKits();
    };

    const handleRevokeKit = async (linkId: string) => {
        setKitBusy(true);
        const res = await revokeLinkKit(linkId);
        setKitBusy(false);
        if (!res.success) {
            setError('Could not remove that connection.');
            return;
        }
        loadKits();
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

    const nodesAtCap = typeof nodeLimit === 'number' && nodeLimit > 0 && nodes.length >= nodeLimit;
    const kitsAtCap = typeof kitUsage.limit === 'number' && kitUsage.limit > 0 && kitUsage.used >= kitUsage.limit;

    return (
        <div className="p-6 max-w-4xl space-y-6 overflow-y-auto">
            <div className="flex flex-wrap items-start justify-between gap-3">
                <div className="min-w-0">
                    <h1 className="text-lg font-display font-bold text-(--base-09) mb-1">My machines</h1>
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
                    <span className="mono-label shrink-0 pt-0.5">{usageLabel(nodes.length, nodeLimit)}</span>
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
                        {revealedToken && (
                            <div className="rounded-md border border-(--accent-border) bg-(--accent-ghost) p-4 space-y-3">
                                <div className="text-sm font-medium text-(--base-09)">
                                    Your enrollment key. It is shown once.
                                </div>
                                <div className="flex items-center gap-2">
                                    <code className="input-mono flex-1 min-w-0 break-all bg-(--base-02) border border-(--base-03) rounded-md px-3 py-2 text-xs text-(--base-08) select-all">
                                        {revealedToken}
                                    </code>
                                    <CopyButton value={revealedToken} />
                                </div>
                                <p className="text-xs text-(--base-07)">
                                    It expires in 7 days and can be used once.
                                </p>
                                <DeployKit kind="node" warpKey={null} enrollUrl={enrollUrl} nodeEnrollToken={revealedToken} />
                                <button type="button" onClick={() => setRevealedToken(null)} className="btn btn-secondary btn-sm">
                                    I saved it
                                </button>
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

            {/* ── Route only ──────────────────────────────────────────────── */}
            <section className="card p-5 space-y-4">
                <div className="flex items-start justify-between gap-3">
                    <div className="min-w-0">
                        <h2 className="text-sm font-display font-semibold text-(--accent-light) flex items-center gap-2">
                            <Globe size={15} /> Route only
                        </h2>
                        <p className="text-sm text-(--base-07) mt-1 max-w-2xl">
                            <strong>You</strong> run the Minecraft server, on your own panel or none at all.
                            Dylaris gives it a protected address and takes the attack traffic. Your machine
                            makes outbound connections only — nothing is exposed.
                        </p>
                    </div>
                    <span className="mono-label shrink-0 pt-0.5">{usageLabel(kitUsage.used, kitUsage.limit)}</span>
                </div>

                {!gatewayEnabled ? (
                    <p className="text-sm text-(--base-06)">
                        Protected addresses need gateway routing, which is off on this platform.
                    </p>
                ) : !entitlementKnown ? (
                    <SkeletonCard height="h-16" />
                ) : !routeOnlyAllowed ? (
                    <NotIncluded what="route only" storeUrl={storeUrl} suspended={suspended} />
                ) : (
                    <>
                        <div className="flex flex-col sm:flex-row gap-2 sm:items-end">
                            <div className="flex-1 flex flex-col gap-[5px]">
                                <label className="input-label">Name this location</label>
                                <input
                                    className="input-field w-full"
                                    value={kitNameDraft}
                                    onChange={e => setKitNameDraft(e.target.value)}
                                    placeholder="home"
                                    disabled={kitsAtCap || suspended}
                                />
                            </div>
                            <button
                                onClick={handleMintKit}
                                disabled={kitBusy || kitsAtCap || suspended}
                                className="btn btn-primary disabled:opacity-40 disabled:cursor-not-allowed"
                                title={kitsAtCap ? 'You have used every protected address your plan includes' : undefined}
                            >
                                <Plus size={14} /> {kitBusy ? 'Working…' : 'Add a location'}
                            </button>
                        </div>

                        {kitsAtCap && (
                            <p className="flex items-start gap-1.5 text-xs text-(--warning-light)">
                                <AlertTriangle size={12} className="mt-0.5 shrink-0" />
                                <span>
                                    Every protected address your plan includes is in use. Add another in the
                                    store to connect a second location.
                                </span>
                            </p>
                        )}

                        {revealedKit && (
                            <div className="rounded-md border border-(--accent-border) bg-(--accent-ghost) p-4 space-y-3">
                                <div className="text-sm font-medium text-(--base-09)">
                                    {revealedKit.name} — your connection key. It is shown once.
                                </div>
                                <div className="flex items-center gap-2">
                                    <code className="input-mono flex-1 min-w-0 break-all bg-(--base-02) border border-(--base-03) rounded-md px-3 py-2 text-xs text-(--base-08) select-all">
                                        {revealedKit.warpKey}
                                    </code>
                                    <CopyButton value={revealedKit.warpKey} />
                                </div>
                                <DeployKit kind="route-only" warpKey={revealedKit.warpKey} enrollUrl={enrollUrl} />
                                <p className="text-xs text-(--base-07)">
                                    Once it is up, create the address itself under{' '}
                                    <a href="/routes" className="text-(--accent-light) hover:underline">Protected addresses</a>.
                                </p>
                                <button type="button" onClick={() => setRevealedKit(null)} className="btn btn-secondary btn-sm">
                                    I saved it
                                </button>
                            </div>
                        )}

                        <div className="border-t border-(--base-03) pt-3 space-y-2">
                            <div className="mono-label">Your locations</div>
                            {kits.length === 0 ? (
                                <p className="text-sm text-(--base-06)">
                                    No location connected yet. Add one above to get its deploy steps.
                                </p>
                            ) : (
                                kits.map(k => (
                                    <div key={k.id} className="flex items-center justify-between gap-3 rounded-md bg-(--base-02) border border-(--base-03) px-3 py-2.5">
                                        <div className="min-w-0">
                                            <div className="text-sm text-(--base-09) truncate">{k.name}</div>
                                            <div className="mono-label truncate">{k.link_id}</div>
                                        </div>
                                        <button
                                            onClick={() => handleRevokeKit(k.link_id)}
                                            disabled={kitBusy}
                                            className="text-(--base-06) hover:text-(--error-light) p-1.5 rounded-md transition-colors disabled:opacity-40"
                                            title="Remove this location"
                                        >
                                            <Trash2 size={14} />
                                        </button>
                                    </div>
                                ))
                            )}
                        </div>

                        {/* Reachable without a fresh key too: everything except the
                            secret is reproducible, and someone who saved their key
                            still needs the compose file the next time they rebuild. */}
                        {kits.length > 0 && !revealedKit && (
                            <details className="group">
                                <summary className="cursor-pointer text-xs text-(--base-06) hover:text-(--base-08) transition-colors select-none">
                                    Show the deploy steps again
                                </summary>
                                <div className="pt-3">
                                    <p className="text-xs text-(--base-06) mb-2">
                                        The key itself cannot be shown again — only its hash is stored. Paste the one
                                        you saved where the snippet says <code className="font-mono">&lt;your-warp-key&gt;</code>,
                                        or remove the location and add it again for a fresh key.
                                    </p>
                                    <DeployKit kind="route-only" warpKey={null} enrollUrl={enrollUrl} />
                                </div>
                            </details>
                        )}
                    </>
                )}
            </section>
        </div>
    );
}
