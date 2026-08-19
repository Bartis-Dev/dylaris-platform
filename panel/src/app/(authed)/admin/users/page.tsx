"use client";

import Link from 'next/link';
import { Settings } from 'lucide-react';
import UsersTab from '@/components/settings/UsersTab';
import { useAppData } from '@/lib/AppDataContext';

// The roster lives here rather than under Settings: it is a list of PEOPLE, and
// Settings is where the platform is configured. The rules about how accounts
// behave (registration, password policy, renames) stayed behind, so each page
// answers one question - and links to the other, because whoever is on one
// regularly wants the other next.
export default function AdminUsersPage() {
    const { user } = useAppData();

    return (
        <div className="max-w-5xl">
            <div className="flex flex-wrap items-start justify-between gap-3 mb-4">
                <div className="min-w-0">
                    <h3 className="text-base font-display font-semibold text-(--base-09)">Users</h3>
                    <p className="text-sm text-(--base-06)">Who has an account here, and what they can do.</p>
                </div>
                <Link href="/settings/users" className="btn btn-secondary btn-sm shrink-0">
                    <Settings size={13} /> User settings
                </Link>
            </div>
            <UsersTab currentUser={user ?? undefined} />
        </div>
    );
}
