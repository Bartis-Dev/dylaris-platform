"use client";

import { useCallback, useEffect, useState } from 'react';
import { Plus } from 'lucide-react';
import type { CatalogScope } from '@/lib/api/authzCatalog';
import type { Server } from '@/lib/api';
import { assignGrant } from '@/lib/api/grants';
import { listServerRoles, type ServerRole } from '@/lib/api/serverRoles';
import CapabilityPicker from '@/components/access/CapabilityPicker';

// Advanced-mode owner UI: assign a friend a server role and/or granular
// grant/deny capability overrides, account-wide or scoped to one owned
// server. The resulting grant shows up in the shared grants list + revoke
// (task 8), rendered by the parent page.

interface AccessAdvancedGrantsProps {
    catalog: CatalogScope[];
    ownedServers: Server[];
    showToast: (msg: string, ok?: boolean) => void;
    onAssigned: () => void;
}

const emptyForm = { username: '', serverId: '', serverRoleId: '', inherit: false };

export default function AccessAdvancedGrants({ catalog, ownedServers, showToast, onAssigned }: AccessAdvancedGrantsProps) {
    const [roles, setRoles] = useState<ServerRole[]>([]);
    const [form, setForm] = useState(emptyForm);
    const [grantCaps, setGrantCaps] = useState<string[]>([]);
    const [denyCaps, setDenyCaps] = useState<string[]>([]);

    const refreshRoles = useCallback(async () => {
        const res = await listServerRoles();
        if (res.success && res.roles) setRoles(res.roles);
    }, []);

    useEffect(() => { refreshRoles(); }, [refreshRoles]);

    const handleAssign = async () => {
        const username = form.username.trim();
        if (!username) { showToast('Username required', false); return; }
        const res = await assignGrant({
            username,
            serverId: form.serverId ? Number(form.serverId) : null,
            serverRoleId: form.serverRoleId ? Number(form.serverRoleId) : null,
            grantCaps,
            denyCaps,
            inherit: form.inherit,
        });
        if (res.success) {
            showToast('Grant created.', true);
            setForm(emptyForm);
            setGrantCaps([]);
            setDenyCaps([]);
            onAssigned();
        } else {
            showToast(res.message || 'Assign failed', false);
        }
    };

    return (
        <div className="card p-6 mb-4">
            <h2 className="text-sm font-medium text-(--base-09) mb-4">Assign grant</h2>
            <div className="space-y-4">
                <div>
                    <label className="input-label">Friend&apos;s username</label>
                    <input
                        type="text"
                        value={form.username}
                        onChange={e => setForm({ ...form, username: e.target.value })}
                        className="input-field w-full"
                        placeholder="friend-username"
                        maxLength={64}
                    />
                </div>

                <div>
                    <label className="input-label">Scope</label>
                    <select
                        value={form.serverId}
                        onChange={e => setForm({ ...form, serverId: e.target.value })}
                        className="input-field w-full"
                    >
                        <option value="">Account-wide (all servers)</option>
                        {ownedServers.map(s => (
                            <option key={s.id} value={s.id}>{s.name}</option>
                        ))}
                    </select>
                </div>

                <div>
                    <label className="input-label">Server role</label>
                    <select
                        value={form.serverRoleId}
                        onChange={e => setForm({ ...form, serverRoleId: e.target.value })}
                        className="input-field w-full"
                    >
                        <option value="">None - custom</option>
                        {roles.map(r => (
                            <option key={r.id} value={r.id}>{r.name}</option>
                        ))}
                    </select>
                </div>

                <div>
                    <label className="input-label">Grant (add on top of the role)</label>
                    <div className="mt-1">
                        <CapabilityPicker
                            catalog={catalog}
                            scopes={['server', 'owner']}
                            selected={grantCaps}
                            onChange={setGrantCaps}
                        />
                    </div>
                </div>

                <div>
                    <label className="input-label">Deny (remove from the role)</label>
                    <div className="mt-1">
                        <CapabilityPicker
                            catalog={catalog}
                            scopes={['server', 'owner']}
                            selected={denyCaps}
                            onChange={setDenyCaps}
                        />
                    </div>
                </div>

                <label className="flex items-center gap-2 text-sm text-(--base-08) cursor-pointer">
                    <input
                        type="checkbox"
                        checked={form.inherit}
                        onChange={e => setForm({ ...form, inherit: e.target.checked })}
                    />
                    Inherit future capability additions to the role
                </label>

                <div>
                    <button onClick={handleAssign} className="btn btn-primary btn-sm">
                        <Plus size={12} />
                        Assign grant
                    </button>
                </div>
            </div>
        </div>
    );
}
