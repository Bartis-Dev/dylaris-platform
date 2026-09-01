"use client";

import React, { useState, useEffect } from 'react';
import { getUsers, createUser, deleteUser, resetUserPassword, getUserRouteLimit, setUserRouteLimit, cancelUserDeletion, setUserRole, setUserPermissions, User } from '@/lib/api';
import { adminResetTOTP } from '@/lib/api/auth';
import { getUserRegions, setUserRegions } from '@/lib/api/regions';
import { setUserModpackFlag, clearUserModpackOverride } from '@/lib/api/modpackSettings';
import {
    getUsernameHistory,
    type UsernameHistoryEntry,
} from '@/lib/api/accountPolicy';
import {
    getUserBilling,
    setUserBillingStatus,
    setUserBillingOverrides,
    type BillingStatus,
    type UserBillingAdmin,
} from '@/lib/api/billing';
import { setUserLimitOverrides } from '@/lib/api/plans';
import { getUserEntitlement, grantEntitlement, revokeEntitlement, type Entitlement, type EntitlementResponse } from '@/lib/api/entitlement';
import { entitlementOf, formatDaysLeft } from '@/lib/entitlementState';
import { entitlementExplanation } from '@/lib/entitlementText';
import UserRegionPicker from '@/components/admin/UserRegionPicker';
import { UserPlus, Settings, X, CircleCheck, CircleAlert, ShieldOff, Trash2, ShieldAlert, History as HistoryIcon, Package, CreditCard } from 'lucide-react';
import { SkeletonText } from '@/components/Skeleton';
import { confirmDialog } from '@/components/ui/ConfirmDialog';
import HelpTip from '@/components/ui/HelpTip';

interface UsersTabProps {
    currentUser?: User;
}

// Four states, and they are genuinely four. "default" decides nothing and lets
// the platform answer; "unlimited" decides "no cap, for this user" and keeps
// saying so if the platform default is later tightened. The backend stores the
// first as no row and the second as a row with a NULL cap.
type RouteMode = 'default' | 'unlimited' | 'custom' | 'disabled';

type UserSort = 'role' | 'name' | 'created';

// Role order for the default sort. Admins first: the question this list is
// usually opened to answer is "who can do what here", and that answer was
// previously buried in creation order.
const ROLE_RANK: Record<string, number> = { admin: 0, support: 1, user: 2 };

function roleOf(u: User): string {
    return u.role || (u.isAdmin ? 'admin' : 'user');
}

/**
 * Sorted copy of the list. Every mode falls back to the username so the order
 * is total - without it, two accounts created in the same second (a seeded
 * install) swap places between renders.
 */
export function sortUsers(users: User[], sort: UserSort): User[] {
    const byName = (a: User, b: User) => a.username.localeCompare(b.username);
    return [...users].sort((a, b) => {
        if (sort === 'name') return byName(a, b);
        if (sort === 'created') {
            const ta = a.createdAt ? new Date(a.createdAt).getTime() : 0;
            const tb = b.createdAt ? new Date(b.createdAt).getTime() : 0;
            return tb - ta || byName(a, b);
        }
        const ra = ROLE_RANK[roleOf(a)] ?? 99;
        const rb = ROLE_RANK[roleOf(b)] ?? 99;
        return ra - rb || byName(a, b);
    });
}

