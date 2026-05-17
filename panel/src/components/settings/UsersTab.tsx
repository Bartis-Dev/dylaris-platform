"use client";

import React, { useState, useEffect } from 'react';
import { getUsers, createUser, deleteUser, resetUserPassword, getUserRouteLimit, setUserRouteLimit, User } from '@/lib/api';
import { adminResetTOTP } from '@/lib/api/auth';
import { UserPlus, Settings, X, CircleCheck, CircleAlert, ShieldOff } from 'lucide-react';

interface UsersTabProps {
    currentUser?: User;
}

type RouteMode = 'default' | 'custom' | 'disabled';

export default function UsersTab({ currentUser }: UsersTabProps) {
    const [users, setUsers] = useState<User[]>([]);
    const [isModalOpen, setIsModalOpen] = useState(false);
    const [error, setError] = useState("");
    const [userForm, setUserForm] = useState<Partial<User>>({ username: "", password: "", isAdmin: false });

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
        const res = await createUser(userForm);
        if (res.success) {
            setIsModalOpen(false);
            loadUsers();
        } else setError(res.message || "Error creating user");
    };

    const handleDeleteUser = async (id: number) => {
        if(!confirm("Do you really want to delete this user?")) return;
        await deleteUser(id);
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
                setRouteMax(res.maxRoutes > 0 ? res.maxRoutes : 5);
            }
        } catch {
            setRouteMode('default');
            setRouteMax(5);
        }
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
        if (!confirm(`Reset 2FA for "${settingsUser.username}"? They will be able to log in with just their password until they re-enable 2FA.`)) return;
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
            <div className="flex justify-end mb-4">
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
                        {users.map(u => (
                            <tr key={u.id} className="table-tr table-tr-hover">
                                <td className="table-td font-mono font-medium text-(--base-09)">{u.username}</td>
                                <td className="table-td">
                                    <span className={u.isAdmin ? 'badge badge-accent' : 'badge badge-neutral'}>
                                        {u.isAdmin ? 'ADMIN' : 'USER'}
                                    </span>
                                </td>
                                <td className="table-td text-sm text-(--base-06)">{u.createdAt ? new Date(u.createdAt).toLocaleDateString() : 'N/A'}</td>
                                <td className="table-td text-right">
                                    <div className="flex items-center justify-end gap-2">
                                        <button
                                            onClick={() => openSettings(u)}
                                            className="btn px-2.5 py-1 text-xs bg-(--base-03) border border-(--base-04) text-(--base-07) hover:text-(--base-09) transition-colors"
                                            title="User settings"
                                        >
                                            <Settings size={13} />
                                        </button>
                                        {currentUser?.username !== u.username && (
                                            <button onClick={() => handleDeleteUser(u.id)} className="btn btn-danger btn-sm">Delete</button>
                                        )}
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
                                    <input type="checkbox" checked={userForm.isAdmin} onChange={e => setUserForm({...userForm, isAdmin: e.target.checked})} className="w-4 h-4 accent-(--accent) rounded" />
                                    <label className="text-sm font-medium text-(--base-08)">Administrator Rights</label>
                                </div>
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
        </div>
    );
}
