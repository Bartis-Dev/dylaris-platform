"use client";

import { useState, useEffect, useCallback } from 'react';
import { HardDrive, Plus, Copy, Check, Trash2, AlertTriangle, Clock, ShieldCheck } from 'lucide-react';
import { useAppData } from '@/lib/AppDataContext';
import { getNodes } from '@/lib/api';
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

export default function MyNodesPage() {
    const { featureFlags, entitlement, user } = useAppData();
    const [nodes, setNodes] = useState<OwnNode[]>([]);
    const [tokens, setTokens] = useState<NodeEnrollToken[]>([]);
    const [loading, setLoading] = useState(true);
    const [minting, setMinting] = useState(false);
    const [label, setLabel] = useState('');
    const [revealed, setRevealed] = useState<{ token: string; fingerprint?: string } | null>(null);
    const [error, setError] = useState('');

    const isAdmin = user?.isAdmin ?? false;
    const entitled = entitlement?.byon ?? false;
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

            {/* Not entitled: say so plainly and stop. Showing the enrollment form
                and letting the backend refuse would be a worse version of the same
                answer. Admins bypass, since their own account may hold no plan. */}
            {entitlementKnown && !entitled && !isAdmin ? (
                <div className="card p-5 space-y-2">
                    <div className="flex items-center gap-2 text-sm font-medium text-(--base-09)">
                        <ShieldCheck size={15} className="text-(--base-06)" />
                        Not enabled for your account yet
                    </div>
                    <p className="text-sm text-(--base-07)">
                        Your account does not currently include bring-your-own-node.
                        {featureFlags.store
                            ? ' You can add it to your plan, or ask an admin to enable it for you.'
                            : ' Ask an admin to enable it for you.'}
                    </p>
                    {entitlement?.source === 'suspended' && (
                        <p className="text-sm text-(--warning-light)">
                            Your account is currently suspended, which pauses node access until it is reactivated.
                        </p>
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
