"use client";

import React, { useState, useEffect, useCallback } from 'react';
import { Search, RefreshCw, UserCheck, X, Loader2 } from 'lucide-react';
import { getAdminServers, getUsers, AdminServer, User } from '@/lib/api';
import { StatusBadge } from '@/components/admin/StatusBadge';
import { AssignOwnerModal } from '@/components/admin/AssignOwnerModal';

export default function AdminServersPage() {
    const [servers, setServers] = useState<AdminServer[]>([]);
    const [users, setUsers] = useState<User[]>([]);
    const [search, setSearch] = useState('');
    const [loading, setLoading] = useState(true);
    const [assignTarget, setAssignTarget] = useState<AdminServer | null>(null);
    const [refreshKey, setRefreshKey] = useState(0);

    const refresh = useCallback(() => setRefreshKey(k => k + 1), []);

    useEffect(() => {
        setLoading(true);
        Promise.all([getAdminServers(), getUsers()]).then(([sRes, uRes]) => {
            setServers(sRes.success ? (sRes.servers ?? []) : []);
            setUsers(uRes.success ? (uRes.users ?? []) : []);
            setLoading(false);
        });
    }, [refreshKey]);

    const filteredServers = search
        ? servers.filter(s =>
            s.name.toLowerCase().includes(search.toLowerCase()) ||
            s.uuid.toLowerCase().includes(search.toLowerCase()) ||
            (s.owner ?? '').toLowerCase().includes(search.toLowerCase())
        )
        : servers;

    return (
        <div className="flex flex-col gap-4 h-full">
            <div className="flex items-center gap-3">
                <div className="relative flex-1 max-w-sm">
                    <Search size={13} className="absolute left-3 top-1/2 -translate-y-1/2 text-(--base-05)" />
                    <input
                        type="text"
                        placeholder="Search by name, UUID or owner..."
                        value={search}
                        onChange={e => setSearch(e.target.value)}
                        className="input-field w-full pl-8"
                    />
                </div>
                {search && (
                    <button onClick={() => setSearch('')} className="text-(--base-05) hover:text-(--base-07) transition-colors">
                        <X size={14} />
                    </button>
                )}
                <span className="text-xs text-(--base-06) ml-auto">{filteredServers.length} server{filteredServers.length !== 1 ? 's' : ''}</span>
                <button onClick={refresh} className="btn btn-secondary btn-sm">
                    <RefreshCw size={13} />
                    Refresh
                </button>
            </div>

            <div className="flex-1 overflow-auto">
                {loading ? (
                    <div className="flex items-center justify-center py-16">
                        <Loader2 size={20} className="animate-spin text-(--base-05)" />
                    </div>
                ) : filteredServers.length === 0 ? (
                    <div className="text-center py-16 text-(--base-05) text-sm">No servers found</div>
                ) : (
                    <table className="w-full text-sm">
                        <thead>
                            <tr className="text-left border-b border-(--base-03)">
                                <th className="pb-2 pr-4 mono-label font-normal">Name</th>
                                <th className="pb-2 pr-4 mono-label font-normal hidden md:table-cell">UUID</th>
                                <th className="pb-2 pr-4 mono-label font-normal">Owner</th>
                                <th className="pb-2 pr-4 mono-label font-normal hidden lg:table-cell">Node</th>
                                <th className="pb-2 pr-4 mono-label font-normal">Status</th>
                                <th className="pb-2 mono-label font-normal">Actions</th>
                            </tr>
                        </thead>
                        <tbody className="divide-y divide-(--base-03)">
                            {filteredServers.map(s => (
                                <tr key={s.id} className="hover:bg-(--base-02) transition-colors group">
                                    <td className="py-2.5 pr-4">
                                        <div>
                                            <p className="font-medium text-(--base-09)">{s.name}</p>
                                            {s.activeSubServer && (
                                                <p className="text-[11px] text-(--base-05) font-mono">{s.activeSubServer}</p>
                                            )}
                                        </div>
                                    </td>
                                    <td className="py-2.5 pr-4 hidden md:table-cell">
                                        <span className="font-mono text-[11px] text-(--base-06)">{s.uuid.split('-')[0]}…</span>
                                    </td>
                                    <td className="py-2.5 pr-4">
                                        <span className="text-(--base-07)">{s.owner ?? <span className="text-(--base-04) italic">unassigned</span>}</span>
                                    </td>
                                    <td className="py-2.5 pr-4 hidden lg:table-cell">
                                        <span className="text-(--base-06) text-xs">{s.node}</span>
                                    </td>
                                    <td className="py-2.5 pr-4">
                                        <StatusBadge status={s.status} />
                                    </td>
                                    <td className="py-2.5">
                                        <div className="flex items-center gap-1 opacity-0 group-hover:opacity-100 transition-opacity">
                                            <button
                                                onClick={() => setAssignTarget(s)}
                                                className="flex items-center gap-1 text-[11px] text-(--accent-light) hover:bg-(--accent-ghost) px-2 py-1 rounded transition-colors"
                                                title="Assign owner"
                                            >
                                                <UserCheck size={12} />
                                                Assign
                                            </button>
                                        </div>
                                    </td>
                                </tr>
                            ))}
                        </tbody>
                    </table>
                )}
            </div>

            {assignTarget && (
                <AssignOwnerModal
                    server={assignTarget}
                    users={users}
                    onClose={() => setAssignTarget(null)}
                    onAssigned={refresh}
                />
            )}
        </div>
    );
}
