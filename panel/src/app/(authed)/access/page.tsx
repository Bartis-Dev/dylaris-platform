"use client";

import React, { useCallback, useEffect, useState } from 'react';
import { ShieldCheck, Plus, Trash2, CircleCheck, CircleAlert } from 'lucide-react';
import { useAppData } from '@/lib/AppDataContext';
import {
    getPermissionsMode, getCatalog, getPresets,
    type PermissionsMode, type CatalogScope, type Preset,
} from '@/lib/api/authzCatalog';
import { listGrants, assignGrant, revokeGrant, type Grant } from '@/lib/api/grants';
import { SkeletonList } from '@/components/Skeleton';

// Owner-facing delegation UI. Behavior branches on the account's
// permissions_mode:
//   off      - delegation is disabled platform-wide; informational only.
//   simple   - assign-only preset grants (this task).
//   advanced - per-server custom roles + per-capability grants (tasks 9-10;
//              stubbed here so the mode switch compiles end-to-end today).
// The grants list + revoke is shared across simple and advanced.

export default function AccessPage() {
    const { servers } = useAppData();
    const ownedServers = servers.filter(s => s.role === 'owner');

    const [mode, setMode] = useState<PermissionsMode | null>(null);
    const [catalog, setCatalog] = useState<CatalogScope[]>([]);
    const [presets, setPresets] = useState<Preset[]>([]);

    const [grants, setGrants] = useState<Grant[]>([]);
    const [grantsLoading, setGrantsLoading] = useState(true);

    const [form, setForm] = useState({ username: '', serverId: '', presetId: '', inherit: false });
    const [toast, setToast] = useState<{ msg: string; ok: boolean } | null>(null);

    const showToast = useCallback((msg: string, ok = true) => {
        setToast({ msg, ok });
        setTimeout(() => setToast(null), 3500);
    }, []);

    const refreshGrants = useCallback(async () => {
        const res = await listGrants();
        if (res.success && res.grants) setGrants(res.grants);
        setGrantsLoading(false);
    }, []);

    useEffect(() => {
        (async () => {
            const modeRes = await getPermissionsMode();
            // Fail closed: if the mode can't be read, treat delegation as
            // disabled rather than getting stuck on the loading skeleton.
            setMode(modeRes.success && modeRes.mode ? modeRes.mode : 'off');
        })();
        (async () => {
            const res = await getCatalog();
            if (res.success && res.catalog) setCatalog(res.catalog);
        })();
        (async () => {
            const res = await getPresets();
            if (res.success && res.presets) setPresets(res.presets);
        })();
        refreshGrants();
    }, [refreshGrants]);

    const handleAssign = async () => {
        const username = form.username.trim();
        if (!username) { showToast('Username required', false); return; }
        const preset = presets.find(p => p.id === form.presetId);
        if (!preset) { showToast('Pick a preset', false); return; }
        const res = await assignGrant({
            username,
            serverId: form.serverId ? Number(form.serverId) : null,
            serverRoleId: null,
            grantCaps: preset.capabilities,
            denyCaps: [],
            inherit: form.inherit,
        });
        if (res.success) {
            showToast('Grant created.', true);
            setForm({ username: '', serverId: '', presetId: '', inherit: false });
            refreshGrants();
        } else {
            showToast(res.message || 'Assign failed', false);
        }
    };

    const handleRevoke = async (g: Grant) => {
        const res = await revokeGrant(g.username, g.serverId);
        if (res.success) {
            showToast('Grant revoked.', true);
            refreshGrants();
        } else {
            showToast(res.message || 'Revoke failed', false);
        }
    };

    if (mode === null) {
        return (
            <main className="flex-1 overflow-y-auto p-6 max-w-4xl">
                <h1 className="h-page mb-6">Access</h1>
                <SkeletonList rows={4} />
            </main>
        );
    }

    return (
        <main className="flex-1 overflow-y-auto p-6 max-w-4xl">
            <h1 className="h-page mb-6">Access</h1>

            {mode === 'off' && (
                <div className="card p-8 flex flex-col items-center text-center gap-2 mb-4">
                    <ShieldCheck size={28} className="text-(--base-05)" />
                    <p className="text-sm text-(--base-07)">
                        Delegation is disabled. Ask an admin to enable it in Settings -&gt; Roles.
                    </p>
                </div>
            )}

            {mode === 'simple' && (
                <div className="card p-6 mb-4">
                    <h2 className="text-sm font-medium text-(--base-09) mb-4">New grant</h2>
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
                            <label className="input-label">Preset</label>
                            <select
                                value={form.presetId}
                                onChange={e => setForm({ ...form, presetId: e.target.value })}
                                className="input-field w-full"
                            >
                                <option value="">Select a preset...</option>
                                {presets.map(p => (
                                    <option key={p.id} value={p.id}>{p.label}</option>
                                ))}
                            </select>
                        </div>

                        <label className="flex items-center gap-2 text-sm text-(--base-08) cursor-pointer">
                            <input
                                type="checkbox"
                                checked={form.inherit}
                                onChange={e => setForm({ ...form, inherit: e.target.checked })}
                            />
                            Inherit future capability additions to this preset
                        </label>

                        <div>
                            <button onClick={handleAssign} className="btn btn-primary btn-sm">
                                <Plus size={12} />
                                Add grant
                            </button>
                        </div>
                    </div>
                </div>
            )}

            {mode === 'advanced' && (
                <>
                    {/* TODO(task 9): mount <AccessServerRoles/> here - the
                        custom per-server role manager (create/edit/delete
                        roles built from the capability catalog). */}
                    <section className="card p-6 mb-4">
                        <h2 className="text-sm font-medium text-(--base-09) mb-2">Server roles</h2>
                        <p className="text-xs text-(--base-06)">
                            Custom server role management is not built yet (task 9).
                            {catalog.length > 0 && ` ${catalog.length} capability scope(s) available once wired up.`}
                        </p>
                    </section>

                    {/* TODO(task 10): mount <AccessAdvancedGrants/> here - the
                        advanced grant/deny editor with per-capability
                        overrides via CapabilityPicker, on top of the shared
                        list below. */}
                    <section className="card p-6 mb-4">
                        <h2 className="text-sm font-medium text-(--base-09) mb-2">Advanced grants</h2>
                        <p className="text-xs text-(--base-06)">
                            Per-capability grant and deny editing is not built yet (task 10).
                        </p>
                    </section>
                </>
            )}

            {mode !== 'off' && (
                <>
                    <h2 className="text-sm font-medium text-(--base-09) mb-2 mt-2">Grants</h2>
                    {grantsLoading ? (
                        <SkeletonList rows={3} />
                    ) : grants.length === 0 ? (
                        <div className="card p-8 flex flex-col items-center text-center gap-2">
                            <ShieldCheck size={28} className="text-(--base-05)" />
                            <p className="text-sm text-(--base-07)">No grants yet.</p>
                        </div>
                    ) : (
                        <div className="space-y-2">
                            {grants.map(g => (
                                <article key={`${g.username}:${g.serverId ?? 'account'}`} className="card p-3 flex items-center gap-3">
                                    <div className="min-w-0 flex-1">
                                        <div className="flex items-center gap-2 flex-wrap">
                                            <span className="font-medium text-sm text-(--base-09)">{g.username}</span>
                                            {g.accountWide ? (
                                                <span className="mono-label bg-(--base-03) text-(--base-07) px-1.5 rounded-sm">Account-wide</span>
                                            ) : (
                                                <span className="mono-label bg-(--base-03) text-(--base-07) px-1.5 rounded-sm">{g.serverName}</span>
                                            )}
                                        </div>
                                        <div className="mt-1 text-xs text-(--base-06)">
                                            <span className="text-(--base-07)">Role:</span> {g.serverRoleName || 'custom'}
                                        </div>
                                    </div>
                                    <button onClick={() => handleRevoke(g)} className="btn btn-secondary btn-sm shrink-0">
                                        <Trash2 size={12} className="text-(--error)" />
                                        Revoke
                                    </button>
                                </article>
                            ))}
                        </div>
                    )}
                </>
            )}

            {toast && (
                <div className="toast-container">
                    <div className="toast">
                        <div className={`toast-bar ${toast.ok ? 'bg-(--success-light)' : 'bg-(--error-light)'}`}></div>
                        {toast.ok ? <CircleCheck size={14} /> : <CircleAlert size={14} />}
                        <span className="text-sm text-(--base-09)">{toast.msg}</span>
                    </div>
                </div>
            )}
        </main>
    );
}
