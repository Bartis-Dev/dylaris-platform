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

interface SettingsTab {
    slug: string;
    label: string;
    // `always: true` bypasses the per-module toggle filter; module-gated tabs
    // carry a `module` name that must be enabled for them to appear.
    always: boolean;
    module?: string;
}

interface SettingsGroup {
    group: string;
    // Group-level feature gate: when set, the whole group (header + items) is
    // hidden unless the named feature flag is on.
    requiresByon?: boolean;
    tabs: SettingsTab[];
}

// Grouped settings nav. Slugs/routes are unchanged from the previous flat list;
// only the presentation moved to a left vertical grouped sidebar. The BYON group
// is hidden unless feature_byon_enabled is on (selfhost installs never see it).
const TAB_GROUPS: SettingsGroup[] = [
    {
        group: 'General',
        tabs: [
            { slug: 'status', label: 'Status', always: true },
            { slug: 'modules', label: 'Modules', always: true },
            { slug: 'features', label: 'Features', always: true },
            { slug: 'maintenance', label: 'Maintenance', always: true },
            { slug: 'database', label: 'Database', always: true },
        ],
    },
    {
        group: 'Access',
        tabs: [
            { slug: 'users', label: 'Users', always: true },
            { slug: 'roles', label: 'Roles', always: true },
        ],
    },
    {
        group: 'Infrastructure',
        tabs: [
            { slug: 'regions', label: 'Regions', always: true },
            { slug: 'nodes', label: 'Nodes', always: true },
            { slug: 'gateway', label: 'Gateway', always: true },
            { slug: 'warp', label: 'Warp', always: true },
            { slug: 'beam', label: 'Beam', always: true },
        ],
    },
    {
        group: 'Storage',
        tabs: [
            { slug: 'core-storage', label: 'Core Storage', always: true },
            { slug: 'storage-connections', label: 'Storage Connections', always: true },
            { slug: 'storage-migration', label: 'Storage Migration', always: true },
            { slug: 'backups', label: 'Backups', always: true },
        ],
    },
    {
        group: 'Servers & Content',
        tabs: [
            { slug: 'servers', label: 'Servers', always: true },
            { slug: 'modpacks', label: 'Modpacks', always: true },
            { slug: 'filemanager', label: 'File Manager', always: true },
        ],
    },
    {
        group: 'Support',
        tabs: [
            { slug: 'ticket-categories', label: 'Ticket Categories', always: true },
            { slug: 'canned-responses', label: 'Canned Responses', always: true },
            { slug: 'tickets', label: 'Ticket Settings', always: true },
            { slug: 'ticket-db', label: 'Ticket DB', always: true },
        ],
    },
    {
        group: 'BYON',
        requiresByon: true,
        tabs: [
            { slug: 'usage', label: 'Usage', always: true },
            { slug: 'billing', label: 'Billing', always: true },
            { slug: 'plans', label: 'Plans', always: true },
        ],
    },
];

// ---------------------------------------------------------------------------
// Inner layout - sits inside the provider so it can read context
// ---------------------------------------------------------------------------

interface PendingNav { href: string }

function SettingsLayoutInner({
    children,
    groups,
}: {
    children: React.ReactNode;
    groups: SettingsGroup[];
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
            <div className="flex-1 flex gap-6 min-h-0 overflow-hidden">
                {/* Left vertical grouped settings sidebar */}
                <nav className="w-52 shrink-0 overflow-y-auto border-r border-(--base-03) pr-3 flex flex-col gap-5 pt-1">
                    {groups.map(group => (
                        <div key={group.group} className="flex flex-col gap-1">
                            <span className="mono-label px-3">{group.group}</span>
                            {group.tabs.map(tab => {
                                const href = `/settings/${tab.slug}`;
                                const isActive =
                                    pathname === href || pathname.startsWith(href + '/');
                                return (
                                    <Link
                                        key={tab.slug}
                                        href={href}
                                        replace
                                        onClick={e => handleTabClick(e, href)}
                                        className={`px-3 py-2 rounded-md text-sm font-medium transition-colors text-left ${
                                            isActive
                                                ? 'bg-(--accent)/10 text-(--accent-light)'
                                                : 'text-(--base-07) hover:text-(--base-09) hover:bg-(--base-03)'
                                        }`}
                                    >
                                        {tab.label}
                                    </Link>
                                );
                            })}
                        </div>
                    ))}
                </nav>

                {/* Content column: routed page + sticky bottom save bar */}
                <div className="flex-1 min-w-0 flex flex-col min-h-0">
                    <div className="flex-1 overflow-y-auto">
                        {children}
                    </div>

                    {/* Sticky bottom save/discard bar - only visible when dirty */}
                    {dirty && (
                        <div className="shrink-0 flex items-center justify-between gap-4 border-t border-(--base-03) bg-(--base-02) px-5 py-3 mt-4">
                            <span className="mono-label text-[11px]">
                                Unsaved changes
                            </span>
                            <div className="flex items-center gap-2">
                                <button
                                    type="button"
                                    onClick={() => registration?.discard()}
                                    className="btn btn-secondary text-xs py-1.5 px-4"
                                    disabled={saving}
                                >
                                    Discard
                                </button>
                                <button
                                    type="button"
                                    onClick={async () => { await registration?.save(); }}
                                    disabled={saving}
                                    className="btn btn-primary text-xs py-1.5 px-4 disabled:opacity-40 inline-flex items-center gap-1.5"
                                >
                                    {saving && <Loader2 size={12} className="animate-spin" />}
                                    Save
                                </button>
                            </div>
                        </div>
                    )}
                </div>
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
    const { user, modules, ready, featureFlags } = useAppData();

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

    // Drop feature-gated groups (BYON only when the flag is on), then filter each
    // group's tabs by the per-module toggle, and finally drop groups left empty.
    const visibleGroups: SettingsGroup[] = TAB_GROUPS
        .filter(g => !g.requiresByon || featureFlags.byon)
        .map(g => ({
            ...g,
            tabs: g.tabs.filter(tab => {
                if (tab.always) return true;
                return modules.some(m => m.name === tab.module && m.isEnabled);
            }),
        }))
        .filter(g => g.tabs.length > 0);

    return (
        <main className="flex-1 flex flex-col overflow-hidden relative z-10 p-6">
            <h1 className="h-page mb-6">System Settings</h1>

            {/* UnsavedChangesProvider is mounted globally in (authed)/layout.tsx
                so its beforeunload + GuardedLink coverage extends beyond the
                Settings module too. We just consume it here. */}
            <SettingsLayoutInner groups={visibleGroups}>
                {children}
            </SettingsLayoutInner>
        </main>
    );
}
