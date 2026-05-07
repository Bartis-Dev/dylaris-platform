"use client";

import UsersTab from '@/components/settings/UsersTab';
import { useAppData } from '@/lib/AppDataContext';

export default function SettingsUsersPage() {
    const { user } = useAppData();
    return <UsersTab currentUser={user ?? undefined} />;
}
