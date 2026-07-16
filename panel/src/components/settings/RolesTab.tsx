"use client";

import React, { useCallback, useEffect, useState } from 'react';
import { CircleCheck, CircleAlert, Plus, Pencil, Trash2, X, ShieldCheck, UserCog } from 'lucide-react';
import { getCatalog, getPermissionsMode, type CatalogScope, type PermissionsMode } from '@/lib/api/authzCatalog';
import {
    listPanelRoles,
    createPanelRole,
    updatePanelRole,
    deletePanelRole,
    assignUserPanelRole,
    getUserPanelRole,
    setPermissionsMode,
    type PanelRole,
} from '@/lib/api/panelRoles';
import { getUsers, type User } from '@/lib/api';
import CapabilityPicker from '@/components/access/CapabilityPicker';
import { SkeletonHeader, SkeletonCard } from '@/components/Skeleton';

// Panel-admin Settings tab (F6): (A) the global permissions_mode 3-state
// switch, (B) panel-role CRUD, (C) assigning a panel role + grant/deny
// overrides to a user. Settings/layout.tsx already admin-gates this route,
// so no extra gate is needed here. Every capability list renders from the
// catalog endpoint - no hardcoded permission arrays.

const MODE_OPTIONS: { value: PermissionsMode; label: string }[] = [
    { value: 'off', label: 'Off' },
    { value: 'simple', label: 'Simple' },
    { value: 'advanced', label: 'Advanced' },
];

const MODE_HELP: Record<PermissionsMode, string> = {
    off: 'Owners cannot delegate server access to invited friends at all. Only the owner and panel staff act on a server.',
    simple: 'Owners assign ready-made preset roles to friends. Assign-only; no custom role creation.',
    advanced: 'Owners can create custom server roles and apply granular per-friend capability overrides.',
};

type Toast = { msg: string; ok: boolean } | null;

