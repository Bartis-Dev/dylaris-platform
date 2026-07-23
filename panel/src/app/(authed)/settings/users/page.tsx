"use client";

import { useState } from 'react';
import UsersTab from '@/components/settings/UsersTab';
import UserManagementTab from '@/components/settings/UserManagementTab';
import { useAppData } from '@/lib/AppDataContext';

type SubTab = 'users' | 'settings';

const SUBTABS: { id: SubTab; label: string }[] = [
    { id: 'users', label: 'Users' },
    { id: 'settings', label: 'User Settings' },
];

export default function SettingsUsersPage() {
    const { user } = useAppData();
    // Folds the former standalone "User Management" tab in as a subtab of Users.
    const [subTab, setSubTab] = useState<SubTab>('users');

    return (
        <div className="flex flex-col h-full">
            <div className="flex gap-4 border-b border-(--base-04) mb-6">
                {SUBTABS.map(t => (
                    <button
                        key={t.id}
                        type="button"
                        onClick={() => setSubTab(t.id)}
                        className={`px-4 py-2 font-mono text-sm font-medium border-b-2 transition-colors whitespace-nowrap ${
                            subTab === t.id
                                ? 'border-(--accent) text-(--accent-light)'
                                : 'border-transparent text-(--base-06) hover:text-(--base-09)'
                        }`}
                    >
                        {t.label}
                    </button>
                ))}
            </div>

            <div className="flex-1 min-h-0">
                {subTab === 'users' && <UsersTab currentUser={user ?? undefined} />}
                {subTab === 'settings' && <UserManagementTab />}
            </div>
        </div>
    );
}