export default function UsersTab({ currentUser }: UsersTabProps) {
    const [users, setUsers] = useState<User[]>([]);
    const [isModalOpen, setIsModalOpen] = useState(false);
    const [error, setError] = useState("");
    const [userForm, setUserForm] = useState<Partial<User>>({ username: "", password: "", isAdmin: false });
    const [sort, setSort] = useState<UserSort>('role');
    // Drives the last-admin tooltip. Core refuses the delete either way; this
    // only decides whether the button is offered at all.
    const adminCount = users.filter(u => u.isAdmin).length;

    // Settings modal
    const [settingsUser, setSettingsUser] = useState<User | null>(null);
    const [settingsTab, setSettingsTab] = useState<'general' | 'gateway'>('general');
    const [newPassword, setNewPassword] = useState('');
    const [pwSaving, setPwSaving] = useState(false);
    const [routeMode, setRouteMode] = useState<RouteMode>('default');
    const [routeMax, setRouteMax] = useState(5);
    const [routeSaving, setRouteSaving] = useState(false);
    const [toast, setToast] = useState<{ msg: string; ok: boolean } | null>(null);
    const [resetting2FA, setResetting2FA] = useState(false);

    // Region access — create form (defaults to all-regions for new users)
    const [createAllRegions, setCreateAllRegions] = useState(true);
    const [createRegions, setCreateRegions] = useState<string[]>([]);

    // Region access — edit modal
    const [editAllRegions, setEditAllRegions] = useState(true);
    const [editRegions, setEditRegions] = useState<string[]>([]);
    const [editRegionsSaving, setEditRegionsSaving] = useState(false);

    // Deletion-rescue state
    const [cancellingDeletion, setCancellingDeletion] = useState(false);

    // Username-history modal
    const [historyUser, setHistoryUser] = useState<{ id: string; username: string } | null>(null);

    // Billing override modal (BYON lifecycle: status + per-user retention overrides)
    const [billingUser, setBillingUser] = useState<{ id: string; username: string } | null>(null);

    // Role + permissions — edit modal
    const [editRole, setEditRole] = useState<'user' | 'support' | 'admin'>('user');
    const [editCanDeleteServers, setEditCanDeleteServers] = useState(false);
    const [editCanChangeResources, setEditCanChangeResources] = useState(false);
    const [editSupportTeam, setEditSupportTeam] = useState('');
    const [editRolePermsSaving, setEditRolePermsSaving] = useState(false);

    // Per-user modpack-authoring flag. Drives the
    // Modpacks UI gate + 503 from /api/me/modpacks for non-admins. Default
    // true so accounts older than the column flip aren't surprised off.
    const [editCanCreateModpacks, setEditCanCreateModpacks] = useState(true);
    const [modpackFlagSaving, setModpackFlagSaving] = useState(false);

    const handleSaveRoleAndPermissions = async () => {
        if (!settingsUser) return;
        setEditRolePermsSaving(true);
        // Two-step save: role first (it also flips is_admin), then flags.
        // Failure of either is surfaced; both completing means the modal
        // local copy gets updated.
        const r1 = await setUserRole(settingsUser.id, editRole);
        if (!r1.success) {
            setEditRolePermsSaving(false);
            showToast(r1.message || 'Failed to set role', false);
            return;
        }
        const r2 = await setUserPermissions(settingsUser.id, {
            canDeleteServers: editCanDeleteServers,
            canChangeResources: editCanChangeResources,
            supportTeam: editSupportTeam,
        });
        setEditRolePermsSaving(false);
        if (!r2.success) {
            showToast(r2.message || 'Role saved, but permissions failed', false);
            return;
        }
        showToast('Role and permissions updated');
        setSettingsUser({
            ...settingsUser,
            role: editRole,
            isAdmin: editRole === 'admin',
            canDeleteServers: editCanDeleteServers,
            canChangeResources: editCanChangeResources,
            supportTeam: editSupportTeam,
        });
        loadUsers();
    };

    const handleSaveModpackFlag = async () => {
        if (!settingsUser) return;
        setModpackFlagSaving(true);
        const res = await setUserModpackFlag(settingsUser.id, editCanCreateModpacks);
        setModpackFlagSaving(false);
        if (!res.success) {
            showToast(res.message || 'Save failed', false);
            return;
        }
        showToast('Modpack flag updated');
        // Writing the flag by hand marks the row manual server-side, so reflect
        // that here too rather than waiting for the reload - otherwise the
        // "follows the platform switch" hint below stays wrong for a moment.
        setSettingsUser({ ...settingsUser, canCreateModpacks: editCanCreateModpacks, canCreateModpacksManual: true });
        loadUsers();
    };

    // Hand the user back to the platform switch without changing what they may do
    // right now. The blunt alternative is the Features screen's "also apply to
    // users I set by hand", which resets every overridden user at once.
    const handleClearModpackOverride = async () => {
        if (!settingsUser) return;
        setModpackFlagSaving(true);
        const res = await clearUserModpackOverride(settingsUser.id);
        setModpackFlagSaving(false);
        if (!res.success) {
            showToast(res.message || 'Failed to clear the override', false);
            return;
        }
        showToast('This user follows the platform setting again');
        setSettingsUser({ ...settingsUser, canCreateModpacksManual: false });
        loadUsers();
    };

    const handleCancelDeletion = async () => {
        if (!settingsUser) return;
        setCancellingDeletion(true);
        const res = await cancelUserDeletion(settingsUser.id);
        setCancellingDeletion(false);
        if (res.success) {
            showToast('Deletion cancelled');
            // Update local user copy + refresh the list so the badge clears.
            setSettingsUser({ ...settingsUser, deletionStatus: 'active', deletionScheduledAt: undefined });
            loadUsers();
        } else {
            showToast(res.message || 'Cancel failed', false);
        }
    };

    const showToast = (msg: string, ok = true) => {
        setToast({ msg, ok });
        setTimeout(() => setToast(null), 3500);
    };

    useEffect(() => {
        loadUsers();
    }, []);

    const loadUsers = async () => {
        const res = await getUsers();
        if (res.success) setUsers(res.users);
    };

    const handleCreateUser = async (e: React.FormEvent) => {
        e.preventDefault();
        const res = await createUser({
            ...userForm,
            allRegions: createAllRegions,
            regionsExplicit: createAllRegions ? [] : createRegions,
        });
        if (res.success) {
            setIsModalOpen(false);
            // Reset region state for the next open.
            setCreateAllRegions(true);
            setCreateRegions([]);
            loadUsers();
        } else setError(res.message || "Error creating user");
    };

    const handleSaveRegions = async () => {
        if (!settingsUser) return;
        setEditRegionsSaving(true);
        const res = await setUserRegions(settingsUser.id, {
            allRegions: editAllRegions,
            regions: editAllRegions ? [] : editRegions,
        });
        if (res.success) {
            showToast('Region access updated');
        } else {
            showToast(res.message || 'Failed to update regions', false);
        }
        setEditRegionsSaving(false);
    };

    const handleDeleteUser = async (id: string) => {
        if (!(await confirmDialog({ title: 'Delete user', message: "Do you really want to delete this user?" }))) return;
        // Core refuses this for real reasons (the last admin, a user still
        // holding servers). Discarding the answer reloaded the list with the
        // user still in it and no word about why, which reads as a bug in the
        // list rather than a refusal. Same handling as handleUpdateRegions above.
        const res = await deleteUser(id);
        if (!res.success) {
            showToast(res.message || res.error || 'Could not delete the user.', false);
            return;
        }
        showToast('User deleted');
        loadUsers();
    };

    const openSettings = async (user: User) => {
        setSettingsUser(user);
        setSettingsTab('general');
        setNewPassword('');

        // Load route limit
        try {
            const res = await getUserRouteLimit(user.id);
            if (res.success) {
                setRouteMode(res.mode as RouteMode);
                // Only a positive cap is a "custom" number worth restoring into
                // the input. null (no cap) and 0 (none) are their own modes and
                // must not seed the field with a value they do not mean.
                setRouteMax(typeof res.maxRoutes === 'number' && res.maxRoutes > 0 ? res.maxRoutes : 5);
            }
        } catch {
            setRouteMode('default');
            setRouteMax(5);
        }

        // Load region access — keep optimistic defaults until the call resolves.
        setEditAllRegions(true);
        setEditRegions([]);
        try {
            const res = await getUserRegions(user.id);
            if (res.success) {
                setEditAllRegions(!!res.allRegions);
                setEditRegions(res.regions || []);
            }
        } catch {
            /* keep optimistic defaults */
        }

        // Role + capability flags. User payload already carries these
        // from the list endpoint — no extra request needed.
        setEditRole((user.role === 'admin' || user.role === 'support') ? user.role : 'user');
        setEditCanDeleteServers(!!user.canDeleteServers);
        setEditCanChangeResources(!!user.canChangeResources);
        setEditSupportTeam(user.supportTeam || '');
        setEditCanCreateModpacks(user.canCreateModpacks ?? true);
    };

    const handleResetPassword = async () => {
        if (!settingsUser || !newPassword) return;
        setPwSaving(true);
        const res = await resetUserPassword(settingsUser.id, newPassword);
        if (res.success) {
            showToast('Password updated');
            setNewPassword('');
        } else {
            showToast(res.message || 'Failed to reset password', false);
        }
        setPwSaving(false);
    };

    const handleReset2FA = async () => {
        if (!settingsUser) return;
        if (!(await confirmDialog({ title: 'Reset two-factor', message: `Reset 2FA for "${settingsUser.username}"? They will be able to log in with just their password until they re-enable 2FA.`, confirmLabel: 'Reset 2FA' }))) return;
        setResetting2FA(true);
        try {
            const res = await adminResetTOTP(settingsUser.id);
            if (res?.success) {
                showToast('2FA reset for user');
                setSettingsUser({ ...settingsUser, is2FAEnabled: false });
                loadUsers();
            } else {
                showToast(res?.message || 'Failed to reset 2FA', false);
            }
        } finally {
            setResetting2FA(false);
        }
    };

    const handleSaveRouteLimit = async () => {
        if (!settingsUser) return;
        setRouteSaving(true);
        const res = await setUserRouteLimit(settingsUser.id, {
            mode: routeMode,
            // Only "custom" carries a number; the other three modes ARE the
            // answer and the backend ignores this field for them.
            maxRoutes: routeMode === 'custom' ? routeMax : 0,
        });
        if (res.success) {
            showToast('Route limit updated');
        } else {
            showToast(res.message || 'Failed to save', false);
        }
        setRouteSaving(false);
    };

    const tabClass = (tab: string) =>
        `px-4 py-2 text-sm font-medium transition-colors border-b-2 ${
            settingsTab === tab
                ? 'text-(--base-09) border-(--accent)'
                : 'text-(--base-06) border-transparent hover:text-(--base-08)'
        }`;

    return (
        <div>
            <div className="flex flex-wrap items-center justify-between gap-3 mb-4">
                <div className="flex items-center gap-2">
                    <label htmlFor="user-sort" className="mono-label">Sort by</label>
                    <select
                        id="user-sort"
                        value={sort}
                        onChange={e => setSort(e.target.value as UserSort)}
                        className="input-field py-1.5 text-sm"
                    >
                        <option value="role">Role</option>
                        <option value="name">Name</option>
                        <option value="created">Newest first</option>
                    </select>
                </div>
                <button onClick={() => {setUserForm({ username: "", password: "", isAdmin: false }); setError(""); setIsModalOpen(true);}} className="btn btn-primary">
                    <UserPlus size={14} />
                    Create User
                </button>
            </div>

            <div className="table-wrapper">
                <table className="w-full">
                    <thead>
                        <tr>
                            <th className="table-th text-left">Username</th>
                            <th className="table-th text-left">Role</th>
                            <th className="table-th text-left">Created At</th>
                            <th className="table-th text-right">Actions</th>
                        </tr>
                    </thead>
                    <tbody>
                        {sortUsers(users, sort).map(u => (
                            <tr key={u.id} className="table-tr table-tr-hover">
                                <td className="table-td font-mono font-medium text-(--base-09)">
                                    {u.username}
                                    {u.deletionStatus === 'pending_deletion' && (
                                        <span
                                            title={u.deletionScheduledAt ? `Scheduled for deletion on ${new Date(u.deletionScheduledAt).toLocaleDateString()}` : 'Scheduled for deletion'}
                                            className="ml-2 inline-flex items-center gap-1 px-1.5 py-0.5 rounded-sm text-[10px] font-mono uppercase tracking-[0.06em] bg-(--error-ghost) text-(--error-light) border border-(--error)/15"
                                        >
                                            <ShieldAlert size={10} />
                                            Pending delete
                                        </span>
                                    )}
                                    {u.deletionStatus === 'anonymized' && (
                                        <span className="ml-2 inline-flex items-center gap-1 px-1.5 py-0.5 rounded-sm text-[10px] font-mono uppercase tracking-[0.06em] bg-(--base-03) text-(--base-06)">
                                            <Trash2 size={10} />
                                            Anonymized
                                        </span>
                                    )}
                                </td>
                                <td className="table-td">
                                    {(() => {
                                        const role = u.role || (u.isAdmin ? 'admin' : 'user');
                                        const cls =
                                            role === 'admin' ? 'badge badge-accent'
                                            : role === 'support' ? 'badge badge-warning'
                                            : 'badge badge-neutral';
                                        return <span className={cls}>{role.toUpperCase()}</span>;
                                    })()}
                                </td>
                                <td className="table-td text-sm text-(--base-06)">{u.createdAt ? new Date(u.createdAt).toLocaleDateString() : 'N/A'}</td>
                                <td className="table-td text-right">
                                    <div className="flex items-center justify-end gap-2">
                                        <button
                                            onClick={() => setHistoryUser({ id: u.id, username: u.username })}
                                            className="btn px-2.5 py-1 text-xs bg-(--base-03) border border-(--base-04) text-(--base-07) hover:text-(--base-09) transition-colors"
                                            title="Username history"
                                        >
                                            <HistoryIcon size={13} />
                                        </button>
                                        <button
                                            onClick={() => setBillingUser({ id: u.id, username: u.username })}
                                            className="btn px-2.5 py-1 text-xs bg-(--base-03) border border-(--base-04) text-(--base-07) hover:text-(--base-09) transition-colors"
                                            title="Billing & retention (BYON)"
                                        >
                                            <CreditCard size={13} />
                                        </button>
                                        <button
                                            onClick={() => openSettings(u)}
                                            className="btn px-2.5 py-1 text-xs bg-(--base-03) border border-(--base-04) text-(--base-07) hover:text-(--base-09) transition-colors"
                                            title="User settings"
                                        >
                                            <Settings size={13} />
                                        </button>
                                        {(() => {
                                            /* The button is always PRESENT, and
                                               says why when it is off. A button
                                               that simply vanishes reads as a
                                               missing feature, and the reader
                                               never learns the rule. */
                                            const isSelf = currentUser?.username === u.username;
                                            const isLastAdmin = u.isAdmin && adminCount <= 1;
                                            const reason = isSelf
                                                ? 'You cannot delete your own account'
                                                : isLastAdmin
                                                    ? 'This is the last admin. Make someone else an admin first.'
                                                    : undefined;
                                            return (
                                                <button
                                                    onClick={() => handleDeleteUser(u.id)}
                                                    disabled={!!reason}
                                                    title={reason || `Delete ${u.username}`}
                                                    className="btn btn-danger btn-sm disabled:opacity-40 disabled:cursor-not-allowed"
                                                >
                                                    Delete
                                                </button>
                                            );
                                        })()}
                                    </div>
                                </td>
                            </tr>
                        ))}
                    </tbody>
                </table>
            </div>

            {/* Create User Modal */}
            {isModalOpen && (
                <div className="modal-overlay animate-fade-in">
                    <div className="modal-panel w-full max-w-md">
                        <div className="modal-header">
                            <h3 className="modal-title">New User</h3>
                        </div>
                        <div className="modal-body">
                            {error && <div className="alert alert-error mb-4 font-medium">{error}</div>}

                            <form onSubmit={handleCreateUser} className="space-y-4">
                                <div className="flex flex-col gap-[5px]">
                                    <label className="input-label">Username</label>
                                    <input required type="text" value={userForm.username} onChange={e => setUserForm({...userForm, username: e.target.value})} className="input-field w-full" />
                                </div>
                                <div className="flex flex-col gap-[5px]">
                                    <label className="input-label">Password</label>
                                    <input required type="password" value={userForm.password} onChange={e => setUserForm({...userForm, password: e.target.value})} className="input-field w-full" />
                                </div>
                                <div className="flex items-center gap-2">
                                    <input type="checkbox"
                            className="checkbox" checked={userForm.isAdmin} onChange={e => setUserForm({...userForm, isAdmin: e.target.checked})} />
                                    <label className="text-sm font-medium text-(--base-08)">Administrator Rights</label>
                                </div>
                                <UserRegionPicker
                                    allRegions={createAllRegions}
                                    regions={createRegions}
                                    onChange={next => { setCreateAllRegions(next.allRegions); setCreateRegions(next.regions); }}
                                />
                                <div className="modal-footer">
                                    <button type="button" onClick={() => setIsModalOpen(false)} className="btn btn-secondary">Cancel</button>
                                    <button type="submit" className="btn btn-primary">Create User</button>
                                </div>
                            </form>
                        </div>
                    </div>
                </div>
            )}

            {/* User Settings Modal */}
            {settingsUser && (
                <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60">
                    <div className="card w-full max-w-lg flex flex-col max-h-[80vh]">
                        {/* Header */}
                        <div className="flex items-center justify-between p-5 border-b border-(--base-03)">
                            <div>
                                <h3 className="h-section">User Settings</h3>
                                <p className="text-xs font-mono text-(--base-06)">{settingsUser.username}</p>
                            </div>
                            <button onClick={() => setSettingsUser(null)} className="p-1 text-(--base-06) hover:text-(--base-09)">
                                <X size={16} />
                            </button>
                        </div>

                        {/* Tabs */}
                        <div className="flex border-b border-(--base-03)">
                            <button
                                type="button"
                                onClick={() => setSettingsTab('general')}
                                className={tabClass('general')}
                            >
                                General
                            </button>
                            <button
                                type="button"
                                onClick={() => setSettingsTab('gateway')}
                                className={tabClass('gateway')}
                            >
                                Gateway
                            </button>
                        </div>

                        {/* Tab Content */}
                        <div className="p-5 overflow-y-auto flex-1">
                            {settingsTab === 'general' && (
                                <div className="space-y-5">
                                    {settingsUser.deletionStatus === 'pending_deletion' && (
                                        <div className="rounded-md border border-(--error)/15 bg-(--error-ghost) p-3 space-y-2">
                                            <div className="flex items-start gap-2">
                                                <ShieldAlert size={16} className="text-(--error-light) mt-0.5 shrink-0" />
                                                <div className="text-sm">
                                                    <p className="text-(--error-light) font-medium">Scheduled for deletion</p>
                                                    <p className="text-xs text-(--base-07) mt-0.5">
                                                        {settingsUser.deletionScheduledAt
                                                            ? <>This account will be auto-removed on <span className="font-mono">{new Date(settingsUser.deletionScheduledAt).toLocaleString()}</span> unless rescued. The user can also rescue it themselves by signing in.</>
                                                            : 'This account will be auto-removed soon.'}
                                                    </p>
                                                </div>
                                            </div>
                                            <button
                                                type="button"
                                                onClick={handleCancelDeletion}
                                                disabled={cancellingDeletion}
                                                className="btn btn-secondary btn-sm w-full disabled:opacity-40"
                                            >
                                                {cancellingDeletion ? 'Cancelling…' : 'Cancel scheduled deletion'}
                                            </button>
                                        </div>
                                    )}

                                    <div>
                                        <h4 className="mono-label mb-3">Reset Password</h4>
                                        <div className="flex gap-2">
                                            <input
                                                type="password"
                                                value={newPassword}
                                                onChange={e => setNewPassword(e.target.value)}
                                                placeholder="New password"
                                                className="input-field flex-1"
                                            />
                                            <button
                                                onClick={handleResetPassword}
                                                disabled={!newPassword || pwSaving}
                                                className="btn btn-primary disabled:opacity-40 shrink-0"
                                            >
                                                {pwSaving ? 'Saving...' : 'Reset'}
                                            </button>
                                        </div>
                                    </div>

                                    <div>
                                        <h4 className="mono-label mb-3">Two-Factor Authentication</h4>
                                        <div className="flex items-center justify-between gap-3 p-3 rounded-md bg-(--base-02) border border-(--base-03)">
                                            <div className="text-sm">
                                                <p className="text-(--base-09)">
                                                    {settingsUser.is2FAEnabled ? 'Enabled' : 'Not enabled'}
                                                </p>
                                                <p className="text-xs text-(--base-06)">
                                                    {settingsUser.is2FAEnabled
                                                        ? 'Use this if the user lost their authenticator and backup codes.'
                                                        : 'Reset is only available when 2FA is enabled.'}
                                                </p>
                                            </div>
                                            <button
                                                type="button"
                                                onClick={handleReset2FA}
                                                disabled={!settingsUser.is2FAEnabled || resetting2FA}
                                                className="btn btn-danger btn-sm disabled:opacity-40 disabled:cursor-not-allowed shrink-0 inline-flex items-center gap-1.5"
                                            >
                                                <ShieldOff size={12} />
                                                {resetting2FA ? 'Resetting…' : 'Reset 2FA'}
                                            </button>
                                        </div>
                                    </div>

                                    {/* Role + capability flags. Role drives the badge in the
                                        user list and is_admin sync; flags are checked at the matching
                                        server-mutation handlers. SupportTeam ist optional and reserved
                                        for the upcoming ticket-system. */}
                                    <div>
                                        <h4 className="mono-label mb-3">Role &amp; Permissions</h4>
                                        <div className="space-y-3 p-3 rounded-md bg-(--base-02) border border-(--base-03)">
                                            <div className="flex flex-col gap-[5px]">
                                                <label className="input-label">Role</label>
                                                <select
                                                    value={editRole}
                                                    onChange={e => setEditRole(e.target.value as 'user' | 'support' | 'admin')}
                                                    className="input-field w-full"
                                                    disabled={editRolePermsSaving}
                                                >
                                                    <option value="user">User — standard account</option>
                                                    <option value="support">Support — assists with assigned tickets</option>
                                                    <option value="admin">Admin — full access</option>
                                                </select>
                                                <p className="text-xs text-(--base-06)">Admins implicitly have all capability flags. The flags below only matter for non-admins.</p>
                                            </div>
                                            <label className="flex items-start gap-2 text-sm cursor-pointer">
                                                <input
                                                    type="checkbox"
                                                    checked={editCanDeleteServers}
                                                    onChange={e => setEditCanDeleteServers(e.target.checked)}
                                                    className="checkbox mt-0.5"
                                                    disabled={editRolePermsSaving || editRole === 'admin'}
                                                />
                                                <span>
                                                    <span className="font-medium">Can delete servers</span>
                                                    <span className="block text-xs text-(--base-06)">Required for the user to invoke DELETE /servers on accounts they own or co-manage.</span>
                                                </span>
                                            </label>
                                            <label className="flex items-start gap-2 text-sm cursor-pointer">
                                                <input
                                                    type="checkbox"
                                                    checked={editCanChangeResources}
                                                    onChange={e => setEditCanChangeResources(e.target.checked)}
                                                    className="checkbox mt-0.5"
                                                    disabled={editRolePermsSaving || editRole === 'admin'}
                                                />
                                                <span>
                                                    <span className="font-medium">Can change server resources</span>
                                                    <span className="block text-xs text-(--base-06)">Required for the user to edit RAM, CPU, or disk limits on servers.</span>
                                                </span>
                                            </label>
                                            <div className="flex flex-col gap-[5px]">
                                                <label className="input-label">Support team (optional)</label>
                                                <input
                                                    type="text"
                                                    value={editSupportTeam}
                                                    onChange={e => setEditSupportTeam(e.target.value)}
                                                    placeholder="e.g. nodes, billing — used for ticket scoping"
                                                    maxLength={64}
                                                    className="input-field w-full"
                                                    disabled={editRolePermsSaving}
                                                />
                                            </div>
                                            <div className="flex justify-end">
                                                <button
                                                    type="button"
                                                    onClick={handleSaveRoleAndPermissions}
                                                    disabled={editRolePermsSaving}
                                                    className="btn btn-primary btn-sm disabled:opacity-40"
                                                >
                                                    {editRolePermsSaving ? 'Saving…' : 'Save role &amp; permissions'}
                                                </button>
                                            </div>
                                        </div>
                                    </div>

                                    {/* Modpack-authoring flag. Admins always pass the
                                        backend check regardless of this toggle (handlers short-circuit
                                        on isAdmin), so the help-text says so explicitly. */}
                                    <div>
                                        <h4 className="mono-label mb-3">Modpack Authoring</h4>
                                        <div className="rounded-md bg-(--base-02) border border-(--base-03) p-3">
                                            <div className="flex items-center justify-between gap-3">
                                                <div className="flex items-start gap-3 min-w-0">
                                                    <Package size={16} className="text-(--accent-light) mt-0.5 shrink-0" />
                                                    <div className="min-w-0">
                                                        <div className="font-medium text-sm text-(--base-09) flex items-center gap-2">
                                                            Can create modpacks
                                                            {settingsUser?.canCreateModpacksManual && (
                                                                <span className="badge badge-neutral" title="Set by hand, so the platform authoring switch leaves it alone">
                                                                    Overridden
                                                                </span>
                                                            )}
                                                        </div>
                                                        <div className="text-xs text-(--base-06) mt-0.5">
                                                            {settingsUser?.canCreateModpacksManual
                                                                ? 'Set by hand: the platform "Open authoring to users" switch will not change it. Admin bypass applies.'
                                                                : 'Follows the platform "Open authoring to users" switch. Changing it here pins it to your choice. Admin bypass applies.'}
                                                        </div>
                                                    </div>
                                                </div>
                                                <button
                                                    type="button"
                                                    role="switch"
                                                    aria-checked={editCanCreateModpacks}
                                                    onClick={() => setEditCanCreateModpacks(v => !v)}
                                                    className={`toggle-track ${editCanCreateModpacks ? 'toggle-track-on' : 'toggle-track-off'}`}
                                                    disabled={modpackFlagSaving}
                                                >
                                                    <span className={`toggle-knob ${editCanCreateModpacks ? 'toggle-knob-on' : 'toggle-knob-off'}`} />
                                                </button>
                                            </div>
                                            <div className="mt-3 flex justify-end gap-2">
                                                {settingsUser?.canCreateModpacksManual && (
                                                    <button
                                                        type="button"
                                                        onClick={handleClearModpackOverride}
                                                        disabled={modpackFlagSaving}
                                                        className="btn btn-secondary btn-sm disabled:opacity-40"
                                                        title="Clear the override so this user follows the platform setting again. Does not change what they may do right now."
                                                    >
                                                        Follow platform setting
                                                    </button>
                                                )}
                                                {editCanCreateModpacks !== (settingsUser?.canCreateModpacks ?? true) && (
                                                    <button
                                                        type="button"
                                                        onClick={handleSaveModpackFlag}
                                                        disabled={modpackFlagSaving}
                                                        className="btn btn-primary btn-sm disabled:opacity-40"
                                                    >
                                                        {modpackFlagSaving ? 'Saving…' : 'Save modpack flag'}
                                                    </button>
                                                )}
                                            </div>
                                        </div>
                                    </div>

                                    {/* Region access — hidden by the picker itself in single-region setups. */}
                                    <div>
                                        <h4 className="mono-label mb-3">Region Access</h4>
                                        <UserRegionPicker
                                            allRegions={editAllRegions}
                                            regions={editRegions}
                                            onChange={next => { setEditAllRegions(next.allRegions); setEditRegions(next.regions); }}
                                            disabled={editRegionsSaving}
                                        />
                                        <div className="flex justify-end mt-3">
                                            <button
                                                type="button"
                                                onClick={handleSaveRegions}
                                                disabled={editRegionsSaving}
                                                className="btn btn-primary btn-sm disabled:opacity-40"
                                            >
                                                {editRegionsSaving ? 'Saving…' : 'Save region access'}
                                            </button>
                                        </div>
                                    </div>
                                </div>
                            )}

                            {settingsTab === 'gateway' && (
                                <div className="space-y-4">
                                    <div>
                                        <h4 className="mono-label mb-3">Route Limit Override</h4>
                                        <p className="text-xs text-(--base-06) mb-3">Override the global default route limit for this user.</p>

                                        <div className="space-y-2">
                                            {/* Default */}
                                            <label className={`flex items-center gap-3 p-3 rounded-md cursor-pointer transition-colors ${routeMode === 'default' ? 'bg-(--accent)/10 border border-(--accent)/30' : 'bg-(--base-02) border border-transparent'}`}>
                                                <input
                                                    type="radio"
                                                    name="routeMode"
                                                    checked={routeMode === 'default'}
                                                    onChange={() => setRouteMode('default')}
                                                    className="accent-(--accent)"
                                                />
                                                <div>
                                                    <p className="text-sm text-(--base-09)">Use global default</p>
                                                    <p className="text-xs text-(--base-06)">Uses the per-user default limit from gateway settings</p>
                                                </div>
                                            </label>

                                            {/* Unlimited */}
                                            <label className={`flex items-center gap-3 p-3 rounded-md cursor-pointer transition-colors ${routeMode === 'unlimited' ? 'bg-(--accent)/10 border border-(--accent)/30' : 'bg-(--base-02) border border-transparent'}`}>
                                                <input
                                                    type="radio"
                                                    name="routeMode"
                                                    checked={routeMode === 'unlimited'}
                                                    onChange={() => setRouteMode('unlimited')}
                                                    className="accent-(--accent)"
                                                />
                                                <div>
                                                    <p className="text-sm text-(--base-09)">No limit</p>
                                                    <p className="text-xs text-(--base-06)">This user is uncapped, whatever the global default becomes later</p>
                                                </div>
                                            </label>

                                            {/* Custom */}
                                            <label className={`flex items-center gap-3 p-3 rounded-md cursor-pointer transition-colors ${routeMode === 'custom' ? 'bg-(--accent)/10 border border-(--accent)/30' : 'bg-(--base-02) border border-transparent'}`}>
                                                <input
                                                    type="radio"
                                                    name="routeMode"
                                                    checked={routeMode === 'custom'}
                                                    onChange={() => setRouteMode('custom')}
                                                    className="accent-(--accent)"
                                                />
                                                <div className="flex-1">
                                                    <p className="text-sm text-(--base-09)">Custom limit</p>
                                                    <p className="text-xs text-(--base-06)">Set a specific max number of routes for this user</p>
                                                </div>
                                                {routeMode === 'custom' && (
                                                    <input
                                                        type="number"
                                                        min={1}
                                                        value={routeMax}
                                                        onChange={e => setRouteMax(Number(e.target.value))}
                                                        className="input-mono w-20 text-center"
                                                        onClick={e => e.stopPropagation()}
                                                    />
                                                )}
                                            </label>

                                            {/* Disabled */}
                                            <label className={`flex items-center gap-3 p-3 rounded-md cursor-pointer transition-colors ${routeMode === 'disabled' ? 'bg-(--error)/10 border border-(--error)/30' : 'bg-(--base-02) border border-transparent'}`}>
                                                <input
                                                    type="radio"
                                                    name="routeMode"
                                                    checked={routeMode === 'disabled'}
                                                    onChange={() => setRouteMode('disabled')}
                                                    className="accent-(--error)"
                                                />
                                                <div>
                                                    <p className="text-sm text-(--base-09)">Disabled</p>
                                                    <p className="text-xs text-(--base-06)">This user cannot create any routes</p>
                                                </div>
                                            </label>
                                        </div>

                                        <div className="flex justify-end mt-4">
                                            <button
                                                onClick={handleSaveRouteLimit}
                                                disabled={routeSaving}
                                                className="btn btn-primary disabled:opacity-40"
                                            >
                                                {routeSaving ? 'Saving...' : 'Save Route Limit'}
                                            </button>
                                        </div>
                                    </div>
                                </div>
                            )}
                        </div>
                    </div>
                </div>
            )}

            {/* Toast */}
            {toast && (
                <div className="fixed bottom-6 right-6 z-50">
                    <div className="flex items-center gap-2 px-4 py-2.5 rounded-md bg-(--base-02) border border-(--base-04) shadow-lg">
                        {toast.ok ? <CircleCheck size={14} className="text-(--success-light)" /> : <CircleAlert size={14} className="text-(--error)" />}
                        <span className="text-sm text-(--base-09)">{toast.msg}</span>
                    </div>
                </div>
            )}

            {/* Username history modal */}
            {historyUser && (
                <UsernameHistoryModal user={historyUser} onClose={() => setHistoryUser(null)} />
            )}

            {/* Billing override modal */}
            {billingUser && (
                <BillingOverrideModal user={billingUser} onClose={() => setBillingUser(null)} />
            )}
        </div>
    );
}