export default function RolesTab() {
    const [mode, setMode] = useState<PermissionsMode>('off');
    const [modeSaving, setModeSaving] = useState(false);

    const [catalog, setCatalog] = useState<CatalogScope[]>([]);
    const [roles, setRoles] = useState<PanelRole[]>([]);
    const [users, setUsers] = useState<User[]>([]);
    const [loading, setLoading] = useState(true);

    const [roleModal, setRoleModal] = useState<{ role: PanelRole | null } | null>(null);
    const [deletingRole, setDeletingRole] = useState<PanelRole | null>(null);
    const [assignUser, setAssignUser] = useState<User | null>(null);

    const [toast, setToast] = useState<Toast>(null);
    const showToast = useCallback((msg: string, ok = true) => {
        setToast({ msg, ok });
        setTimeout(() => setToast(null), 3500);
    }, []);

    const loadRoles = useCallback(async () => {
        const res = await listPanelRoles();
        if (res.success && res.roles) setRoles(res.roles);
        else if (!res.success) showToast(res.message || 'Failed to load panel roles', false);
    }, [showToast]);

    useEffect(() => {
        (async () => {
            const [modeRes, catalogRes, usersRes] = await Promise.all([
                getPermissionsMode(),
                getCatalog(),
                getUsers(),
            ]);
            if (modeRes.success && modeRes.mode) setMode(modeRes.mode);
            else showToast(modeRes.message || 'Failed to load permissions mode - shown value is unconfirmed', false);
            if (catalogRes.success && catalogRes.catalog) setCatalog(catalogRes.catalog);
            if (usersRes.success && usersRes.users) setUsers(usersRes.users);
            await loadRoles();
            setLoading(false);
        })();
    }, [loadRoles, showToast]);

    const handleSetMode = async (next: PermissionsMode) => {
        if (modeSaving || next === mode) return;
        const prev = mode;
        setMode(next);
        setModeSaving(true);
        const res = await setPermissionsMode(next);
        if (!res.success) {
            setMode(prev);
            showToast(res.message || 'Failed to update permissions mode', false);
        } else {
            showToast('Permissions mode updated');
        }
        setModeSaving(false);
    };

    const handleDeleteRole = async () => {
        if (!deletingRole) return;
        const res = await deletePanelRole(deletingRole.id);
        setDeletingRole(null);
        if (res.success) {
            showToast('Panel role deleted');
            loadRoles();
        } else {
            showToast(res.message || 'Failed to delete panel role', false);
        }
    };

    if (loading) {
        return (
            <div className="max-w-3xl space-y-6">
                <SkeletonHeader />
                <SkeletonCard height="h-28" />
                <SkeletonCard height="h-56" />
                <SkeletonCard height="h-56" />
            </div>
        );
    }

    return (
        <div className="max-w-3xl space-y-8">
            {/* Section A - permissions_mode */}
            <section>
                <div className="mb-3">
                    <h2 className="text-base font-display font-bold text-(--base-09) mb-1">Permissions Mode</h2>
                    <p className="text-sm text-(--base-07)">Controls whether and how server owners can delegate access to invited friends on their Access page. Panel roles and per-user overrides below are unaffected and always enforced.</p>
                </div>
                <div className="card p-5 space-y-4">
                    <div className="flex items-center gap-2">
                        {MODE_OPTIONS.map(opt => (
                            <button
                                key={opt.value}
                                type="button"
                                disabled={modeSaving}
                                onClick={() => handleSetMode(opt.value)}
                                className={`btn btn-sm ${
                                    opt.value === mode
                                        ? 'bg-(--accent-ghost) text-(--accent-light) border border-(--accent-border)'
                                        : 'btn-secondary'
                                } disabled:opacity-40`}
                            >
                                {opt.label}
                            </button>
                        ))}
                    </div>
                    <p className="text-xs text-(--base-06)">{MODE_HELP[mode]}</p>
                </div>
            </section>

            {/* Section B - panel roles CRUD */}
            <section>
                <div className="flex items-center justify-between mb-3">
                    <div>
                        <h2 className="text-base font-display font-bold text-(--base-09) mb-1">Panel Roles</h2>
                        <p className="text-sm text-(--base-07)">Capability bundles for panel staff. Always available to admins, independent of the permissions mode above.</p>
                    </div>
                    <button type="button" onClick={() => setRoleModal({ role: null })} className="btn btn-primary btn-sm shrink-0">
                        <Plus size={13} />
                        New Role
                    </button>
                </div>

                {roles.length === 0 ? (
                    <div className="card p-6 flex flex-col items-center text-center gap-2">
                        <ShieldCheck size={24} className="text-(--base-05)" />
                        <p className="text-sm text-(--base-07)">No panel roles yet.</p>
                    </div>
                ) : (
                    <div className="space-y-2">
                        {roles.map(role => (
                            <div key={role.id} className="card p-3.5 flex items-center gap-3">
                                <div className="min-w-0 flex-1">
                                    <div className="flex items-center gap-2">
                                        <span className="font-medium text-sm text-(--base-09)">{role.name}</span>
                                        {role.isSystem && <span className="badge badge-neutral">system</span>}
                                    </div>
                                    <span className="mono-label">{role.capabilities.length} capabilities</span>
                                </div>
                                {!role.isSystem && (
                                    <div className="flex items-center gap-2 shrink-0">
                                        <button
                                            type="button"
                                            onClick={() => setRoleModal({ role })}
                                            className="btn btn-secondary btn-sm"
                                            title="Edit role"
                                        >
                                            <Pencil size={12} />
                                            Edit
                                        </button>
                                        <button
                                            type="button"
                                            onClick={() => setDeletingRole(role)}
                                            className="btn btn-danger btn-sm"
                                            title="Delete role"
                                        >
                                            <Trash2 size={12} />
                                            Delete
                                        </button>
                                    </div>
                                )}
                            </div>
                        ))}
                    </div>
                )}
            </section>

            {/* Section C - assign a panel role to a user */}
            <section>
                <div className="mb-3">
                    <h2 className="text-base font-display font-bold text-(--base-09) mb-1">User Assignment</h2>
                    <p className="text-sm text-(--base-07)">Assign a panel role, plus optional per-user grant/deny overrides, to a user.</p>
                </div>
                <div className="table-wrapper">
                    <table className="w-full">
                        <thead>
                            <tr>
                                <th className="table-th text-left">Username</th>
                                <th className="table-th text-left">Role</th>
                                <th className="table-th text-right">Actions</th>
                            </tr>
                        </thead>
                        <tbody>
                            {users.map(u => (
                                <tr key={u.id} className="table-tr table-tr-hover">
                                    <td className="table-td font-mono font-medium text-(--base-09)">{u.username}</td>
                                    <td className="table-td">
                                        <span className={`badge ${u.isAdmin ? 'badge-accent' : 'badge-neutral'}`}>
                                            {(u.role || (u.isAdmin ? 'admin' : 'user')).toUpperCase()}
                                        </span>
                                    </td>
                                    <td className="table-td text-right">
                                        <button
                                            type="button"
                                            onClick={() => setAssignUser(u)}
                                            className="btn px-2.5 py-1 text-xs bg-(--base-03) border border-(--base-04) text-(--base-07) hover:text-(--base-09) transition-colors"
                                            title="Assign panel role"
                                        >
                                            <UserCog size={13} />
                                        </button>
                                    </td>
                                </tr>
                            ))}
                        </tbody>
                    </table>
                </div>
            </section>

            {/* Create/Edit role modal */}
            {roleModal && (
                <RoleModal
                    role={roleModal.role}
                    catalog={catalog}
                    onClose={() => setRoleModal(null)}
                    onSaved={() => { setRoleModal(null); loadRoles(); showToast(roleModal.role ? 'Panel role updated' : 'Panel role created'); }}
                    onError={msg => showToast(msg, false)}
                />
            )}

            {/* Delete role confirm */}
            {deletingRole && (
                <div className="modal-overlay animate-fade-in" onClick={() => setDeletingRole(null)}>
                    <div className="modal-panel w-full max-w-sm" onClick={e => e.stopPropagation()}>
                        <div className="modal-header">
                            <h3 className="modal-title text-(--error-light)">Delete &quot;{deletingRole.name}&quot;?</h3>
                        </div>
                        <div className="modal-body">
                            <p className="text-sm text-(--base-07)">Users assigned this role fall back to no panel role. This cannot be undone.</p>
                        </div>
                        <div className="modal-footer">
                            <button type="button" onClick={() => setDeletingRole(null)} className="btn btn-secondary">Cancel</button>
                            <button type="button" onClick={handleDeleteRole} className="btn btn-danger">Delete</button>
                        </div>
                    </div>
                </div>
            )}

            {/* Assign panel role to user */}
            {assignUser && (
                <AssignRoleModal
                    user={assignUser}
                    roles={roles}
                    catalog={catalog}
                    onClose={() => setAssignUser(null)}
                    onSaved={() => { setAssignUser(null); showToast('User panel role updated'); }}
                    onError={msg => showToast(msg, false)}
                />
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
        </div>
    );
}

// ---------------------------------------------
// Create/Edit panel-role modal
// ---------------------------------------------
function RoleModal({
    role,
    catalog,
    onClose,
    onSaved,
    onError,
}: {
    role: PanelRole | null;
    catalog: CatalogScope[];
    onClose: () => void;
    onSaved: () => void;
    onError: (msg: string) => void;
}) {
    const [name, setName] = useState(role?.name ?? '');
    const [caps, setCaps] = useState<string[]>(role?.capabilities ?? []);
    const [saving, setSaving] = useState(false);

    const handleSave = async () => {
        const trimmed = name.trim();
        if (!trimmed) { onError('Role name is required'); return; }
        setSaving(true);
        const res = role
            ? await updatePanelRole(role.id, trimmed, caps)
            : await createPanelRole(trimmed, caps);
        setSaving(false);
        if (res.success) {
            onSaved();
        } else {
            onError(res.message || 'Failed to save panel role');
        }
    };

    return (
        <div className="modal-overlay animate-fade-in" onClick={onClose}>
            <div className="modal-panel w-full max-w-lg max-h-[85vh] flex flex-col" onClick={e => e.stopPropagation()}>
                <div className="modal-header flex items-center justify-between">
                    <h3 className="modal-title">{role ? 'Edit Panel Role' : 'New Panel Role'}</h3>
                    <button onClick={onClose} className="p-1 rounded hover:bg-(--base-03) text-(--base-06)">
                        <X size={16} />
                    </button>
                </div>
                <div className="modal-body overflow-y-auto flex-1 space-y-4">
                    <div className="flex flex-col gap-[5px]">
                        <label className="input-label">Name</label>
                        <input
                            type="text"
                            value={name}
                            onChange={e => setName(e.target.value)}
                            maxLength={64}
                            className="input-field w-full"
                            disabled={saving}
                        />
                    </div>
                    <div>
                        <label className="input-label">Capabilities</label>
                        <div className="mt-2">
                            <CapabilityPicker catalog={catalog} scopes={['panel']} selected={caps} onChange={setCaps} disabled={saving} />
                        </div>
                    </div>
                </div>
                <div className="modal-footer">
                    <button type="button" onClick={onClose} className="btn btn-secondary" disabled={saving}>Cancel</button>
                    <button type="button" onClick={handleSave} className="btn btn-primary disabled:opacity-40" disabled={saving}>
                        {saving ? 'Saving...' : 'Save'}
                    </button>
                </div>
            </div>
        </div>
    );
}

// ---------------------------------------------
// Assign panel role + grant/deny overrides to a user
// ---------------------------------------------
function AssignRoleModal({
    user,
    roles,
    catalog,
    onClose,
    onSaved,
    onError,
}: {
    user: User;
    roles: PanelRole[];
    catalog: CatalogScope[];
    onClose: () => void;
    onSaved: () => void;
    onError: (msg: string) => void;
}) {
    const [loading, setLoading] = useState(true);
    const [panelRoleId, setPanelRoleId] = useState<number | null>(null);
    const [grantCaps, setGrantCaps] = useState<string[]>([]);
    const [denyCaps, setDenyCaps] = useState<string[]>([]);
    const [saving, setSaving] = useState(false);

    useEffect(() => {
        getUserPanelRole(user.id).then(res => {
            if (res.success) {
                setPanelRoleId(res.panelRoleId ?? null);
                setGrantCaps(res.grantCaps ?? []);
                setDenyCaps(res.denyCaps ?? []);
            }
            setLoading(false);
        });
    }, [user.id]);

    const handleSave = async () => {
        setSaving(true);
        const res = await assignUserPanelRole(user.id, panelRoleId, grantCaps, denyCaps);
        setSaving(false);
        if (res.success) {
            onSaved();
        } else {
            onError(res.message || 'Failed to assign panel role');
        }
    };

    return (
        <div className="modal-overlay animate-fade-in" onClick={onClose}>
            <div className="modal-panel w-full max-w-lg max-h-[85vh] flex flex-col" onClick={e => e.stopPropagation()}>
                <div className="modal-header flex items-center justify-between">
                    <div>
                        <h3 className="modal-title">Assign Panel Role</h3>
                        <p className="text-xs font-mono text-(--base-06)">{user.username}</p>
                    </div>
                    <button onClick={onClose} className="p-1 rounded hover:bg-(--base-03) text-(--base-06)">
                        <X size={16} />
                    </button>
                </div>
                {loading ? (
                    <div className="modal-body">
                        <SkeletonCard height="h-24" />
                    </div>
                ) : (
                    <div className="modal-body overflow-y-auto flex-1 space-y-5">
                        <div className="flex flex-col gap-[5px]">
                            <label className="input-label">Panel role</label>
                            <select
                                value={panelRoleId ?? ''}
                                onChange={e => setPanelRoleId(e.target.value === '' ? null : Number(e.target.value))}
                                className="input-field w-full"
                                disabled={saving}
                            >
                                <option value="">None</option>
                                {roles.map(r => (
                                    <option key={r.id} value={r.id}>{r.name}</option>
                                ))}
                            </select>
                        </div>
                        <div>
                            <label className="input-label">Grant overrides</label>
                            <p className="text-xs text-(--base-06) mb-2">Additional capabilities on top of the role, or on top of nothing if no role is assigned.</p>
                            <CapabilityPicker catalog={catalog} scopes={['panel']} selected={grantCaps} onChange={setGrantCaps} disabled={saving} />
                        </div>
                        <div>
                            <label className="input-label">Deny overrides</label>
                            <p className="text-xs text-(--base-06) mb-2">Capabilities removed even if the role or a grant above would include them.</p>
                            <CapabilityPicker catalog={catalog} scopes={['panel']} selected={denyCaps} onChange={setDenyCaps} disabled={saving} />
                        </div>
                    </div>
                )}
                <div className="modal-footer">
                    <button type="button" onClick={onClose} className="btn btn-secondary" disabled={saving}>Cancel</button>
                    <button type="button" onClick={handleSave} className="btn btn-primary disabled:opacity-40" disabled={saving || loading}>
                        {saving ? 'Saving...' : 'Save'}
                    </button>
                </div>
            </div>
        </div>
    );
}
