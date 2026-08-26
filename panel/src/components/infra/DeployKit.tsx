"use client";

import { useState } from 'react';
import Link from 'next/link';
import { Copy, Check, Terminal, Lock, ShoppingCart, ExternalLink, Link2 as LinkIcon } from 'lucide-react';
import { nodeCompose, routeOnlyCompose, deployCli } from '@/lib/warpDeploy';
import type { DeployPlatform } from '@/lib/warpDeploy';
import type { WarpDeployConfig } from '@/lib/api/warpDeployConfig';

// Shared by both halves of "my infrastructure". They used to be two pages with
// their own copies, which is how route-only ended up with a mint flow on each
// of them.

export function CopyButton({ value, label, className }: { value: string; label?: string; className?: string }) {
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

/**
 * A secret shown exactly once. Blurred until asked for, because it sits on a
 * page someone may well have open while screen-sharing a deploy - and because
 * it is not the thing they came for: both keys are already filled into the
 * compose file below, so the normal path never reads them at all.
 *
 * Still fully retrievable in the moment: only a hash is stored, so this is the
 * one chance to save it.
 */
export function SecretField({ label, value, note }: { label: string; value: string; note?: string }) {
    const [shown, setShown] = useState(false);
    return (
        <div className="space-y-1">
            <span className="mono-label">{label}</span>
            <div className="flex items-center gap-2">
                <button
                    type="button"
                    onClick={() => setShown(s => !s)}
                    aria-label={shown ? `Hide ${label}` : `Reveal ${label}`}
                    aria-pressed={shown}
                    title={shown ? 'Hide' : 'Click to reveal'}
                    className="group flex-1 min-w-0 text-left rounded-md bg-(--base-02) border border-(--base-03) px-3 py-2 hover:border-(--base-04) transition-colors cursor-pointer"
                >
                    <code
                        className={`input-mono block break-all text-xs text-(--base-08) transition-[filter] motion-reduce:transition-none ${
                            shown ? 'select-all' : 'blur-[5px] select-none'
                        }`}
                    >
                        {value}
                    </code>
                </button>
                <CopyButton value={value} />
            </div>
            {note && <p className="text-xs text-(--base-07)">{note}</p>}
        </div>
    );
}

/** A copyable code block. Used for both the compose file and the CLI steps. */
export function Snippet({ title, body }: { title: string; body: string }) {
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
export function DeployKit({ kind, warpKey, enrollUrl, nodeEnrollToken, grpcTlsFingerprint, nodeId, config }: {
    kind: 'node' | 'route-only';
    warpKey: string | null;
    enrollUrl: string;
    nodeEnrollToken?: string;
    grpcTlsFingerprint?: string;
    nodeId?: string;
    config?: WarpDeployConfig | null;
}) {
    // Only route-only runs on Docker Desktop. A managed node drives the host's
    // Docker socket to run Minecraft containers, which has its own path and port
    // semantics there and is not covered, so the toggle stays off for it.
    const [platform, setPlatform] = useState<DeployPlatform>('linux');
    const input = {
        apiKey: warpKey ?? '<your-warp-key>',
        enrollUrl,
        nodeEnrollToken,
        grpcTlsFingerprint,
        nodeId,
        platform,
        // Undetermined values stay undefined so the snippet keeps its
        // placeholder: a blank tells the reader something is missing, an empty
        // string looks like a setting that was deliberately cleared.
        tunnelSubnets: config?.tunnelSubnets || undefined,
    };
    const compose = kind === 'node' ? nodeCompose(input) : routeOnlyCompose(input);

    return (
        <div className="space-y-3 rounded-md border border-(--base-03) bg-(--base-01) p-4">
            <div className="flex items-center gap-2 text-sm font-medium text-(--base-09)">
                <Terminal size={15} className="text-(--accent-light)" />
                Deploy it on your machine
            </div>
            {kind === 'route-only' ? (
                <>
                    <div className="flex items-center gap-1" role="group" aria-label="Target machine">
                        {(['linux', 'windows'] as const).map((p) => (
                            <button
                                key={p}
                                type="button"
                                onClick={() => setPlatform(p)}
                                aria-pressed={platform === p}
                                className={`rounded-md px-2.5 py-1 text-xs transition-colors focus-visible:outline-none focus-visible:[box-shadow:var(--focus-ring)] ${
                                    platform === p
                                        ? 'bg-(--accent) text-(--base-00)'
                                        : 'bg-(--base-02) text-(--base-07) hover:bg-(--base-03) hover:text-(--base-09)'
                                }`}
                            >
                                {p === 'linux' ? 'Linux' : 'Windows (Docker Desktop)'}
                            </button>
                        ))}
                    </div>
                    <p className="text-xs text-(--base-06)">
                        {platform === 'linux'
                            ? 'The tunnel uses kernel WireGuard, which needs host networking and NET_ADMIN.'
                            : 'Host networking on Docker Desktop joins the WSL2 VM, not Windows, so the snippet points the link at host.docker.internal. Your Minecraft server keeps running on Windows as it does now.'}
                    </p>
                </>
            ) : (
                <p className="text-xs text-(--base-06)">
                    Linux only: the node drives the host&apos;s Docker socket to run your Minecraft
                    containers, which needs host networking and NET_ADMIN.
                </p>
            )}
            <Snippet title={kind === 'node' ? 'byon-node.yml' : 'route-only.yml'} body={compose} />
            <Snippet title="Commands" body={deployCli(kind)} />
        </div>
    );
}

/** Shown in place of a tab's controls when the account does not include it. */
export function NotIncluded({ what, storeUrl, suspended, storeLinked }: {
    what: string;
    storeUrl: string | null;
    suspended: boolean;
    /** null = not known yet. See below for why that is not the same as false. */
    storeLinked?: boolean | null;
}) {
    // Three different problems with three different fixes, and telling someone
    // to buy a plan they already bought because the ACCOUNTS are not joined is
    // the worst of them: the store shows a paid subscription, the panel shows
    // nothing, and neither screen mentions the join that is missing.
    //
    // storeLinked === null means we have not been told yet (or the storefront
    // could not be reached). That falls through to the generic message rather
    // than claiming a disconnection we cannot support.
    const notLinked = storeLinked === false;
    return (
        <div className="rounded-md border border-(--base-03) bg-(--base-02) p-4 space-y-2.5">
            <div className="flex items-center gap-2 text-sm font-medium text-(--base-08)">
                <Lock size={14} className="text-(--base-06)" />
                {suspended ? 'Paused' : notLinked ? 'Connect your account first' : 'Not on your account'}
            </div>
            <p className="text-sm text-(--base-07)">
                {suspended
                    ? 'Your account is suspended, which pauses this until it is reactivated.'
                    : notLinked
                      ? 'Your panel account is not connected to the Dylaris store yet. A subscription raises no limits here until it is, so connect it first and this unlocks on its own.'
                      : `Add ${what} to your plan, or ask an admin to enable it for you.`}
            </p>
            {!suspended && notLinked && (
                <Link href="/account/store" className="btn btn-primary btn-sm inline-flex w-fit">
                    <LinkIcon size={13} /> Connect the store
                </Link>
            )}
            {!suspended && !notLinked && storeUrl && (
                <a href={storeUrl} target="_blank" rel="noopener noreferrer" className="btn btn-primary btn-sm inline-flex w-fit">
                    <ShoppingCart size={13} /> Get {what} <ExternalLink size={11} />
                </a>
            )}
        </div>
    );
}

/** "3 of 5 in use", or "3 in use" when the plan sets no cap (limit <= 0). */
export function usageLabel(used: number, limit: number | undefined): string {
    if (limit === undefined || limit <= 0) return `${used} in use`;
    return `${used} of ${limit} in use`;
}
