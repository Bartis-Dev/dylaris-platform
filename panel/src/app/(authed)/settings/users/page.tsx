"use client";

import Link from 'next/link';
import { Users } from 'lucide-react';
import UserManagementTab from '@/components/settings/UserManagementTab';
import AccountPolicyCard from '@/components/settings/AccountPolicyCard';

// Rules about accounts, not the accounts themselves. The roster moved to
// /admin/users - this page had been both, and a page that is a list of people
// AND a form of policy switches makes the reader work out which half they are
// looking at. Each side links to the other.
export default function SettingsUsersPage() {
    return (
        <div className="flex flex-col h-full">
            <div className="flex flex-wrap items-start justify-between gap-3 mb-6">
                <div className="min-w-0">
                    <h2 className="text-lg font-display text-(--base-09)">User settings</h2>
                    <p className="text-sm text-(--base-06)">
                        How accounts behave here: registration, sign-in, password reset and renames.
                    </p>
                </div>
                <Link href="/admin/users" className="btn btn-secondary btn-sm shrink-0">
                    <Users size={13} /> Manage user accounts
                </Link>
            </div>

            <div className="flex-1 min-h-0 space-y-8">
                <UserManagementTab />
                <div className="max-w-3xl">
                    <AccountPolicyCard />
                </div>
            </div>
        </div>
    );
}
