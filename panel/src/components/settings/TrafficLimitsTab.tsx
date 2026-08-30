"use client";

import { useCallback, useEffect, useMemo, useState } from 'react';
import { Gauge, AlertTriangle, Trash2 } from 'lucide-react';
import SettingsPage from '@/components/settings/SettingsPage';
import SettingsCard from '@/components/settings/SettingsCard';
import { LimitField } from '@/components/settings/LimitField';
import { toast } from '@/components/ui/Toast';
import { getInfrastructureOverview } from '@/lib/api';
import {
    listTrafficLimits,
    setTrafficLimit,
    writeFor,
    TRAFFIC_KINDS,
    KIND_LABELS,
    KIND_HINTS,
    type TrafficLimit,
} from '@/lib/api/trafficLimits';

// The two scopes an operator edits here. A per-USER override lives on the user,
// the same way the route limit does; this screen is the policy behind them.
const SCOPES = [
    { id: 'global', label: 'Platform default' },
    { id: 'user_default', label: 'Default for tenants' },
] as const;

type ScopeID = (typeof SCOPES)[number]['id'];

interface CellState {
    set: boolean;
    includedGb: number | null;
    maxPurchaseGb: number | null;
}

const cellKey = (region: string, kind: string) => `${region}|${kind}`;

function cellFrom(row: TrafficLimit | undefined): CellState {
    if (!row) return { set: false, includedGb: null, maxPurchaseGb: null };
    return { set: true, includedGb: row.includedGb, maxPurchaseGb: row.maxPurchaseGb };
}

