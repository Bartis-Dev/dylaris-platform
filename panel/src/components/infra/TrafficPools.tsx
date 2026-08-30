"use client";

import { useEffect, useState } from 'react';
import { Gauge, AlertTriangle } from 'lucide-react';
import { getMyBilling, type MyTrafficStatus, type TrafficPool } from '@/lib/api/billing';

/**
 * What the tenant has used against what they hold, one bar per allowance.
 *
 * Deliberately NOT one bar. Player traffic is capped per region and file
 * transfers hold their own pool, so a tenant can sit at 30% of three allowances
 * and at 130% of a fourth - and it is the fourth that stops them. A single
 * summed bar is the one number that cannot say which.
 *
 * The bars are drawn against the INCLUDED allowance, not the fair-use ceiling.
 * The buffer is grace rather than allowance: a warning that only appears past it
 * arrives after the point where the tenant could still have done something.
 */

const KIND_LABEL: Record<string, string> = {
    edge: 'Player traffic',
    relay: 'File transfers',
    warp: 'Overlay',
};

/** A pool with no configured allowance is not drawn: there is nothing to be a
 *  percentage of, and a bar filling against no limit is a false alarm. */
export function poolsWorthShowing(pools: TrafficPool[] | undefined): TrafficPool[] {
    return (pools || []).filter(p => p.includedGb !== null && p.includedGb > 0);
}

/** The notice a pool has earned. Kept beside the bar so the two never disagree. */
export function poolTone(warn: number): { bar: string; text: string; label: string } | null {
    if (warn >= 100) return { bar: 'bg-(--error)', text: 'text-(--error)', label: 'over the allowance' };
    if (warn >= 90) return { bar: 'bg-(--error)', text: 'text-(--error)', label: '90% used' };
    if (warn >= 80) return { bar: 'bg-(--warning)', text: 'text-(--warning)', label: '80% used' };
    return null;
}

function poolName(p: TrafficPool): string {
    const kind = KIND_LABEL[p.kind] ?? p.kind;
    // "*" is the region a non-regional pool is stored under. Showing it raw would
    // put a literal asterisk in front of the tenant as if it meant something.
    return p.region === '*' ? kind : `${kind} · ${p.region}`;
}

export default function TrafficPools() {
    const [traffic, setTraffic] = useState<MyTrafficStatus | null>(null);
    const [loaded, setLoaded] = useState(false);

    useEffect(() => {
        let alive = true;
        getMyBilling()
            .then(res => { if (alive) setTraffic(res.traffic || null); })
            .catch(() => { /* a failed read shows nothing rather than a zeroed bar */ })
            .finally(() => { if (alive) setLoaded(true); });
        return () => { alive = false; };
    }, []);

    const pools = poolsWorthShowing(traffic?.pools);
    // Nothing metered against a limit - a self-hosted install, or a platform
    // where nobody has set an allowance yet. Silence is the honest answer.
    if (!loaded || !traffic || pools.length === 0) return null;

    const metered = traffic.billingEnabled;

    return (
        <section className="card p-5">
            <div className="flex flex-wrap items-start justify-between gap-3 mb-4">
                <div className="flex items-start gap-2.5 min-w-0">
                    <Gauge size={16} className="text-(--base-06) mt-0.5 shrink-0" />
                    <div className="min-w-0">
                        <h2 className="text-sm font-medium text-(--base-09)">Traffic this month</h2>
                        <p className="text-xs text-(--base-06) mt-0.5 max-w-2xl">
                            Each of these is its own allowance: using one up does not touch another. Player
                            traffic counts per region, file transfers hold a single pool.
                        </p>
                    </div>
                </div>
                {traffic.warn !== undefined && traffic.warn >= 80 && (
                    <span
                        className={`flex items-center gap-1.5 text-xs shrink-0 ${
                            traffic.warn >= 90 ? 'text-(--error)' : 'text-(--warning)'
                        }`}
                    >
                        <AlertTriangle size={13} />
                        {metered
                            ? 'Past this you are billed per additional TB.'
                            : 'Past this your servers are stopped, not billed.'}
                    </span>
                )}
            </div>

            <ul className="space-y-3">
                {pools.map(p => {
                    const included = p.includedGb as number;
                    const tone = poolTone(p.warn);
                    // The BAR is clamped so it cannot overflow its track; the
                    // NUMBER beside it is not, because someone at 300% has to be
                    // told 300% rather than a reassuring full bar.
                    const width = Math.min(100, Math.max(0, p.pct));
                    return (
                        <li key={`${p.kind}|${p.region}`}>
                            <div className="flex flex-wrap items-baseline justify-between gap-2 mb-1.5">
                                <span className="text-xs text-(--base-08)">{poolName(p)}</span>
                                <span className="text-xs text-(--base-06) tabular-nums">
                                    <span className={tone ? tone.text : 'text-(--base-08)'}>{p.usedGb} GB</span>
                                    {' of '}{included} GB
                                    {tone && <span className={`ml-2 ${tone.text}`}>{tone.label}</span>}
                                </span>
                            </div>
                            <div
                                className="h-1.5 rounded-full bg-(--base-03) overflow-hidden"
                                role="progressbar"
                                aria-valuenow={p.pct}
                                aria-valuemin={0}
                                aria-valuemax={100}
                                aria-label={`${poolName(p)}: ${p.usedGb} of ${included} GB`}
                            >
                                <div
                                    className={`h-full rounded-full transition-all ${tone ? tone.bar : 'bg-(--accent)'}`}
                                    style={{ width: `${width}%` }}
                                />
                            </div>
                        </li>
                    );
                })}
            </ul>
        </section>
    );
}
