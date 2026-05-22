"use client";

import React, { useState, useEffect, useRef } from 'react';
import Link from 'next/link';
import { Bell, AlertTriangle, ExternalLink } from 'lucide-react';
import { useAppData } from '@/lib/AppDataContext';

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

interface Notification {
    id: string;
    severity: 'warning' | 'error';
    title: string;
    message: string;
    href?: string;
    cta?: string;
}

// ---------------------------------------------------------------------------
// Checks — each returns a Notification when it has something to report,
// null when everything is fine. Add new checks as the platform grows.
// ---------------------------------------------------------------------------

async function checkBeamRelayMissing(): Promise<Notification | null> {
    try {
        const token = localStorage.getItem('authToken') || localStorage.getItem('token');
        const API_URL = process.env.NEXT_PUBLIC_API_URL || 'http://localhost:25500/api';
        const res = await fetch(`${API_URL}/settings/beam`, {
            headers: { Authorization: `Bearer ${token}` },
        });
        if (!res.ok) return null;
        const data = await res.json();
        if (!data.success || !data.settings) return null;
        const enabled = data.settings.enabled !== false;
        if (!enabled) return null;
        const addr = (
            data.settings.manualOverride ??
            data.settings.relayAddress ??
            ''
        ).toString().trim();
        if (addr) return null;
        return {
            id: 'beam-relay-missing',
            severity: 'warning',
            title: 'Beam relay address not set',
            message:
                'The Beam desktop app gets the relay address from Core after login. Without it, file transfers can\'t connect.',
            href: '/settings/gateway#beam',
            cta: 'Open Beam settings',
        };
    } catch {
        return null;
    }
}

// All registered checks. Run in parallel; null results are dropped.
const CHECKS: Array<() => Promise<Notification | null>> = [
    checkBeamRelayMissing,
];

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

export default function NotificationsDropdown() {
    const { user } = useAppData();
    const [open, setOpen] = useState(false);
    const [items, setItems] = useState<Notification[]>([]);
    const wrapRef = useRef<HTMLDivElement>(null);

    const isAdmin = user?.isAdmin ?? false;

    const refresh = async () => {
        const results = await Promise.all(CHECKS.map(c => c()));
        setItems(results.filter((n): n is Notification => n !== null));
    };

    // Poll periodically so newly-introduced issues (e.g. an admin clears the
    // relay address) light up without a page reload, but cheap enough that
    // it doesn't matter.
    useEffect(() => {
        if (!isAdmin) return;
        refresh();
        const t = setInterval(refresh, 30000);
        return () => clearInterval(t);
    }, [isAdmin]);

    // Click-outside closes the dropdown — matches the profile dropdown's
    // behavior in the same navbar.
    useEffect(() => {
        const handler = (e: MouseEvent) => {
            if (!wrapRef.current?.contains(e.target as Node)) setOpen(false);
        };
        document.addEventListener('click', handler);
        return () => document.removeEventListener('click', handler);
    }, []);

    if (!isAdmin) return null;

    const count = items.length;
    const hasItems = count > 0;

    return (
        <div ref={wrapRef} className="relative mr-2">
            <button
                type="button"
                onClick={() => setOpen(o => !o)}
                title={hasItems ? `${count} notification${count !== 1 ? 's' : ''}` : 'No notifications'}
                className={`relative flex items-center justify-center w-9 h-9 rounded-md transition-colors border ${
                    open
                        ? 'bg-(--base-03) border-(--base-04) text-(--base-09)'
                        : hasItems
                            ? 'bg-(--warning-ghost) text-(--warning-light) border-(--warning-border) hover:bg-(--warning-ghost)/80'
                            : 'text-(--base-07) hover:bg-(--base-04)/50 hover:text-(--base-09) border-transparent'
                }`}
            >
                <Bell size={18} />
                {hasItems && (
                    <span className="absolute -top-1 -right-1 min-w-[16px] h-[16px] px-1 rounded-full bg-(--warning-light) text-(--base-00) text-[10px] font-mono font-bold flex items-center justify-center leading-none">
                        {count}
                    </span>
                )}
            </button>

            {open && (
                <div className="dropdown-menu right-0 mt-3 w-80 animate-fade-in origin-top-right">
                    <div className="px-4 py-2 border-b border-(--base-03)">
                        <div className="font-mono text-[10px] uppercase tracking-[0.08em] text-(--base-06)">
                            Notifications
                        </div>
                    </div>
                    {items.length === 0 ? (
                        <div className="px-4 py-6 text-sm text-(--base-06) text-center">
                            All clear — nothing to report.
                        </div>
                    ) : (
                        <div className="py-1 max-h-80 overflow-y-auto">
                            {items.map(n => (
                                <div
                                    key={n.id}
                                    className="px-3 py-2.5 hover:bg-(--base-03) transition-colors"
                                >
                                    <div className="flex items-start gap-2">
                                        <AlertTriangle
                                            size={14}
                                            className={`mt-0.5 shrink-0 ${
                                                n.severity === 'error'
                                                    ? 'text-(--error-light)'
                                                    : 'text-(--warning-light)'
                                            }`}
                                        />
                                        <div className="min-w-0 flex-1">
                                            <div className="text-sm font-medium text-(--base-09)">
                                                {n.title}
                                            </div>
                                            <div className="text-xs text-(--base-06) mt-0.5 leading-snug">
                                                {n.message}
                                            </div>
                                            {n.href && (
                                                <Link
                                                    href={n.href}
                                                    onClick={() => setOpen(false)}
                                                    className="inline-flex items-center gap-1 mt-2 text-xs text-(--accent-light) hover:underline"
                                                >
                                                    {n.cta || 'Go to fix'}
                                                    <ExternalLink size={11} />
                                                </Link>
                                            )}
                                        </div>
                                    </div>
                                </div>
                            ))}
                        </div>
                    )}
                </div>
            )}
        </div>
    );
}