// ─────────────────────────────────────────────
// Account Policy card
// ─────────────────────────────────────────────
// Loads the platform-level rename policy and lets the admin toggle whether
// users may rename themselves and set a cooldown between renames. The
// per-user 429/403 surface in ProfilePopup is driven by the server's reply,
// so this card is the only place the policy is configured.
// ─────────────────────────────────────────────
// Username history modal
// ─────────────────────────────────────────────
function UsernameHistoryModal({ user, onClose }: { user: { id: string; username: string }; onClose: () => void }) {
    const [rows, setRows] = useState<UsernameHistoryEntry[]>([]);
    const [loading, setLoading] = useState(true);
    useEffect(() => {
        getUsernameHistory(user.id).then(r => { setRows(r); setLoading(false); });
    }, [user.id]);
    return (
        <div className="modal-overlay animate-fade-in" onClick={onClose}>
            <div className="modal-panel w-full max-w-lg" onClick={e => e.stopPropagation()}>
                <div className="modal-header flex justify-between items-center">
                    <h3 className="modal-title">Username history — {user.username}</h3>
                    <button onClick={onClose} className="p-1 rounded hover:bg-(--base-03) text-(--base-06)">
                        <X size={16} />
                    </button>
                </div>
                <div className="modal-body max-h-[60vh] overflow-y-auto">
                    {loading ? (
                        <div className="space-y-1">
                            {Array.from({ length: 4 }).map((_, i) => (
                                <div key={i} className="p-2 rounded-md border border-(--base-04) space-y-1.5">
                                    <SkeletonText width="w-1/2" />
                                    <SkeletonText width="w-2/3" className="h-2" />
                                </div>
                            ))}
                        </div>
                    ) : rows.length === 0 ? (
                        <p className="text-sm text-(--base-06)">No renames recorded.</p>
                    ) : (
                        <div className="space-y-1">
                            {rows.map(r => (
                                <div key={r.id} className="p-2 rounded-md border border-(--base-04) text-xs">
                                    <div className="font-mono text-(--base-09)">{r.oldUsername} → {r.newUsername}</div>
                                    <div className="text-(--base-06) mt-0.5">
                                        {new Date(r.changedAt).toLocaleString()} · {r.byAdmin ? 'by admin' : 'self'}
                                    </div>
                                </div>
                            ))}
                        </div>
                    )}
                </div>
                <div className="modal-footer">
                    <button type="button" onClick={onClose} className="btn btn-secondary">Close</button>
                </div>
            </div>
        </div>
    );
}

