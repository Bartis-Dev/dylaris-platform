"use client";

import React, { useEffect, useState, useCallback } from 'react';
import { getPlans, createPlan, updatePlan, deletePlan, Plan, PlanInput } from '@/lib/api/plans';
import { SkeletonHeader, SkeletonCard } from '@/components/Skeleton';
import { Loader2, Info, Plus, Trash2, Pencil, Star } from 'lucide-react';

const EMPTY: PlanInput = {
    name: '', priceLabel: '', maxNodes: 0, maxLinks: 0, r2QuotaGb: 0,
    trafficEdgeGb: 0, trafficRelayGb: 0, trafficCombinedGb: 0, isDefault: false,
};

const lim = (n: number) => (n === 0 ? '∞' : String(n));

export default function PlansTab() {
    const [plans, setPlans] = useState<Plan[]>([]);
    const [loading, setLoading] = useState(true);
    const [editing, setEditing] = useState<{ id: number | null; data: PlanInput } | null>(null);
    const [saving, setSaving] = useState(false);
    const [toast, setToast] = useState<{ msg: string; ok: boolean } | null>(null);

    const showToast = (msg: string, ok = true) => { setToast({ msg, ok }); setTimeout(() => setToast(null), 3500); };

    const load = useCallback(async () => {
        const res = await getPlans();
        if (res.success) setPlans(res.plans || []);
        setLoading(false);
    }, []);

    useEffect(() => { load(); }, [load]);

    const save = async () => {
        if (!editing) return;
        if (!editing.data.name.trim()) { showToast('Name is required.', false); return; }
        setSaving(true);
        const res = editing.id == null
            ? await createPlan(editing.data)
            : await updatePlan(editing.id, editing.data);
        setSaving(false);
        if (res.success) { setEditing(null); showToast('Plan saved.'); load(); }
        else showToast(res.message || 'Save failed.', false);
    };

    const remove = async (id: number) => {
        const res = await deletePlan(id);
        if (res.success) { showToast('Plan deleted.'); load(); }
        else showToast(res.message || 'Delete failed.', false);
    };

    if (loading) return <div className="space-y-4"><SkeletonHeader /><SkeletonCard /></div>;

    return (
        <div className="space-y-6 max-w-3xl">
            <div>
                <h2 className="font-display text-xl text-(--base-09)">Plans</h2>
                <p className="font-mono text-[11px] uppercase tracking-[0.08em] text-(--base-06) mt-1">BYON tiers &amp; limits</p>
            </div>

            <div className="flex items-start gap-2 rounded-md border border-(--base-04) bg-(--base-01) p-3">
                <Info size={15} className="text-(--base-06) shrink-0 mt-0.5" />
                <p className="text-sm text-(--base-07)">
                    Tiers for BYON tenants. <span className="font-mono">0 = unlimited</span> on every limit. Max nodes and the
                    R2 backup quota are hard-enforced; the monthly traffic limits (edge player + warp/relay data, tracked
                    separately) are warn-only. A tenant&apos;s plan is the baseline; a per-user override (User Management) wins.
                    Only active when the BYON feature is enabled.
                </p>
            </div>

            <div className="space-y-3">
                {plans.length === 0 && <p className="text-sm text-(--base-06)">No plans yet. Add one below.</p>}
                {plans.map(p => (
                    <div key={p.id} className="rounded-md border border-(--base-04) p-4 space-y-2">
                        <div className="flex flex-wrap items-center gap-2">
                            <span className="font-display text-base text-(--base-09)">{p.name}</span>
                            {p.isDefault && <span className="badge badge-accent inline-flex items-center gap-1"><Star size={11} /> default</span>}
                            {p.priceLabel && <span className="font-mono text-xs text-(--base-06)">{p.priceLabel}</span>}
                            <div className="ml-auto flex items-center gap-2">
                                <button onClick={() => setEditing({ id: p.id, data: { ...p } })} className="btn btn-secondary btn-sm inline-flex items-center gap-1"><Pencil size={12} /> Edit</button>
                                <button onClick={() => remove(p.id)} className="btn btn-danger btn-sm" title="Delete plan"><Trash2 size={12} /></button>
                            </div>
                        </div>
                        <div className="grid grid-cols-2 md:grid-cols-3 gap-x-6 gap-y-1 font-mono text-xs text-(--base-07)">
                            <span>nodes: {lim(p.maxNodes)}</span>
                            <span>links: {lim(p.maxLinks)}</span>
                            <span>R2: {lim(p.r2QuotaGb)} GB</span>
                            <span>traffic combined: {lim(p.trafficCombinedGb)} GB</span>
                            <span>traffic edge: {lim(p.trafficEdgeGb)} GB</span>
                            <span>traffic relay: {lim(p.trafficRelayGb)} GB</span>
                        </div>
                    </div>
                ))}
            </div>

            {editing ? (
                <PlanEditor
                    data={editing.data}
                    isNew={editing.id == null}
                    saving={saving}
                    onChange={d => setEditing(e => e && { ...e, data: d })}
                    onSave={save}
                    onCancel={() => setEditing(null)}
                />
            ) : (
                <button onClick={() => setEditing({ id: null, data: { ...EMPTY } })} className="btn btn-primary inline-flex items-center gap-1.5">
                    <Plus size={14} /> Add plan
                </button>
            )}

            {toast && <span className={`block text-sm ${toast.ok ? 'text-(--success-light)' : 'text-(--error)'}`}>{toast.msg}</span>}
        </div>
    );
}

