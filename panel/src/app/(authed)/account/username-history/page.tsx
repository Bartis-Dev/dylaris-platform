"use client";

import React, { useEffect, useState } from 'react';
import { History as HistoryIcon } from 'lucide-react';
import { getMyUsernameHistory, type UsernameHistoryEntry } from '@/lib/api/accountPolicy';
import { SkeletonList } from '@/components/Skeleton';

export default function MyUsernameHistoryPage() {
    const [rows, setRows] = useState<UsernameHistoryEntry[]>([]);
    const [loading, setLoading] = useState(true);
    useEffect(() => { getMyUsernameHistory().then(r => { setRows(r); setLoading(false); }); }, []);

    return (
        <main className="flex-1 overflow-y-auto p-6 max-w-3xl">
            <header className="flex items-center gap-3 mb-4">
                <HistoryIcon size={20} className="text-(--accent-light)" />
                <h1 className="text-base font-display font-semibold text-(--base-09)">Username History</h1>
            </header>
            {loading ? <SkeletonList rows={3} /> : rows.length === 0 ? (
                <p className="text-sm text-(--base-06)">You haven&apos;t renamed your account.</p>
            ) : (
                <div className="space-y-2">
                    {rows.map(r => (
                        <article key={r.id} className="card p-3">
                            <div className="font-mono text-sm text-(--base-09)">{r.oldUsername} → {r.newUsername}</div>
                            <div className="text-xs text-(--base-06) mt-0.5">
                                {new Date(r.changedAt).toLocaleString()} · {r.byAdmin ? 'by admin' : 'self'}
                            </div>
                        </article>
                    ))}
                </div>
            )}
        </main>
    );
}
