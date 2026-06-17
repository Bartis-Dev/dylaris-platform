"use client";

import React, { useState, useEffect } from 'react';
import { Layers, Wand2, Grid3x3 } from 'lucide-react';
import { getNodeCpu, type NodeCpuTopology } from '@/lib/api';

interface CpuPinningControlProps {
    nodeId?: number;            // node to fetch topology for; undefined => grid hidden (e.g. tag-targeted create)
    mode: 'shared' | 'auto' | 'manual';
    cpuset: string;             // current manual cpuset string
    cpuLimit?: number;          // used only for an informational hint
    onChange: (next: { mode: 'shared' | 'auto' | 'manual'; cpuset: string }) => void;
    disabled?: boolean;
}

// Parse a Linux cpuset list ("0-3,8") into a sorted, de-duplicated number[].
function parseCpuset(spec: string): number[] {
    const out = new Set<number>();
    for (const part of spec.split(',')) {
        const t = part.trim();
        if (!t) continue;
        const dash = t.indexOf('-');
        if (dash > 0) {
            const lo = Number(t.slice(0, dash));
            const hi = Number(t.slice(dash + 1));
            if (Number.isInteger(lo) && Number.isInteger(hi) && lo <= hi) {
                for (let i = lo; i <= hi; i++) out.add(i);
            }
        } else {
            const n = Number(t);
            if (Number.isInteger(n)) out.add(n);
        }
    }
    return [...out].sort((a, b) => a - b);
}

// Compact a number[] into a Linux cpuset list ("0-3,8"). Inverse of parseCpuset.
function compactCpuset(ids: number[]): string {
    const sorted = [...new Set(ids)].sort((a, b) => a - b);
    const ranges: string[] = [];
    let start = -1;
    let prev = -1;
    for (const id of sorted) {
        if (start === -1) {
            start = id;
            prev = id;
        } else if (id === prev + 1) {
            prev = id;
        } else {
            ranges.push(start === prev ? `${start}` : `${start}-${prev}`);
            start = id;
            prev = id;
        }
    }
    if (start !== -1) ranges.push(start === prev ? `${start}` : `${start}-${prev}`);
    return ranges.join(',');
}

const MODES: { id: 'shared' | 'auto' | 'manual'; label: string; icon: React.ElementType; hint: string }[] = [
    { id: 'shared', label: 'Shared', icon: Layers, hint: "Uses all of the node's cores within the CPU limit." },
    { id: 'auto', label: 'Auto', icon: Wand2, hint: 'Placed automatically across least-loaded cores, preferring performance cores.' },
    { id: 'manual', label: 'Manual', icon: Grid3x3, hint: 'Pick the exact cores this server is pinned to.' },
];