// ─────────────────────────────────────────────
// Billing override modal (BYON lifecycle)
// ─────────────────────────────────────────────
// Admin surface for one tenant's billing: flip the lifecycle status (which has
// side effects — past_due starts the grace window + dunning mail, suspended
// stops their servers) and set per-user retention overrides that fall back to
// the platform defaults shown as placeholders. Only meaningful when BYON is
// enabled and the user owns nodes, but harmless to set ahead of time.
const SPEC_RE = /^\d+[dwm]$/;

function statusBadge(s: BillingStatus) {
    const cls = s === 'suspended' ? 'badge badge-error' : s === 'past_due' ? 'badge badge-warning' : 'badge badge-neutral';
    return <span className={cls}>{s.replace('_', ' ').toUpperCase()}</span>;
}

function BillingOverrideModal({ user, onClose }: { user: { id: string; username: string }; onClose: () => void }) {
    const [data, setData] = useState<UserBillingAdmin | null>(null);
    const [loading, setLoading] = useState(true);
    const [status, setStatus] = useState<BillingStatus>('active');
    const [gp, setGp] = useState('');
    const [r2, setR2] = useState('');
    const [nr, setNr] = useState('');
    const [quota, setQuota] = useState(''); // '' = use platform default
    const [savingStatus, setSavingStatus] = useState(false);
    const [savingOverrides, setSavingOverrides] = useState(false);
    const [toast, setToast] = useState<{ msg: string; ok: boolean } | null>(null);

    // Per-user limit overrides ('' = leave unset, which means unlimited).
    // There is no plan to fall back to any more; these ARE the caps.
    const [maxNodes, setMaxNodes] = useState('');
    const [maxLinks, setMaxLinks] = useState('');
    const [tEdge, setTEdge] = useState('');
    const [tRelay, setTRelay] = useState('');
    const [tCombined, setTCombined] = useState('');
    const [savingLimits, setSavingLimits] = useState(false);

    // Entitlement: WHAT the tenant may use, as opposed to the status and caps
    // below. Shown first because it is the question an operator actually opens
    // this modal with ("can this person do BYON yet"), and because a grant is
    // the one control here that hands out capability rather than restricting it.
    const [ent, setEnt] = useState<Entitlement | null>(null);
    // One pending duration per kind. They are separate grants now, so a single
    // shared value would make the two rows fight over one input.
    const [grantDaysByon, setGrantDaysByon] = useState('30');
    const [grantDaysRoute, setGrantDaysRoute] = useState('30');
    // How many of each they may hold. Empty means "leave the limit alone", which
    // for a tenant who has bought nothing means NO limit - so the row says so
    // rather than letting the default be silent.
    // Which row is mid-request, so only that row's buttons go inert.
    const [grantBusy, setGrantBusy] = useState<'byon' | 'route_only' | null>(null);

    useEffect(() => {
        getUserEntitlement(user.id).then(e => {
            if (e.success) {
                setEnt(entitlementOf(e));
            }
        });
        getUserBilling(user.id).then(d => {
            if (d.success) {
                setData(d);
                setStatus(d.status);
                setGp(d.overrides.gracePeriod || '');
                setR2(d.overrides.r2Retention || '');
                setNr(d.overrides.nodeRetention || '');
                setQuota(d.overrides.r2QuotaGb == null ? '' : String(d.overrides.r2QuotaGb));
                setMaxNodes(d.overrides.maxNodes == null ? '' : String(d.overrides.maxNodes));
                setMaxLinks(d.overrides.maxLinks == null ? '' : String(d.overrides.maxLinks));
                setTEdge(d.overrides.trafficEdgeGb == null ? '' : String(d.overrides.trafficEdgeGb));
                setTRelay(d.overrides.trafficRelayGb == null ? '' : String(d.overrides.trafficRelayGb));
                setTCombined(d.overrides.trafficCombinedGb == null ? '' : String(d.overrides.trafficCombinedGb));
            }
            setLoading(false);
        });
    }, [user.id]);

    const show = (msg: string, ok: boolean) => { setToast({ msg, ok }); setTimeout(() => setToast(null), 3000); };

    const applyEntitlement = (r: EntitlementResponse, okMsg: string) => {
        if (!r.success) { show(r.message || 'Failed', false); return; }
        // The response carries the RESOLVED entitlement, so the panel reflects
        // what the server decided rather than what the form asked for. Those
        // differ whenever a plan already covers what was granted.
        setEnt(entitlementOf(r));
        show(okMsg, true);
    };

    // No quantity: a grant is worth one machine of its kind, and Core derives
    // that. There was a field here, and it wrote the same column the store
    // pushes a purchase into - so granting made a tenant read as a paying one,
    // and the number outlived the grant.
    const handleGrant = async (kind: 'byon' | 'route_only', rawDays: string) => {
        const days = parseInt(rawDays, 10);
        if (!Number.isFinite(days) || days < 1 || days > 730) {
            show('Days must be between 1 and 730', false);
            return;
        }
        setGrantBusy(kind);
        const r = await grantEntitlement(user.id, kind, days);
        setGrantBusy(null);
        applyEntitlement(r, `Granted for ${days} day${days === 1 ? '' : 's'}`);
    };

    // Ends ONE kind. The other keeps whatever time it has left - that is the
    // whole reason the two carry separate deadlines.
    const handleRevokeGrant = async (kind: 'byon' | 'route_only') => {
        setGrantBusy(kind);
        const r = await revokeEntitlement(user.id, kind);
        setGrantBusy(null);
        applyEntitlement(r, 'Grant removed');
    };

    const specOk = (v: string) => v === '' || SPEC_RE.test(v);
    const quotaOk = quota === '' || /^\d+$/.test(quota);
    const overridesValid = specOk(gp) && specOk(r2) && specOk(nr) && quotaOk;

    const applyStatus = async () => {
        setSavingStatus(true);
        const res = await setUserBillingStatus(user.id, status);
        setSavingStatus(false);
        show(res.success ? 'Status updated.' : (res.message || 'Failed.'), !!res.success);
    };

    const saveOverrides = async () => {
        if (!overridesValid) { show('Use specs like 3d, 2w, 3m; quota is a whole number of GB.', false); return; }
        setSavingOverrides(true);
        const res = await setUserBillingOverrides(user.id, {
            gracePeriod: gp,
            r2Retention: r2,
            nodeRetention: nr,
            r2QuotaGb: quota === '' ? null : parseInt(quota, 10),
        });
        setSavingOverrides(false);
        show(res.success ? 'Overrides saved.' : (res.message || 'Failed.'), !!res.success);
    };

    const numOk = (v: string) => v === '' || /^\d+$/.test(v);
    const limitsValid = numOk(maxNodes) && numOk(maxLinks) && numOk(tEdge) && numOk(tRelay) && numOk(tCombined);
    const toNum = (v: string) => (v === '' ? null : parseInt(v, 10));

    const saveLimits = async () => {
        if (!limitsValid) { show('Limits are whole numbers (empty = no limit, 0 = none).', false); return; }
        setSavingLimits(true);
        const res = await setUserLimitOverrides(user.id, {
            maxNodes: toNum(maxNodes),
            maxLinks: toNum(maxLinks),
            trafficEdgeGb: toNum(tEdge),
            trafficRelayGb: toNum(tRelay),
            trafficCombinedGb: toNum(tCombined),
        });
        setSavingLimits(false);
        show(res.success ? 'Limits saved.' : (res.message || 'Failed.'), res.success);
    };

    return (
        <div className="modal-overlay animate-fade-in" onClick={onClose}>
            <div className="modal-panel w-full max-w-lg" onClick={e => e.stopPropagation()}>
                <div className="modal-header flex justify-between items-center">
                    <h3 className="modal-title">Billing — {user.username}</h3>
                    <button onClick={onClose} className="p-1 rounded hover:bg-(--base-03) text-(--base-06)">
                        <X size={16} />
                    </button>
                </div>
                <div className="modal-body max-h-[70vh] overflow-y-auto space-y-6">
                    {loading || !data ? (
                        <div className="space-y-3">
                            <SkeletonText width="w-1/3" />
                            <SkeletonText width="w-2/3" className="h-2" />
                            <SkeletonText width="w-1/2" className="h-2" />
                        </div>
                    ) : (
                        <>
                            {/* Entitlement: what this tenant may USE. First, because
                                it is the question this modal usually gets opened
                                with, and the grant is the only control here that
                                hands out capability instead of limiting it. */}
                            <section className="space-y-3">
                                <label className="input-label">Access</label>
                                {!ent ? (
                                    <SkeletonText width="w-1/2" className="h-2" />
                                ) : (
                                    <>
                                        <div className="flex flex-wrap items-center gap-2">
                                            <span className={`badge ${ent.byon ? 'badge-accent' : 'badge-neutral'}`}>
                                                {ent.byon ? 'Bring your own node' : 'No BYON'}
                                            </span>
                                            <span className={`badge ${ent.routeOnly ? 'badge-accent' : 'badge-neutral'}`}>
                                                {ent.routeOnly ? 'Route only' : 'No routes'}
                                            </span>
                                        </div>
                                        <p className="text-xs text-(--base-06)">
                                            {entitlementExplanation(ent)}
                                        </p>
                                        {/* One row per kind, because they ARE two grants.
                                            A single dropdown made granting the second
                                            one silently end the first, which is what
                                            "it only switches between them" was. */}
                                        <div className="space-y-2">
                                            <GrantRow
                                                label="Bring your own node"
                                                expiresAt={ent.grantByonExpiresAt}
                                                days={grantDaysByon}
                                                onDaysChange={setGrantDaysByon}
                                                busy={grantBusy === 'byon'}
                                                disabled={grantBusy !== null}
                                                onGrant={() => handleGrant('byon', grantDaysByon)}
                                                onRevoke={() => handleRevokeGrant('byon')}
                                            />
                                            <GrantRow
                                                label="Route only"
                                                expiresAt={ent.grantRouteExpiresAt}
                                                days={grantDaysRoute}
                                                onDaysChange={setGrantDaysRoute}
                                                busy={grantBusy === 'route_only'}
                                                disabled={grantBusy !== null}
                                                onGrant={() => handleGrant('route_only', grantDaysRoute)}
                                                onRevoke={() => handleRevokeGrant('route_only')}
                                            />
                                        </div>
                                        <p className="text-xs text-(--base-06)">
                                            Each is granted on its own and keeps its own deadline, so one may run for a week
                                            and the other for a year. A grant is added on top of whatever they have bought,
                                            so subscribing later extends the access rather than colliding with it, and it
                                            lapses on its own at the end of the period.
                                        </p>
                                    </>
                                )}
                            </section>

                            {/* Lifecycle status */}
                            <section className="space-y-2">
                                <div className="flex items-center justify-between">
                                    <label className="input-label">Lifecycle status</label>
                                    {statusBadge(data.status)}
                                </div>
                                <p className="text-xs text-(--base-06)">
                                    past due starts the grace window + dunning email; suspended stops the tenant&apos;s servers
                                    (data and backups are kept). active reactivates without auto-starting servers.
                                </p>
                                {data.graceUntil && (
                                    <p className="text-xs text-(--base-06)">Grace until {new Date(data.graceUntil).toLocaleString()}</p>
                                )}
                                {data.suspendedAt && (
                                    <p className="text-xs text-(--base-06)">Suspended {new Date(data.suspendedAt).toLocaleString()}</p>
                                )}
                                <div className="flex items-center gap-2">
                                    <select
                                        value={status}
                                        onChange={e => setStatus(e.target.value as BillingStatus)}
                                        className="input-field w-44"
                                    >
                                        <option value="active">active</option>
                                        <option value="past_due">past due</option>
                                        <option value="suspended">suspended</option>
                                    </select>
                                    <button
                                        type="button"
                                        onClick={applyStatus}
                                        disabled={savingStatus || status === data.status}
                                        className="btn btn-secondary btn-sm disabled:opacity-40"
                                    >
                                        {savingStatus ? 'Applying…' : 'Apply status'}
                                    </button>
                                </div>
                            </section>

                            {/* Per-user retention overrides */}
                            <section className="space-y-3 border-t border-(--base-04) pt-4">
                                <div>
                                    <label className="input-label">Retention overrides</label>
                                    <p className="text-xs text-(--base-06) mt-0.5">Leave a field empty to use the platform default (shown faint).</p>
                                </div>
                                <OverrideField label="Grace period" value={gp} onChange={setGp} placeholder={data.defaults.gracePeriod} valid={specOk(gp)} />
                                <OverrideField label="R2 backup retention" value={r2} onChange={setR2} placeholder={data.defaults.r2Retention} valid={specOk(r2)} />
                                <OverrideField label="Node connection retention" value={nr} onChange={setNr} placeholder={data.defaults.nodeRetention} valid={specOk(nr)} />
                                <div className="flex items-center gap-3">
                                    <label className="input-label w-48 shrink-0">R2 quota (GB)</label>
                                    <input
                                        type="number"
                                        min={0}
                                        value={quota}
                                        onChange={e => setQuota(e.target.value)}
                                        placeholder={data.defaults.r2QuotaGb === '0' ? 'default (unlimited)' : `default (${data.defaults.r2QuotaGb})`}
                                        className={`input-field input-mono w-40 ${quotaOk ? '' : 'border-(--error)'}`}
                                    />
                                </div>
                                <p className="text-xs text-(--base-05)">Empty falls back to the platform default. 0 means this user may store none - it is not a way to switch the quota off.</p>
                                <div className="flex items-center justify-end gap-3">
                                    {toast && (
                                        <span className={`text-sm ${toast.ok ? 'text-(--success-light)' : 'text-(--error)'}`}>{toast.msg}</span>
                                    )}
                                    <button
                                        type="button"
                                        onClick={saveOverrides}
                                        disabled={savingOverrides || !overridesValid}
                                        className="btn btn-primary btn-sm disabled:opacity-40"
                                    >
                                        {savingOverrides ? 'Saving…' : 'Save overrides'}
                                    </button>
                                </div>
                            </section>

                            {/* Per-user limit overrides. The plan selector that
                                used to head this section is gone with plans
                                themselves: these values ARE the tenant's caps,
                                and the store writes the same ones on purchase. */}
                            <section className="space-y-3 border-t border-(--base-04) pt-4">
                                <div>
                                    <label className="input-label flex items-center gap-1.5">
                                        Limits
                                        <HelpTip label="About these limits">
                                            <p className="mb-2">
                                                <strong>Empty</strong> means no limit at all.
                                                <br />
                                                <strong>0</strong> means none: they may hold zero of this.
                                                <br />
                                                <strong>Any other number</strong> is the cap.
                                            </p>
                                            <p>
                                                Empty and 0 are opposites, and 0 is not a way to switch a limit
                                                off. This matches every other limit in the panel.
                                            </p>
                                        </HelpTip>
                                    </label>
                                    <p className="text-xs text-(--base-06) mt-0.5">
                                        What this tenant may hold. A purchase writes these too, so an edit here
                                        is overwritten the next time their subscription changes. Leave a field
                                        empty for no limit; 0 means they may hold none.
                                    </p>
                                </div>
                                <LimitField label="Max nodes" value={maxNodes} onChange={setMaxNodes} />
                                <LimitField label="Max links" value={maxLinks} onChange={setMaxLinks} />
                                <LimitField label="Traffic combined (GB/mo)" value={tCombined} onChange={setTCombined} />
                                <LimitField label="Traffic edge (GB/mo)" value={tEdge} onChange={setTEdge} />
                                <LimitField label="Traffic relay (GB/mo)" value={tRelay} onChange={setTRelay} />
                                <div className="flex items-center justify-end">
                                    <button
                                        type="button"
                                        onClick={saveLimits}
                                        disabled={savingLimits || !limitsValid}
                                        className="btn btn-primary btn-sm disabled:opacity-40"
                                    >
                                        {savingLimits ? 'Saving…' : 'Save limits'}
                                    </button>
                                </div>
                            </section>
                        </>
                    )}
                </div>
                <div className="modal-footer">
                    <button type="button" onClick={onClose} className="btn btn-secondary">Close</button>
                </div>
            </div>
        </div>
    );
}

