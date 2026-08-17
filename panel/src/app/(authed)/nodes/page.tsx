"use client";

import { useState, useEffect, useCallback } from 'react';
import { HardDrive, Plus, Copy, Check, Trash2, AlertTriangle, Clock, ShieldCheck, Globe, ExternalLink, CircleCheck, CircleSlash } from 'lucide-react';
import { useAppData } from '@/lib/AppDataContext';
import { getNodes } from '@/lib/api';
import { getStoreStatus } from '@/lib/api/store';
import { mintEnrollToken, listEnrollTokens, revokeEnrollToken } from '@/lib/api/nodeAdmission';
import type { NodeEnrollToken } from '@/lib/api/types';
import { nodeLabel } from '@/lib/nodeLabel';
import { nodeConnectivity, dotFor } from '@/lib/connectivity';
import { SkeletonCard } from '@/components/Skeleton';

// ---------------------------------------------------------------------------
// "My nodes" - the TENANT side of bring-your-own-node.
//
// This page exists because enrolling a node lived only in Settings -> Warp,
// which is admin-only. A paying BYON customer therefore had no way to reach it:
// they saw neither Settings nor (before owning a server) Access. Everything here
// uses the already tenant-scoped endpoints - /api/nodes returns only the
// caller's own nodes, /api/nodes/enroll-token mints against the caller - so no
// admin route is widened.
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

function CopyButton({ value, label }: { value: string; label?: string }) {
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
            className="btn btn-secondary btn-sm shrink-0"
        >
            {copied ? <><Check size={13} /> Copied</> : <><Copy size={13} /> {label || 'Copy'}</>}
        </button>
    );
}

/** One line of "what your account includes", stated either way. */
function EntitlementRow({ on, title, children }: { on: boolean; title: string; children: React.ReactNode }) {
    return (
        <div className="flex items-start gap-2.5">
            {on
                ? <CircleCheck size={15} className="shrink-0 mt-0.5 text-(--success-light)" />
                : <CircleSlash size={15} className="shrink-0 mt-0.5 text-(--base-06)" />}
            <div className="min-w-0">
                <div className={`text-sm font-medium ${on ? 'text-(--base-09)' : 'text-(--base-07)'}`}>{title}</div>
                <p className="text-xs text-(--base-06) leading-snug mt-0.5">{children}</p>
            </div>
        </div>
    );
}

