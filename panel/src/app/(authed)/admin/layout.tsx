"use client";

import React from 'react';
import Link from 'next/link';
import { usePathname } from 'next/navigation';

const TABS = [
    { slug: 'servers', label: 'All Servers' },
    { slug: 'disk', label: 'Disk Analysis' },
];

export default function AdminLayout({ children }: { children: React.ReactNode }) {
    const pathname = usePathname();

    return (
        <main className="flex-1 flex flex-col overflow-hidden relative z-10 p-6">
            <div className="flex items-center justify-between mb-6">
                <div>
                    <h2 className="text-xl font-display font-bold text-(--base-09)">Admin</h2>
                    <p className="text-sm text-(--base-06)">Server management and storage diagnostics</p>
                </div>
            </div>

            <div className="flex gap-1 mb-4 border-b border-(--base-03) pb-0">
                {TABS.map(t => {
                    const href = `/admin/${t.slug}`;
                    const isActive = pathname === href || pathname.startsWith(href + '/');
                    return (
                        <Link
                            key={t.slug}
                            href={href}
                            replace
                            className={`px-4 py-2 text-sm font-medium transition-colors border-b-2 -mb-px ${
                                isActive
                                    ? 'border-(--accent) text-(--accent-light)'
                                    : 'border-transparent text-(--base-06) hover:text-(--base-08)'
                            }`}
                        >
                            {t.label}
                        </Link>
                    );
                })}
            </div>

            <div className="flex-1 min-h-0 overflow-auto">
                {children}
            </div>
        </main>
    );
}
