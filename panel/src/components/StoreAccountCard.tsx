"use client";

import { useState } from 'react';
import { AlertTriangle, Loader2, CloudOff } from 'lucide-react';
import Switch from '@/components/ui/Switch';
import {
    usagePct, trafficSwitch, backupSwitch, consequenceOf,
    type SwitchState,
} from '@/lib/storeAccount';
import { setStoreBillingConsent, type StoreAccountSummary } from '@/lib/api/store';

// What the tenant bought and how much of it is left, on the panel side.
//
// The numbers are the store's - it owns the money and already computes them for
// its own screens - and travel through Core, which names the account from the
// session. Nothing here recomputes an allowance: a second copy is the one that
// drifts, and this platform has already shipped a pricing page promising a
// number nothing enforced.

// A single allowance, drawn. The bar is deliberately the same shape for traffic
// and backups: they are the same question asked about different units, and two
// different treatments would invite reading them as two different kinds of
// limit.
function Allowance({ label, usedGb, ceilingGb, note }: {
    label: string;
    usedGb: number;
    ceilingGb: number;
    note?: string;
}) {
    const pct = usagePct(usedGb, ceilingGb);
    const tone = pct >= 100 ? 'bg-(--error)' : pct >= 80 ? 'bg-(--warning)' : 'bg-(--accent)';
    return (
        <div>
            <div className="flex items-baseline justify-between gap-3">
                <span className="text-sm text-(--base-08)">{label}</span>
                <span className="text-sm font-mono tabular-nums text-(--base-09)">
                    {usedGb} <span className="text-(--base-06)">/ {ceilingGb > 0 ? `${ceilingGb} GB` : 'none'}</span>
                </span>
            </div>
            <div className="mt-1.5 h-1.5 rounded-full bg-(--base-03) overflow-hidden">
                <div className={`h-full rounded-full transition-all ${tone}`} style={{ width: `${pct}%` }} />
            </div>
            {note && <p className="mt-1.5 text-xs text-(--base-06)">{note}</p>}
        </div>
    );
}

// One consent, with the consequence written under it rather than the setting
// name. "Metered billing: off" tells a tenant nothing; what they are choosing is
// whether running out costs money or stops the service.
function ConsentRow({ title, kind, state, busy, onChange }: {
    title: string;
    kind: 'traffic' | 'backup';
    state: SwitchState;
    busy: boolean;
    onChange: (next: boolean) => void;
}) {
    if (state.kind === 'unavailable') {
        return (
            <div className="flex items-start gap-2.5">
                <AlertTriangle size={15} className="text-(--base-06) mt-0.5 shrink-0" />
                <div className="min-w-0">
                    <div className="text-sm text-(--base-08)">{title}</div>
                    <p className="text-xs text-(--base-06) mt-0.5 leading-relaxed">{state.reason}</p>
                </div>
            </div>
        );
    }
    const on = state.kind === 'on';
    return (
        <div className="flex items-start justify-between gap-4">
            <div className="min-w-0">
                <div className="text-sm text-(--base-08)">{title}</div>
                <p className="text-xs text-(--base-06) mt-0.5 leading-relaxed">{consequenceOf(kind, on)}</p>
            </div>
            <div className="shrink-0 pt-0.5">
                {busy
                    ? <Loader2 size={16} className="animate-spin text-(--base-06)" />
                    : <Switch checked={on} onChange={onChange} ariaLabel={title} />}
            </div>
        </div>
    );
}

export default function StoreAccountCard({ summary, onChanged, onError }: {
    summary: StoreAccountSummary;
    onChanged: () => void;
    onError: (message: string) => void;
}) {
    const [busy, setBusy] = useState<'traffic' | 'backup' | null>(null);

    // A quiet storefront is its own state. Rendering an account with nothing in
    // it would read as "you have no subscription", which is the opposite of what
    // happened and sends the reader to buy something they already own.
    if (summary.reachable === false) {
        return (
            <div className="card p-5 flex items-start gap-2.5">
                <CloudOff size={16} className="text-(--base-06) mt-0.5 shrink-0" />
                <p className="text-sm text-(--base-07) leading-relaxed">
                    {summary.message || 'The store could not be reached, so your subscription details are not shown.'}
                </p>
            </div>
        );
    }

    if (!summary.subscribed) {
        return (
            <div className="card p-5">
                <p className="text-sm text-(--base-07)">
                    No active subscription on this account. Servers you run on your own hardware are
                    unaffected; a subscription is what buys nodes, addresses and the allowances below.
                </p>
            </div>
        );
    }

    const apply = async (change: { traffic?: boolean; backup?: boolean }) => {
        setBusy(change.traffic !== undefined ? 'traffic' : 'backup');
        const res = await setStoreBillingConsent(change);
        setBusy(null);
        if (!res.success) {
            onError(res.message || 'The change could not be saved.');
            return;
        }
        onChanged();
    };

    const units = [
        summary.nodes ? `${summary.nodes} node${summary.nodes === 1 ? '' : 's'}` : '',
        summary.routeOnly ? `${summary.routeOnly} route-only` : '',
    ].filter(Boolean).join(' + ');

    return (
        <div className="space-y-4">
            <div className="card p-5 space-y-4">
                <div className="flex flex-wrap items-center justify-between gap-2">
                    <div className="text-sm text-(--base-08)">
                        Subscription
                        {units && <span className="text-(--base-06)"> - {units}</span>}
                    </div>
                    <span className={`badge ${summary.status === 'active' ? 'badge-success' : 'badge-warning'}`}>
                        {summary.status || 'unknown'}
                    </span>
                </div>

                {summary.traffic && (
                    <Allowance
                        label="Traffic this period"
                        usedGb={summary.traffic.usedGb ?? 0}
                        ceilingGb={summary.traffic.ceilingGb ?? 0}
                        note={summary.traffic.cutOff ? 'Your servers are currently stopped for traffic.' : undefined}
                    />
                )}

                {summary.backup && (
                    <Allowance
                        label="Backup storage"
                        usedGb={summary.backup.usedGb ?? 0}
                        ceilingGb={summary.backup.ceilingGb ?? 0}
                        // Storage on a bucket the tenant connected is neither
                        // billed nor capped. Folding it into the bar would put
                        // somebody over an allowance for space we do not provide,
                        // and leaving it out entirely would make 400 GB read as
                        // nothing on a screen headed "backup storage".
                        note={summary.backup.ownStorageGb
                            ? `Plus ${summary.backup.ownStorageGb} GB on your own storage, which is neither counted nor billed.`
                            : undefined}
                    />
                )}
            </div>

            <div className="card p-5 space-y-4">
                <div className="text-sm font-medium text-(--base-09)">When an allowance runs out</div>
                <ConsentRow
                    title="Bill me for extra traffic"
                    kind="traffic"
                    state={trafficSwitch(summary)}
                    busy={busy === 'traffic'}
                    onChange={next => apply({ traffic: next })}
                />
                <ConsentRow
                    title="Bill me for extra backup storage"
                    kind="backup"
                    state={backupSwitch(summary)}
                    busy={busy === 'backup'}
                    onChange={next => apply({ backup: next })}
                />
            </div>
        </div>
    );
}