export default function MyNodesPage() {
    const { featureFlags, entitlement, user, gatewayEnabled } = useAppData();
    const [nodes, setNodes] = useState<OwnNode[]>([]);
    const [tokens, setTokens] = useState<NodeEnrollToken[]>([]);
    const [loading, setLoading] = useState(true);
    const [minting, setMinting] = useState(false);
    const [label, setLabel] = useState('');
    const [revealed, setRevealed] = useState<{ token: string; fingerprint?: string } | null>(null);
    const [error, setError] = useState('');
    // The real storefront origin, not a hardcoded dylaris.com: a self-host
    // install with the store wired points somewhere else, and a link that goes to
    // the wrong shop is worse than no link.
    const [storeUrl, setStoreUrl] = useState<string | null>(null);

    const isAdmin = user?.isAdmin ?? false;
    const entitled = entitlement?.byon ?? false;
    const routeOnly = entitlement?.routeOnly ?? false;
    // null means "not fetched yet". Rendering the refusal during that window
    // would tell an entitled tenant they have nothing, then take it back.
    const entitlementKnown = entitlement !== null;

    const load = useCallback(async () => {
        const [n, t] = await Promise.all([getNodes(), listEnrollTokens()]);
        if (n.success && Array.isArray(n.nodes)) setNodes(n.nodes as OwnNode[]);
        if (t.success && Array.isArray(t.tokens)) setTokens(t.tokens);
        setLoading(false);
    }, []);

    useEffect(() => { load(); }, [load]);

    useEffect(() => {
        if (!featureFlags.store) return;
        let cancelled = false;
        getStoreStatus().then(res => {
            if (!cancelled && res.success && res.storeUrl) setStoreUrl(res.storeUrl);
        });
        return () => { cancelled = true; };
    }, [featureFlags.store]);

    const handleMint = async () => {
        setMinting(true);
        setError('');
        const res = await mintEnrollToken({ label: label.trim() || 'my machine', expiresDays: 7 });
        setMinting(false);
        if (!res.success || !res.token) {
            setError(res.message || 'Could not create an enrollment key.');
            return;
        }
        setRevealed({ token: res.token, fingerprint: res.grpcTlsFingerprint });
        setLabel('');
        load();
    };

    const handleRevoke = async (id: string) => {
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
                <h1 className="text-lg font-display font-bold text-(--base-09) mb-2">My nodes</h1>
                <p className="text-sm text-(--base-07)">
                    Bring-your-own-node is turned off on this platform.
                </p>
            </div>
        );
    }

    return (
        <div className="p-6 max-w-3xl space-y-6 overflow-y-auto">
            <div>
                <h1 className="text-lg font-display font-bold text-(--base-09) mb-1 flex items-center gap-2">
                    <HardDrive size={18} className="text-(--accent-light)" />
                    My nodes
                </h1>
                <p className="text-sm text-(--base-07)">
                    Run Minecraft servers on your own machine. It connects out to this platform through an
                    encrypted tunnel, so it needs no public IP and no port forwarding.
                </p>
            </div>

            {error && (
                <div className="alert alert-error">
                    <AlertTriangle size={14} className="shrink-0 mt-0.5" />
                    <span>{error}</span>
                </div>
            )}

            {/* What the account actually includes, stated for BOTH capabilities.
                A tenant with route-only but not BYON used to land here, see a
                bare refusal, and have no way to learn that the other half of
                their plan exists. */}
            {entitlementKnown && !isAdmin && (
                <div className="card p-5 space-y-3">
                    <h2 className="text-sm font-display font-semibold text-(--accent-light)">Your access</h2>
                    <EntitlementRow on={entitled} title="Bring your own node">
                        {entitled
                            ? 'Connect your own machines and run servers on them.'
                            : 'Not included in your account yet.'}
                    </EntitlementRow>
                    <EntitlementRow on={routeOnly} title="Route only">
                        {routeOnly
                            ? 'A protected Dylaris address for a Minecraft server you already run yourself. Your operator issues the connection kit.'
                            : 'A protected address for a server you host yourself. Not included in your account yet.'}
                    </EntitlementRow>
                    {entitlement?.source === 'suspended' && (
                        <p className="text-sm text-(--warning-light)">
                            Your account is suspended, which pauses everything above until it is reactivated.
                        </p>
                    )}
                    {routeOnly && gatewayEnabled && (
                        <a href="/routes" className="btn btn-secondary btn-sm inline-flex w-fit">
                            <Globe size={13} /> Manage your addresses
                        </a>
                    )}
                </div>
            )}

            {/* Not entitled to BYON: say so plainly and stop. Showing the
                enrollment form and letting the backend refuse would be a worse
                version of the same answer. Admins bypass, since their own account
                may hold no plan. */}
            {entitlementKnown && !entitled && !isAdmin ? (
                <div className="card p-5 space-y-3">
                    <div className="flex items-center gap-2 text-sm font-medium text-(--base-09)">
                        <ShieldCheck size={15} className="text-(--base-06)" />
                        Bring-your-own-node is not on your account
                    </div>
                    <p className="text-sm text-(--base-07)">
                        {entitlement?.source === 'suspended'
                            ? 'Reactivate your account to connect machines again.'
                            : featureFlags.store
                                ? 'Add it to your plan in the store, or ask an admin to enable it for you.'
                                : 'Ask an admin to enable it for you.'}
                    </p>
                    {featureFlags.store && entitlement?.source !== 'suspended' && (
                        storeUrl ? (
                            <a
                                href={storeUrl}
                                target="_blank"
                                rel="noopener noreferrer"
                                className="btn btn-primary btn-sm inline-flex w-fit"
                            >
                                Get a plan <ExternalLink size={12} />
                            </a>
                        ) : (
                            // features.store is on but /store/status has not answered
                            // yet (or the storefront is unreachable). Never guess a
                            // URL here - a dead link reads as a broken checkout.
                            <span className="text-xs text-(--base-06)">Loading store link…</span>
                        )
                    )}
                </div>
            ) : (
                <>
                    {/* Enrollment */}
                    <div className="card p-5 space-y-4">
                        <div>
                            <h2 className="text-sm font-display font-semibold text-(--accent-light)">Connect a machine</h2>
                            <p className="text-xs text-(--base-06) mt-1">
                                Create a one-time key, then run the installer on the machine you want to use. Each
                                machine needs its own key.
                            </p>
                        </div>
                        <div className="flex flex-col sm:flex-row gap-2 sm:items-end">
                            <div className="flex-1 flex flex-col gap-[5px]">
                                <label className="input-label">Name this machine</label>
                                <input
                                    className="input-field w-full"
                                    value={label}
                                    onChange={e => setLabel(e.target.value)}
                                    placeholder="home-desktop"
                                />
                            </div>
                            <button onClick={handleMint} disabled={minting} className="btn btn-primary disabled:opacity-40">
                                <Plus size={14} /> {minting ? 'Creating…' : 'Create key'}
                            </button>
                        </div>

                        {/* Shown once. The key is not retrievable afterwards, so the
                            copy has to say that before the user closes it. */}
                        {revealed && (
                            <div className="rounded-md border border-(--accent-border) bg-(--accent-ghost) p-4 space-y-3">
                                <div className="text-sm font-medium text-(--base-09)">
                                    Your enrollment key. It is shown once.
                                </div>
                                <div className="flex items-center gap-2">
                                    <code className="input-mono flex-1 min-w-0 break-all bg-(--base-02) border border-(--base-03) rounded-md px-3 py-2 text-xs text-(--base-08) select-all">
                                        {revealed.token}
                                    </code>
                                    <CopyButton value={revealed.token} />
                                </div>
                                <p className="text-xs text-(--base-07)">
                                    Run the installer on your machine and paste this key when it asks. The key expires in
                                    7 days and can be used once.
                                </p>
                                <button
                                    type="button"
                                    onClick={() => setRevealed(null)}
                                    className="btn btn-secondary btn-sm"
                                >
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
                                            onClick={() => handleRevoke(t.id)}
                                            className="text-(--base-06) hover:text-(--error-light) p-1.5 rounded-md transition-colors"
                                            title="Revoke this key"
                                        >
                                            <Trash2 size={14} />
                                        </button>
                                    </div>
                                ))}
                            </div>
                        )}
                    </div>

                    {/* Connected machines */}
                    <div className="card p-5 space-y-3">
                        <h2 className="text-sm font-display font-semibold text-(--accent-light)">Your machines</h2>
                        {loading ? (
                            <SkeletonCard height="h-16" />
                        ) : nodes.length === 0 ? (
                            <p className="text-sm text-(--base-06)">
                                No machine connected yet. Create a key above and run the installer.
                            </p>
                        ) : (
                            <div className="space-y-2">
                                {nodes.map(n => {
                                    const { tier } = nodeConnectivity(n.status, n.lastSeenAt, Date.now());
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
                                })}
                            </div>
                        )}
                    </div>
                </>
            )}
        </div>
    );
}