export default function CpuPinningControl({ nodeId, mode, cpuset, cpuLimit, onChange, disabled }: CpuPinningControlProps) {
    const [topology, setTopology] = useState<NodeCpuTopology | null>(null);
    const [load, setLoad] = useState<Record<string, number>>({});
    const [loaded, setLoaded] = useState(false);

    useEffect(() => {
        if (nodeId === undefined) {
            setTopology(null);
            setLoad({});
            setLoaded(false);
            return;
        }
        let cancelled = false;
        setLoaded(false);
        getNodeCpu(nodeId).then(res => {
            if (cancelled) return;
            if (res?.success) {
                setTopology(res.topology ?? null);
                setLoad(res.load ?? {});
            } else {
                setTopology(null);
                setLoad({});
            }
            setLoaded(true);
        }).catch(() => {
            if (cancelled) return;
            setTopology(null);
            setLoad({});
            setLoaded(true);
        });
        return () => { cancelled = true; };
    }, [nodeId]);

    const selected = new Set(parseCpuset(cpuset));

    const toggleCore = (id: number) => {
        if (disabled) return;
        const next = new Set(selected);
        if (next.has(id)) next.delete(id);
        else next.add(id);
        onChange({ mode: 'manual', cpuset: compactCpuset([...next]) });
    };

    const setMode = (next: 'shared' | 'auto' | 'manual') => {
        if (disabled) return;
        if (next === 'shared') onChange({ mode: 'shared', cpuset: '' });
        else if (next === 'auto') onChange({ mode: 'auto', cpuset: '' });
        else onChange({ mode: 'manual', cpuset }); // keep current cpuset
    };

    const activeHint = MODES.find(m => m.id === mode)?.hint ?? '';

    return (
        <div className="flex flex-col gap-2">
            <div className="flex items-center justify-between gap-3">
                <label className="input-label mb-0">CPU pinning</label>
                <div className="flex bg-(--base-03) p-0.5 rounded-md">
                    {MODES.map(({ id, label, icon: Icon }) => (
                        <button
                            key={id}
                            type="button"
                            disabled={disabled}
                            onClick={() => setMode(id)}
                            className={`px-2.5 py-1 text-[11px] rounded-sm transition-colors inline-flex items-center gap-1 disabled:opacity-40 disabled:cursor-not-allowed ${
                                mode === id ? 'bg-(--accent) text-white' : 'text-(--base-07) hover:text-(--base-09)'
                            }`}
                        >
                            <Icon size={11} /> {label}
                        </button>
                    ))}
                </div>
            </div>

            <p className="text-[11px] text-(--base-06)">{activeHint}</p>

            {mode === 'manual' && nodeId !== undefined && (
                <>
                    {topology ? (
                        <div className="flex flex-col gap-2">
                            <div className="flex flex-wrap gap-1.5">
                                {topology.cores.map(core => {
                                    const isSel = selected.has(core.id);
                                    const coreLoad = load[String(core.id)] ?? 0;
                                    return (
                                        <button
                                            key={core.id}
                                            type="button"
                                            disabled={disabled}
                                            onClick={() => toggleCore(core.id)}
                                            title={`Core ${core.id}${topology.hybrid ? ` (${core.type === 'P' ? 'Performance' : core.type === 'E' ? 'Efficiency' : 'standard'})` : ''}${coreLoad > 0 ? ` · ${coreLoad} pinned` : ''}`}
                                            className={`relative w-12 h-12 rounded-md border flex flex-col items-center justify-center transition-colors disabled:opacity-40 disabled:cursor-not-allowed ${
                                                isSel
                                                    ? 'border-(--accent) bg-(--accent-ghost) text-(--accent-light)'
                                                    : 'border-(--base-04) text-(--base-07) hover:border-(--base-05) hover:text-(--base-09)'
                                            }`}
                                        >
                                            <span className="font-mono text-sm leading-none">{core.id}</span>
                                            {topology.hybrid && core.type !== 'standard' && (
                                                <span className={`font-mono text-[9px] leading-none mt-0.5 ${core.type === 'P' ? 'text-(--accent-light)' : 'text-(--base-06)'}`}>
                                                    {core.type}
                                                </span>
                                            )}
                                            {coreLoad > 0 && (
                                                <span className="absolute top-0.5 right-1 font-mono text-[9px] text-(--base-06)" title={`${coreLoad} server(s) pinned here`}>
                                                    x{coreLoad}
                                                </span>
                                            )}
                                        </button>
                                    );
                                })}
                            </div>
                            <p className="text-[11px] text-(--base-06)">
                                {selected.size === 0
                                    ? 'No cores selected. Pick at least one.'
                                    : `${selected.size} core${selected.size === 1 ? '' : 's'} selected: ${compactCpuset([...selected])}`}
                                {cpuLimit !== undefined && cpuLimit > 0 && (
                                    <span className="ml-1">CPU limit is {cpuLimit} core{cpuLimit === 1 ? '' : 's'}.</span>
                                )}
                            </p>
                        </div>
                    ) : loaded ? (
                        <div className="flex flex-col gap-1.5">
                            <p className="text-[11px] text-(--base-06)">This node has not reported its CPU layout yet.</p>
                            <input
                                type="text"
                                value={cpuset}
                                disabled={disabled}
                                onChange={e => onChange({ mode: 'manual', cpuset: e.target.value })}
                                placeholder="e.g. 0-3,8 (Linux cpuset list)"
                                className="input-field input-mono w-full"
                            />
                        </div>
                    ) : (
                        <p className="text-[11px] text-(--base-06)">Loading CPU layout…</p>
                    )}
                </>
            )}

            {mode === 'manual' && nodeId === undefined && (
                <div className="flex flex-col gap-1.5">
                    <p className="text-[11px] text-(--base-06)">No specific node selected. Type a raw cpuset list.</p>
                    <input
                        type="text"
                        value={cpuset}
                        disabled={disabled}
                        onChange={e => onChange({ mode: 'manual', cpuset: e.target.value })}
                        placeholder="e.g. 0-3,8 (Linux cpuset list)"
                        className="input-field input-mono w-full"
                    />
                </div>
            )}
        </div>
    );
}
