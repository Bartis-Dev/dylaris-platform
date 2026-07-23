"use client";

import { useEffect } from 'react';
import { useRouter } from 'next/navigation';

// User Management is now a subtab of Users. Keep this route alive as a thin
// redirect so any bookmarked/deep link still resolves.
export default function SettingsUserManagementRedirect() {
    const router = useRouter();
    useEffect(() => { router.replace('/settings/users'); }, [router]);
    return null;
}