export default function TrafficLimitsTab() {
    const [rows, setRows] = useState<TrafficLimit[]>([]);
    const [liveRegions, setLiveRegions] = useState<string[]>([]);
    const [scope, setScope] = useState<ScopeID>('global');
    const [cells, setCells] = useState<Record<string, CellState>>({});
    const [loading, setLoading] = useState(true);
    const [savingKey, setSavingKey] = useState<string | null>(null);

    const load = useCallback(async () => {
        setLoading(true);
        const [limits, infra] = await Promise.all([
            listTrafficLimits(),
            // The regions the edges actually REPORT. Without them the screen can
            // only show regions somebody already configured, which is exactly
            // the set that does not need attention - the gap is the regions
            // carrying traffic that nothing limits.
            getInfrastructureOverview().catch(() => null),
        ]);
        if (limits.success) setRows(limits.limits || []);
        else toast(limits.message || 'Could not load traffic limits.', false);

        const regions = new Set<string>();
        for (const e of (infra?.edges || [])) if (e.region) regions.add(e.region);
        setLiveRegions([...regions].sort());
        setLoading(false);
    }, []);

    useEffect(() => { load(); }, [load]);

    // Every region worth a row: what the edges report, plus anything already
    // configured (a region can be retired while its limit still exists, and a
    // row nobody can see is a row nobody can clear).
    const regions = useMemo(() => {
        const s = new Set<string>(liveRegions);
        for (const r of rows) s.add(r.region);
        return [...s].sort();
    }, [liveRegions, rows]);

    const rowFor = useCallback(
        (region: string, kind: string) =>
            rows.find(r => r.scope === scope && r.region === region && r.kind === kind),
        [rows, scope],
    );

    // Reset the edit buffer whenever the scope or the loaded rows change, so the
    // fields always show what is stored rather than a leftover from the scope
    // the operator was looking at a moment ago.
    useEffect(() => {
        const next: Record<string, CellState> = {};
        for (const region of regions) {
            for (const kind of TRAFFIC_KINDS) {
                next[cellKey(region, kind)] = cellFrom(rowFor(region, kind));
            }
        }
        setCells(next);
    }, [regions, rowFor]);

    const update = (region: string, kind: string, patch: Partial<CellState>) => {
        const k = cellKey(region, kind);
        setCells(c => ({ ...c, [k]: { ...c[k], ...patch } }));
    };

    const save = async (region: string, kind: string) => {
        const k = cellKey(region, kind);
        const c = cells[k];
        if (!c) return;
        setSavingKey(k);
        const included = writeFor(c.set, c.includedGb);
        const purchase = writeFor(c.set, c.maxPurchaseGb);
        const res = await setTrafficLimit({
            scope,
            region,
            kind,
            includedMode: included.mode,
            includedGb: included.gb,
            purchaseMode: purchase.mode,
            purchaseGb: purchase.gb,
        });
        setSavingKey(null);
        if (!res.success) {
            toast(res.message || 'Could not save the limit.', false);
            return;
        }
        toast(c.set ? `Saved ${region} · ${KIND_LABELS[kind] ?? kind}` : 'Cleared - this scope now defers');
        await load();
    };

    const overrides = rows.filter(r => r.scope.startsWith('user:'));

    return (
        <SettingsPage
            title="Traffic limits"
            description="How much traffic is included per region, and how much may be bought on top. Set per region because a terabyte does not cost the same everywhere."
            icon={Gauge}
            width="5xl"
            loading={loading}
            skeletonCards={2}
        >
            <div className="flex items-center gap-0.5 bg-(--base-02) border border-(--base-03) rounded-lg p-1 w-fit">
                {SCOPES.map(s => (
                    <button
                        key={s.id}
                        type="button"
                        onClick={() => setScope(s.id)}
                        aria-pressed={scope === s.id}
                        className={`px-4 py-1.5 rounded-md text-sm font-medium transition-all ${
                            scope === s.id ? 'bg-(--accent) text-white shadow-sm' : 'text-(--base-07) hover:text-(--base-09)'
                        }`}
                    >
                        {s.label}
                    </button>
                ))}
            </div>

            {regions.length === 0 && (
                <div className="card p-8 text-center text-(--base-06) text-sm">
                    No regions yet. A region appears here once an edge reports one.
                </div>
            )}

            {regions.map(region => (
                <SettingsCard
                    key={region}
                    title={region}
                    description={
                        liveRegions.includes(region)
                            ? undefined
                            : 'No edge is reporting this region right now. Its rows still apply to traffic already recorded there.'
                    }
                >
                    <div className="space-y-4">
                        {TRAFFIC_KINDS.map(kind => {
                            const k = cellKey(region, kind);
                            const c = cells[k];
                            if (!c) return null;
                            return (
                                <div key={kind} className="rounded-md border border-(--base-03) p-3 space-y-3">
                                    <div className="flex flex-wrap items-start justify-between gap-3">
                                        <div className="min-w-0">
                                            <div className="text-sm font-medium text-(--base-09)">
                                                {KIND_LABELS[kind] ?? kind}
                                            </div>
                                            <p className="text-xs text-(--base-06) mt-0.5 max-w-xl">{KIND_HINTS[kind]}</p>
                                        </div>
                                        <label className="checkbox-row text-xs text-(--base-07) shrink-0">
                                            <input
                                                type="checkbox"
                                                className="checkbox"
                                                checked={c.set}
                                                onChange={e => update(region, kind, { set: e.target.checked })}
                                            />
                                            Set here
                                        </label>
                                    </div>

                                    {!c.set ? (
                                        // Not a limit of zero, and the copy has to say so: the
                                        // difference between deferring and deciding "no limit"
                                        // is invisible in a number field.
                                        <p className="text-xs text-(--base-06)">
                                            {scope === 'global'
                                                ? 'Nothing decided anywhere for this. Traffic here is not limited.'
                                                : 'Defers to the platform default.'}
                                        </p>
                                    ) : (
                                        <div className="flex flex-wrap items-center gap-6">
                                            <div className="flex items-center gap-3">
                                                <span className="mono-label text-(--base-06) w-28">Included</span>
                                                <LimitField
                                                    value={c.includedGb}
                                                    onChange={v => update(region, kind, { includedGb: v })}
                                                    unit="GB"
                                                />
                                            </div>
                                            <div className="flex items-center gap-3">
                                                <span className="mono-label text-(--base-06) w-28">May buy</span>
                                                <LimitField
                                                    value={c.maxPurchaseGb}
                                                    onChange={v => update(region, kind, { maxPurchaseGb: v })}
                                                    unit="GB"
                                                />
                                            </div>
                                        </div>
                                    )}

                                    <div className="flex items-center justify-between gap-3">
                                        <p className="text-xs text-(--base-06)">
                                            Per unit held. A tenant with a node and a protected address gets it twice.
                                        </p>
                                        <button
                                            type="button"
                                            className="btn btn-secondary btn-sm shrink-0"
                                            disabled={savingKey === k}
                                            onClick={() => save(region, kind)}
                                        >
                                            {savingKey === k ? 'Saving...' : c.set ? 'Save' : 'Clear'}
                                        </button>
                                    </div>
                                </div>
                            );
                        })}
                    </div>
                </SettingsCard>
            ))}

            {overrides.length > 0 && (
                <SettingsCard
                    title="Per-tenant overrides"
                    description="Set on the tenant, shown here so none of them is invisible. An override answers on its own - it does not inherit the half it leaves empty."
                    icon={AlertTriangle}
                >
                    <div className="rounded-md border border-(--base-03) divide-y divide-(--base-03)">
                        {overrides.map(o => (
                            <div key={o.id} className="flex flex-wrap items-center gap-3 p-3 text-sm">
                                <code className="input-mono text-xs text-(--base-08)">{o.scope.slice(5)}</code>
                                <span className="mono-label text-(--accent-light)">{o.region}</span>
                                <span className="mono-label text-(--base-06)">{KIND_LABELS[o.kind] ?? o.kind}</span>
                                <span className="text-(--base-07)">
                                    included {o.includedGb === null ? 'unlimited' : `${o.includedGb} GB`}
                                    {' · '}
                                    may buy {o.maxPurchaseGb === null ? 'unlimited' : `${o.maxPurchaseGb} GB`}
                                </span>
                                <button
                                    type="button"
                                    className="btn btn-secondary btn-sm ml-auto"
                                    onClick={async () => {
                                        const res = await setTrafficLimit({
                                            scope: o.scope,
                                            region: o.region,
                                            kind: o.kind,
                                            includedMode: 'default',
                                            purchaseMode: 'default',
                                        });
                                        if (res.success) { toast('Override removed'); await load(); }
                                        else toast(res.message || 'Could not remove the override.', false);
                                    }}
                                >
                                    <Trash2 size={13} /> Remove
                                </button>
                            </div>
                        ))}
                    </div>
                </SettingsCard>
            )}
        </SettingsPage>
    );
}