/**
 * One entitlement, with its own clock.
 *
 * Deliberately shows the state and the control together: the row says what the
 * tenant has right now and the same row is where you change it. The previous
 * shape put the state in one box and a dropdown in another, which is how an
 * admin could grant the second kind without noticing they had just ended the
 * first.
 */
function GrantRow({
    label, expiresAt, days, onDaysChange,
    busy, disabled, onGrant, onRevoke,
}: {
    label: string;
    /** Set only while this kind is actively granted. */
    expiresAt?: string;
    days: string;
    onDaysChange: (v: string) => void;
    busy: boolean;
    disabled: boolean;
    onGrant: () => void;
    onRevoke: () => void;
}) {
    const granted = !!expiresAt;
    // No quantity field. A grant is worth one machine of its kind - there is
    // nothing to choose - and the field that used to be here wrote the same
    // column the store pushes a purchase into, so a granted tenant read as a
    // paying one and the number outlived the grant. Core derives the one now.
    return (
        <div className="flex flex-wrap items-center gap-2 rounded-md bg-(--base-02) border border-(--base-03) px-3 py-2">
            <div className="min-w-0 flex-1">
                <div className="text-sm text-(--base-09)">{label}</div>
                <div className="text-xs text-(--base-06)">
                    {/* Date AND remaining days. The date answers "when", which is
                        not the question being asked while deciding whether to
                        extend - that one is "how soon". */}
                    {granted
                        ? `Granted until ${new Date(expiresAt).toLocaleDateString()} · ${formatDaysLeft(expiresAt)}`
                        : 'Not granted'}
                </div>
            </div>
            <span className="text-xs text-(--base-06)">for</span>
            <input
                type="number"
                min={1}
                max={730}
                className="input-field input-mono w-20 text-center"
                value={days}
                onChange={e => onDaysChange(e.target.value)}
                aria-label={`Days to grant ${label}`}
            />
            <span className="text-xs text-(--base-06)">days</span>
            <button
                type="button"
                onClick={onGrant}
                disabled={disabled}
                className="btn btn-primary btn-sm disabled:opacity-40"
            >
                {busy ? 'Saving...' : granted ? 'Extend' : 'Grant'}
            </button>
            {granted && (
                <button
                    type="button"
                    onClick={onRevoke}
                    disabled={disabled}
                    className="btn btn-secondary btn-sm disabled:opacity-40"
                >
                    Remove
                </button>
            )}
        </div>
    );
}

function LimitField({ label, value, onChange }: { label: string; value: string; onChange: (v: string) => void }) {
    const ok = value === '' || /^\d+$/.test(value);
    return (
        <div className="flex items-center gap-3">
            <label className="input-label w-48 shrink-0">{label}</label>
            <input
                type="number"
                min={0}
                value={value}
                onChange={e => onChange(e.target.value)}
                placeholder="no limit"
                className={`input-field input-mono w-40 ${ok ? '' : 'border-(--error)'}`}
            />
        </div>
    );
}

function OverrideField({ label, value, onChange, placeholder, valid }: { label: string; value: string; onChange: (v: string) => void; placeholder: string; valid: boolean }) {
    return (
        <div className="flex items-center gap-3">
            <label className="input-label w-48 shrink-0">{label}</label>
            <input
                type="text"
                value={value}
                onChange={e => onChange(e.target.value)}
                placeholder={`default (${placeholder})`}
                className={`input-field input-mono w-40 ${valid ? '' : 'border-(--error)'}`}
            />
        </div>
    );
}