function PlanEditor({ data, isNew, saving, onChange, onSave, onCancel }: {
    data: PlanInput; isNew: boolean; saving: boolean;
    onChange: (d: PlanInput) => void; onSave: () => void; onCancel: () => void;
}) {
    const num = (k: keyof PlanInput) => (e: React.ChangeEvent<HTMLInputElement>) =>
        onChange({ ...data, [k]: Math.max(0, parseInt(e.target.value || '0', 10)) });

    return (
        <div className="rounded-md border border-(--accent)/40 p-4 space-y-4">
            <h3 className="font-display text-base text-(--base-09)">{isNew ? 'New plan' : `Edit ${data.name || 'plan'}`}</h3>
            <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                <div className="space-y-1">
                    <label className="input-label">Name</label>
                    <input className="input-field w-full" value={data.name} onChange={e => onChange({ ...data, name: e.target.value })} placeholder="Home Hoster" />
                </div>
                <div className="space-y-1">
                    <label className="input-label">Price label (free-form)</label>
                    <input className="input-field w-full" value={data.priceLabel} onChange={e => onChange({ ...data, priceLabel: e.target.value })} placeholder="EUR 5/mo (negotiated)" />
                </div>
            </div>
            <div className="grid grid-cols-2 md:grid-cols-3 gap-4">
                <NumField label="Max nodes" value={data.maxNodes} onChange={num('maxNodes')} />
                <NumField label="Max links" value={data.maxLinks} onChange={num('maxLinks')} />
                <NumField label="R2 quota (GB)" value={data.r2QuotaGb} onChange={num('r2QuotaGb')} />
                <NumField label="Traffic combined (GB/mo)" value={data.trafficCombinedGb} onChange={num('trafficCombinedGb')} />
                <NumField label="Traffic edge (GB/mo)" value={data.trafficEdgeGb} onChange={num('trafficEdgeGb')} />
                <NumField label="Traffic relay (GB/mo)" value={data.trafficRelayGb} onChange={num('trafficRelayGb')} />
            </div>
            <p className="text-xs text-(--base-05)">0 = unlimited on every field.</p>
            <div className="flex items-center justify-between">
                <label className="flex items-center gap-2 text-sm text-(--base-08)">
                    <input type="checkbox" checked={data.isDefault} onChange={e => onChange({ ...data, isDefault: e.target.checked })} />
                    Default plan for new tenants
                </label>
                <div className="flex items-center gap-2">
                    <button onClick={onCancel} className="btn btn-secondary btn-sm">Cancel</button>
                    <button onClick={onSave} disabled={saving} className="btn btn-primary btn-sm inline-flex items-center gap-1.5 disabled:opacity-40">
                        {saving && <Loader2 size={13} className="animate-spin" />} Save plan
                    </button>
                </div>
            </div>
        </div>
    );
}

function NumField({ label, value, onChange }: { label: string; value: number; onChange: (e: React.ChangeEvent<HTMLInputElement>) => void }) {
    return (
        <div className="space-y-1">
            <label className="font-mono text-[10px] uppercase tracking-[0.08em] text-(--base-06)">{label}</label>
            <input type="number" min={0} value={value} onChange={onChange} className="input-field input-mono w-full" />
        </div>
    );
}
