"use client";

import React, { useEffect, useState, useCallback } from 'react';
import Link from 'next/link';
import { usePathname, useRouter } from 'next/navigation';
import { useAppData } from '@/lib/AppDataContext';
import { Loader2 } from 'lucide-react';
import {
    useUnsavedChangesState,
    UnsavedDialog,
} from '@/components/settings/UnsavedChanges';

const ALL_TABS = [
    { slug: 'status', label: 'Status', always: true },
    { slug: 'modules', label: 'Modules', always: true },
    { slug: 'users', label: 'Users', always: true },
    { slug: 'user-management', label: 'User Management', always: true },
    { slug: 'regions', label: 'Regions', always: true },
    { slug: 'nodes', label: 'Nodes', always: true },
    { slug: 'library', label: 'Library', always: true },
    { slug: 'modpacks', label: 'Modpacks', always: true },
    { slug: 'filemanager', label: 'File Manager', always: true },
    { slug: 'servers', label: 'Servers', always: true },
    { slug: 'features', label: 'Features', always: true },
    // Gateway config (hoster domains, route limits, XDP, etc.) stays
    // available as a settings tab even though the standalone Gateway module
    // was retired — admins still need to configure the feature before
    // toggling it on from the Features tab.
    { slug: 'gateway', label: 'Gateway', always: true },
    { slug: 'warp', label: 'Warp', always: true },
    { slug: 'usage', label: 'Usage', always: true },
    { slug: 'backups', label: 'Backups', always: true },
    { slug: 'maintenance', label: 'Maintenance', always: true },
    { slug: 'database', label: 'Database', always: true },
    { slug: 'ticket-categories', label: 'Ticket Categories', always: true },
    { slug: 'canned-responses', label: 'Canned Responses', always: true },
    { slug: 'tickets', label: 'Ticket Settings', always: true },
    { slug: 'ticket-db', label: 'Ticket DB', always: true },
] as const;

// ---------------------------------------------------------------------------
// Inner layout — sits inside the provider so it can read context
// ---------------------------------------------------------------------------

interface PendingNav { href: string }

function SettingsLayoutInner({
    children,
    visibleTabs,
}: {
    children: React.ReactNode;
    visibleTabs: typeof ALL_TABS[number][];
}) {
    const router = useRouter();
    const pathname = usePathname();
    const registration = useUnsavedChangesState();

    // Pending navigation while the confirm dialog is open.
    const [pendingNav, setPendingNav] = useState<PendingNav | null>(null);
    const [dialogSaving, setDialogSaving] = useState(false);

    const dirty = registration?.dirty ?? false;
    const saving = registration?.saving ?? false;

    // Intercept tab clicks when dirty.
    const handleTabClick = useCallback(
        (e: React.MouseEvent<HTMLAnchorElement>, href: string) => {
            const isCurrentTab =
                pathname === href || pathname.startsWith(href + '/');
            if (!dirty || isCurrentTab) return; // let it navigate normally
            e.preventDefault();
            setPendingNav({ href });
        },
        [dirty, pathname],
    );

    const closeDialog = () => {
        setPendingNav(null);
        setDialogSaving(false);
    };

    const handleDialogSave = async () => {
        if (!registration || !pendingNav) return;
        setDialogSaving(true);
        try {
            await registration.save();
        } finally {
            setDialogSaving(false);
        }
        const href = pendingNav.href;
        closeDialog();
        router.replace(href);
    };

    const handleDialogDiscard = () => {
        if (!registration || !pendingNav) return;
        registration.discard();
        const href = pendingNav.href;
        closeDialog();
        router.replace(href);
    };

    return (
        <>
            {/* Tab bar + sticky save bar row */}
            <div className="relative flex items-end justify-between gap-4 border-b border-(--base-04) mb-6">
                {/* Tabs */}
                <div className="flex gap-4 overflow-x-auto hide-scrollbar">
                    {visibleTabs.map(tab => {
                        const href = `/settings/${tab.slug}`;
                        const isActive =
                            pathname === href || pathname.startsWith(href + '/');
                        return (
                            <Link
                                key={tab.slug}
                                href={href}
                                replace
                                onClick={e => handleTabClick(e, href)}
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

                {/* Sticky save/discard bar — only visible when dirty */}
                {dirty && (
                    <div className="sticky right-0 top-0 flex items-center gap-2 pb-2 shrink-0">
                        <span className="font-mono text-[11px] uppercase tracking-[0.08em] text-(--base-06) whitespace-nowrap">
                            Unsaved changes
                        </span>
                        <button
                            type="button"
                            onClick={() => registration?.discard()}
                            className="btn btn-secondary text-xs py-1 px-3"
                            disabled={saving}
                        >
                            Discard
                        </button>
                        <button
                            type="button"
                            onClick={async () => { await registration?.save(); }}
                            disabled={saving}
                            className="btn btn-primary text-xs py-1 px-3 disabled:opacity-40 inline-flex items-center gap-1.5"
                        >
                            {saving && <Loader2 size={12} className="animate-spin" />}
                            Save
                        </button>
                    </div>
                )}
            </div>

            {/* Page content */}
            <div className="flex-1 overflow-y-auto">
                {children}
            </div>

            {/* Tab-switch confirm dialog */}
            {pendingNav && registration && (
                <UnsavedDialog
                    onSave={handleDialogSave}
                    onDiscard={handleDialogDiscard}
                    onCancel={closeDialog}
                    saving={dialogSaving}
                />
            )}
        </>
    );
}

// ---------------------------------------------------------------------------
// Root layout export
// ---------------------------------------------------------------------------

export default function SettingsLayout({ children }: { children: React.ReactNode }) {
    const router = useRouter();
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
            <h1 className="h-page mb-6">System Settings</h1>

            {/* UnsavedChangesProvider is mounted globally in (authed)/layout.tsx
                so its beforeunload + GuardedLink coverage extends beyond the
                Settings module too. We just consume it here. */}
            <SettingsLayoutInner visibleTabs={visibleTabs as unknown as typeof ALL_TABS[number][]}>
                {children}
            </SettingsLayoutInner>
        </main>
    );
}
