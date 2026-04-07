"use client";

import { useState, useEffect, useCallback } from 'react';
import { Server, ServerInvite, TabPermissions, getServerMembers, getInheritedMembers, inviteServerMember, updateMemberPermissions, removeServerMember } from '@/lib/api';
import { UserPlus, Info, Users, UserMinus, ShieldAlert, ShieldCheck } from 'lucide-react';

interface MembersViewProps {
    server: Server;
}

const PERMISSION_ENTRIES: { key: keyof TabPermissions; label: string; proxyOnly?: boolean }[] = [
    { key: 'power', label: 'Power' },
    { key: 'setup', label: 'Setup' },
    { key: 'console', label: 'Console' },
    { key: 'files', label: 'Files' },
    { key: 'config', label: 'Configuration' },
    { key: 'network', label: 'Network' },
    { key: 'members', label: 'Members' },
    { key: 'inherit', label: 'Inherit', proxyOnly: true },
];

export default function MembersView({ server }: MembersViewProps) {
    const [members, setMembers] = useState<ServerInvite[]>([]);
    const [inheritedMembers, setInheritedMembers] = useState<{ userId: number; username: string; permissions: TabPermissions; proxyId: number }[]>([]);
    const [loading, setLoading] = useState(true);
    const [inviteUsername, setInviteUsername] = useState('');
    const [inviteError, setInviteError] = useState('');
    const [inviteSuccess, setInviteSuccess] = useState('');
    const [confirmRemove, setConfirmRemove] = useState<number | null>(null);
    const [confirmMembersPerm, setConfirmMembersPerm] = useState<ServerInvite | null>(null);

    const isOwner = server.role !== 'invited' && server.role !== 'inherited';
    const myPerms = server.permissions;

    const loadMembers = useCallback(async () => {
        const result = await getServerMembers(server.id);
        if (result.success && result.members) {
            setMembers(result.members);
        }
        // Load inherited members for child servers
        if (server.proxyId) {
            const inherited = await getInheritedMembers(server.id);
            if (inherited.success && inherited.members) {
                setInheritedMembers(inherited.members);
            }
        }
        setLoading(false);
    }, [server.id, server.proxyId]);

    useEffect(() => {
        loadMembers();
    }, [loadMembers]);

    const handleInvite = async () => {
        setInviteError('');
        setInviteSuccess('');
        const username = inviteUsername.trim();
        if (!username) { setInviteError('Username required'); return; }

        // If invited user, cap permissions to own permissions
        const permissions: Partial<TabPermissions> | undefined = !isOwner && myPerms
            ? Object.fromEntries(PERMISSION_ENTRIES.map(e => [e.key, myPerms[e.key] ?? false])) as Partial<TabPermissions>
            : undefined;

        const result = await inviteServerMember(server.id, username, permissions);
        if (result.success) {
            setInviteUsername('');
            setInviteSuccess(`${username} invited successfully`);
            setTimeout(() => setInviteSuccess(''), 3000);
            loadMembers();
        } else {
            setInviteError(result.error || result.message || 'Failed to invite user');
        }
    };

    const handleTogglePermission = async (member: ServerInvite, perm: keyof TabPermissions) => {
        if (perm === 'members' && !member.permissions.members) {
            setConfirmMembersPerm(member);
            return;
        }
        const newPerms = { ...member.permissions, [perm]: !member.permissions[perm] };
        await updateMemberPermissions(server.id, member.userId, newPerms);
        loadMembers();
    };

    const handleConfirmMembersPerm = async () => {
        if (!confirmMembersPerm) return;
        const newPerms = { ...confirmMembersPerm.permissions, members: true };
        await updateMemberPermissions(server.id, confirmMembersPerm.userId, newPerms);
        setConfirmMembersPerm(null);
        loadMembers();
    };

    const handleRemove = async (userId: number) => {
        setMembers(prev => prev.filter(m => m.userId !== userId));
        setConfirmRemove(null);
        await removeServerMember(server.id, userId);
        loadMembers();
    };

    // Determine which permissions the current user can grant/toggle
    const canTogglePerm = (perm: keyof TabPermissions) => {
        if (isOwner) return true;
        return myPerms ? myPerms[perm] : false;
    };

    if (loading) {
        return <div className="flex items-center justify-center h-64 text-(--base-07)">Loading members...</div>;
    }

    return (
        <div className="max-w-3xl mx-auto">
            {/* Invite Section */}
            <div className="card p-6 mb-6">
                <h2 className="modal-title mb-4 flex items-center gap-2">
                    <UserPlus size={20} />
                    Invite Member
                </h2>
                <div className="flex gap-3">
                    <input
                        type="text"
                        placeholder="Enter username..."
                        value={inviteUsername}
                        onChange={e => setInviteUsername(e.target.value)}
                        onKeyDown={e => e.key === 'Enter' && handleInvite()}
                        className="input-field flex-1 placeholder:text-(--base-06)"
                    />
                    <button
                        onClick={handleInvite}
                        className="btn btn-primary px-6 py-2.5 text-sm"
                    >
                        Invite
                    </button>
                </div>
                {inviteError && <p className="text-(--error-light) text-sm mt-2">{inviteError}</p>}
                {inviteSuccess && <p className="text-(--success-light) text-sm mt-2">{inviteSuccess}</p>}
            </div>

            {/* Info Note */}
            <div className="flex items-center gap-2 bg-(--base-03) border border-(--base-04) rounded-xl px-4 py-3 mb-6">
                <Info size={16} className="text-(--base-06)" />
                <span className="text-sm text-(--base-07)">
                    Overview tab is always available for all invited members.
                </span>
            </div>

            {/* Members List */}
            <div className="card p-6">
                <h2 className="modal-title mb-4 flex items-center gap-2">
                    <Users size={20} />
                    Members ({members.length})
                </h2>

                {members.length === 0 ? (
                    <p className="text-(--base-06) text-sm italic">No members invited yet.</p>
                ) : (
                    <div className="space-y-3">
                        {members.map(member => (
                            <div key={member.id} className="bg-(--base-03) rounded-lg p-4 border border-(--base-04)">
                                <div className="flex items-center justify-between mb-3">
                                    <div>
                                        <span className="font-medium text-(--base-09)">{member.username}</span>
                                        {member.email && (
                                            <span className="text-(--base-07) text-sm ml-2">{member.email}</span>
                                        )}
                                        <span className="text-xs text-(--base-06) ml-2">
                                            invited by {member.inviterName}
                                        </span>
                                    </div>
                                    {confirmRemove === member.userId ? (
                                        <div className="flex gap-2">
                                            <button
                                                onClick={() => handleRemove(member.userId)}
                                                className="btn btn-danger px-3 py-1 text-xs"
                                            >
                                                Confirm
                                            </button>
                                            <button
                                                onClick={() => setConfirmRemove(null)}
                                                className="btn btn-secondary px-3 py-1 text-xs"
                                            >
                                                Cancel
                                            </button>
                                        </div>
                                    ) : (
                                        <button
                                            onClick={() => setConfirmRemove(member.userId)}
                                            className="text-(--error-light) hover:text-(--error) transition-colors"
                                            title="Remove member"
                                        >
                                            <UserMinus size={20} />
                                        </button>
                                    )}
                                </div>
                                <div className="flex flex-wrap gap-2">
                                    {PERMISSION_ENTRIES.filter(e => !e.proxyOnly || server.serverType === 'proxy').map(({ key, label }) => {
                                        const canToggle = canTogglePerm(key);
                                        return (
                                            <label key={key} className={`flex items-center gap-1.5 select-none ${canToggle ? 'cursor-pointer' : 'opacity-40 cursor-not-allowed'}`}>
                                                <input
                                                    type="checkbox"
                                                    checked={member.permissions[key] ?? false}
                                                    onChange={() => canToggle && handleTogglePermission(member, key)}
                                                    disabled={!canToggle}
                                                    className="w-4 h-4 rounded accent-(--accent)"
                                                />
                                                <span className="text-sm text-(--base-09)">{label}</span>
                                            </label>
                                        );
                                    })}
                                </div>
                            </div>
                        ))}
                    </div>
                )}
            </div>

            {/* Inherited Members Section (for child servers) */}
            {server.proxyId && inheritedMembers.length > 0 && (
                <div className="card p-6 mt-6">
                    <h2 className="modal-title mb-3 flex items-center gap-2">
                        <ShieldCheck size={20} className="text-(--accent-light)" />
                        Inherited Members
                    </h2>
                    <div className="flex items-center gap-2 bg-(--base-03) border border-(--base-04) rounded-lg px-4 py-3 mb-4">
                        <Info size={16} className="text-(--base-06) shrink-0" />
                        <span className="text-sm text-(--base-07)">
                            These users have access through the linked proxy server. Manage their permissions on the proxy.
                        </span>
                    </div>
                    <div className="space-y-3">
                        {inheritedMembers.map(member => (
                            <div key={member.userId} className="bg-(--base-03)/50 rounded-lg p-4 border border-(--base-04) opacity-75">
                                <div className="flex items-center justify-between mb-3">
                                    <div>
                                        <span className="font-medium text-(--base-08)">{member.username}</span>
                                        <span className="text-[10px] font-mono text-(--accent-light) ml-2 uppercase tracking-wider">inherited</span>
                                    </div>
                                </div>
                                <div className="flex flex-wrap gap-2">
                                    {PERMISSION_ENTRIES.filter(e => !e.proxyOnly).map(({ key, label }) => (
                                        <label key={key} className="flex items-center gap-1.5 opacity-50 cursor-not-allowed select-none">
                                            <input
                                                type="checkbox"
                                                checked={member.permissions[key] ?? false}
                                                disabled
                                                className="w-4 h-4 rounded accent-(--accent)"
                                            />
                                            <span className="text-sm text-(--base-08)">{label}</span>
                                        </label>
                                    ))}
                                </div>
                            </div>
                        ))}
                    </div>
                </div>
            )}

            {/* Confirm Members Permission Modal */}
            {confirmMembersPerm && (
                <div className="modal-overlay animate-fade-in">
                    <div className="modal-panel w-96">
                        <div className="modal-header">
                            <h3 className="modal-title flex items-center gap-2 text-(--warning-light)">
                                <ShieldAlert size={18} />
                                Grant Members Permission?
                            </h3>
                        </div>
                        <div className="modal-body">
                            <p className="text-sm text-(--base-07) mb-3">
                                Granting the <span className="font-semibold text-(--base-09)">Members</span> permission to <span className="font-semibold text-(--base-09)">{confirmMembersPerm.username}</span> will allow them to:
                            </p>
                            <ul className="text-sm text-(--base-07) space-y-1.5 list-disc list-inside">
                                <li>Invite other users to this server</li>
                                <li>Remove other users from this server</li>
                                <li>Grant and revoke permissions (only those they have themselves)</li>
                            </ul>
                        </div>
                        <div className="modal-footer">
                            <button
                                onClick={() => setConfirmMembersPerm(null)}
                                className="btn btn-secondary px-4 py-2 text-sm"
                            >
                                Cancel
                            </button>
                            <button
                                onClick={handleConfirmMembersPerm}
                                className="btn btn-primary px-4 py-2 text-sm"
                            >
                                Confirm
                            </button>
                        </div>
                    </div>
                </div>
            )}
        </div>
    );
}
