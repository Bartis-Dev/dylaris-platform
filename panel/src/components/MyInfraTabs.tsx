"use client";

import Link from 'next/link';
import { usePathname } from 'next/navigation';
import { HardDrive, Globe } from 'lucide-react';

// Machines and protected addresses are two halves of one thing a tenant owns,
// and they were reachable from two unrelated places: /nodes from the sidebar,
// /routes from the account dropdown next to Edit Profile and Logout. Nobody
// looks for a product surface under their own name, so route-only was
// effectively hidden from the people entitled to it.
//
// They stay separate PAGES - a machine and an address are different objects
// with different lists - but share this bar, so each is one click from the
// other.
const TABS = [
    { href: '/nodes', label: 'My machines', icon: HardDrive },
    { href: '/routes', label: 'Protected addresses', icon: Globe },
];

export default function MyInfraTabs({ showRoutes }: { showRoutes: boolean }) {
    const pathname = usePathname();
    // With gateway routing off there is no route-only product, so a second tab
    // would lead to a page that renders nothing.
    const tabs = showRoutes ? TABS : TABS.slice(0, 1);
    if (tabs.length < 2) return null;

    return (
        <div className="flex gap-1 border-b border-(--base-03)">
            {tabs.map(t => {
                const active = pathname === t.href || pathname.startsWith(t.href + '/');
                const Icon = t.icon;
                return (
                    <Link
                        key={t.href}
                        href={t.href}
                        className={`flex items-center gap-2 px-4 py-2 text-sm font-medium border-b-2 -mb-px transition-colors ${
                            active
                                ? 'border-(--accent) text-(--accent-light)'
                                : 'border-transparent text-(--base-06) hover:text-(--base-08)'
                        }`}
                    >
                        <Icon size={14} />
                        {t.label}
                    </Link>
                );
            })}
        </div>
    );
}
