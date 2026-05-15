"use client";

import React, { useEffect } from 'react';
import Link from 'next/link';
import { usePathname, useRouter } from 'next/navigation';
import { useAppData } from '@/lib/AppDataContext';

const ALL_TABS = [
    { slug: 'modules', label: 'Modules', always: true },
    { slug: 'users', label: 'Users', always: true },
    { slug: 'nodes', label: 'Nodes', always: true },
    { slug: 'library', label: 'Library', always: true },
    { slug: 'filemanager', label: 'File Manager', always: true },
    { slug: 'servers', label: 'Servers', always: true },
    { slug: 'features', label: 'Features', always: true },
    // Gateway config (hoster domains, route limits, XDP, etc.) stays
    // available as a settings tab even though the standalone Gateway module
    // was retired — admins still need to configure the feature before
    // toggling it on from the Features tab.
    { slug: 'gateway', label: 'Gateway', always: true },
] as const;

export default function SettingsLayout({ children }: { children: React.ReactNode }) {
    const router = useRouter();
    const pathname = usePathname();
    const { user, modules, ready } = useAppData();

    // Admin-only gate
    useEffect(() => {
        if (!ready) return;
        if (!user?.isAdmin) router.replace('/servers');
    }, [ready, user?.isAdmin, router]);

    if (!ready) return null;
    if (!user?.isAdmin) {
        return (
            <main className="flex-1 flex items-center justify-center text-(--error) font-semibold text-xl font-display">
                Access denied. Administrator rights required.
            </main>
        );
    }

    const visibleTabs = ALL_TABS.filter(tab => {
        if (tab.always) return true;
        return modules.some(m => m.name === (tab as any).module && m.isEnabled);
    });

    return (
        <main className="flex-1 flex flex-col overflow-hidden relative z-10 p-6">
            <h1 className="text-3xl font-display font-bold text-(--accent) mb-6">System Settings</h1>

            <div className="flex gap-4 border-b border-(--base-04) mb-6 overflow-x-auto hide-scrollbar">
                {visibleTabs.map(tab => {
                    const href = `/settings/${tab.slug}`;
                    const isActive = pathname === href || pathname.startsWith(href + '/');
                    return (
                        <Link
                            key={tab.slug}
                            href={href}
                            replace
                            className={`px-4 py-2 font-mono text-sm font-medium border-b-2 transition-colors whitespace-nowrap ${
                                isActive
                                    ? 'border-(--accent) text-(--accent-light)'
                                    : 'border-transparent text-(--base-06) hover:text-(--base-09)'
                            }`}
                        >
                            {tab.label}
                        </Link>
                    );
                })}
            </div>

            <div className="flex-1 overflow-y-auto">
                {children}
            </div>
        </main>
    );
}
