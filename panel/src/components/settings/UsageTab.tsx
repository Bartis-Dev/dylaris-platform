"use client";

import React, { useState, useEffect, useCallback } from 'react';
import { getAllUsage, formatBytes, TrafficUsage } from '@/lib/api/usage';
import { SkeletonHeader, SkeletonCard } from '@/components/Skeleton';
import { Activity, FolderDown, HardDrive, Info } from 'lucide-react';

// currentMonth returns YYYY-MM for the <input type="month"> default.
function currentMonth(): string {
    const d = new Date();
    return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}`;
}

export default function UsageTab() {
    const [period, setPeriod] = useState<string>(currentMonth());
    const [rows, setRows] = useState<TrafficUsage[]>([]);
    const [loading, setLoading] = useState(true);
    const [error, setError] = useState<string | null>(null);

    const load = useCallback(async (p: string) => {
        setLoading(true);
        setError(null);
        const res = await getAllUsage(p);
        if (res.success) {
            setRows(res.usage || []);
        } else {
            setError(res.message || 'Failed to load usage.');
            setRows([]);
        }
        setLoading(false);
    }, []);

    useEffect(() => { load(period); }, [period, load]);

    if (loading) {
        return (
            <div className="space-y-4">
                <SkeletonHeader />
                <SkeletonCard />
            </div>
        );
    }

    const totals = rows.reduce(
        (acc, r) => {
            acc.edge += r.edgeBytes;
            acc.relay += r.relayBytes;
            acc.backup += r.backupBytes;
            return acc;
        },
        { edge: 0, relay: 0, backup: 0 },
    );

    return (
        <div className="space-y-6">
            {/* Header + period picker */}
            <div className="flex flex-wrap items-end justify-between gap-4">
                <div>
                    <h2 className="font-display text-xl text-(--base-09)">Traffic & Storage Usage</h2>
                    <p className="font-mono text-[11px] uppercase tracking-[0.08em] text-(--base-06) mt-1">
                        Per-tenant metering (BYON)
                    </p>
                </div>
                <label className="flex flex-col gap-1">
                    <span className="font-mono text-[10px] uppercase tracking-[0.08em] text-(--base-06)">Billing month</span>
                    <input
                        type="month"
                        value={period}
                        onChange={e => setPeriod(e.target.value || currentMonth())}
                        className="input-field w-44"
                    />
                </label>
            </div>

            {/* Context note */}
            <div className="flex items-start gap-2 rounded-md border border-(--base-04) bg-(--base-01) p-3">
                <Info size={15} className="text-(--base-06) shrink-0 mt-0.5" />
                <p className="text-sm text-(--base-07)">
                    Edge is billable player traffic. Relay is filebrowser transfer through the Beam relay,
                    and Backup is R2 storage held right now. Only servers on tenant-owned (BYON) nodes are
                    metered; platform-owned nodes never appear here.
                </p>
            </div>

            {error && (
                <div className="rounded-md border border-(--error) bg-(--error)/10 p-3 text-sm text-(--error)">
                    {error}
                </div>
            )}

            {/* Summary cards */}
            <div className="grid grid-cols-1 sm:grid-cols-3 gap-3">
                <SummaryCard icon={<Activity size={16} />} label="Edge (player)" value={formatBytes(totals.edge)} />
                <SummaryCard icon={<FolderDown size={16} />} label="Relay (files)" value={formatBytes(totals.relay)} />
                <SummaryCard icon={<HardDrive size={16} />} label="Backup (R2)" value={formatBytes(totals.backup)} />
            </div>

            {/* Per-tenant table */}
            {rows.length === 0 ? (
                <div className="rounded-md border border-(--base-04) p-8 text-center text-(--base-06) text-sm">
                    No metered usage for this month.
                </div>
            ) : (
                <div className="overflow-x-auto rounded-md border border-(--base-04)">
                    <table className="w-full text-sm">
                        <thead>
                            <tr className="border-b border-(--base-04) text-left">
                                <Th>Tenant</Th>
                                <Th right>Edge</Th>
                                <Th right>Relay</Th>
                                <Th right>Backup</Th>
                                <Th right>Total</Th>
                            </tr>
                        </thead>
                        <tbody>
                            {rows.map(r => (
                                <tr key={r.userId} className="border-b border-(--base-03) last:border-0 hover:bg-(--base-01)">
                                    <td className="px-4 py-2.5">
                                        <div className="text-(--base-09)">{r.username || '(unknown)'}</div>
                                        <div className="font-mono text-[10px] text-(--base-05)">{r.userId.slice(0, 8)}</div>
                                    </td>
                                    <Td right>{formatBytes(r.edgeBytes)}</Td>
                                    <Td right>{formatBytes(r.relayBytes)}</Td>
                                    <Td right>{formatBytes(r.backupBytes)}</Td>
                                    <Td right strong>{formatBytes(r.edgeBytes + r.relayBytes)}</Td>
                                </tr>
                            ))}
                        </tbody>
                    </table>
                </div>
            )}
        </div>
    );
}

function SummaryCard({ icon, label, value }: { icon: React.ReactNode; label: string; value: string }) {
    return (
        <div className="rounded-md border border-(--base-04) bg-(--base-01) p-4">
            <div className="flex items-center gap-2 text-(--base-06)">
                {icon}
                <span className="font-mono text-[10px] uppercase tracking-[0.08em]">{label}</span>
            </div>
            <div className="mt-2 font-display text-2xl text-(--base-09)">{value}</div>
        </div>
    );
}

function Th({ children, right }: { children: React.ReactNode; right?: boolean }) {
    return (
        <th className={`px-4 py-2.5 font-mono text-[10px] uppercase tracking-[0.08em] text-(--base-06) ${right ? 'text-right' : ''}`}>
            {children}
        </th>
    );
}

function Td({ children, right, strong }: { children: React.ReactNode; right?: boolean; strong?: boolean }) {
    return (
        <td className={`px-4 py-2.5 ${right ? 'text-right font-mono' : ''} ${strong ? 'text-(--base-09)' : 'text-(--base-07)'}`}>
            {children}
        </td>
    );
}
