"use client";

import React from 'react';

// Every status a server can actually be reported in. The three below the
// blank line were producing the neutral fallback, which on the admin list -
// the one place an operator scans for trouble - rendered a server held for a
// full disk exactly like one the owner had simply stopped. The Sidebar and the
// server layout already treat disk_full as an error state; this matches them.
export const STATUS_CLASSES: Record<string, string> = {
    online: 'bg-(--success-ghost) text-(--success-light) border-(--success-border)',
    offline: 'bg-(--base-03) text-(--base-06) border-(--base-04)',
    stopped: 'bg-(--base-03) text-(--base-06) border-(--base-04)',
    installing: 'bg-(--warning-ghost) text-(--warning) border-(--warning-border)',
    pending_setup: 'bg-(--base-03) text-(--base-05) border-(--base-03)',
    starting: 'bg-(--warning-ghost) text-(--warning) border-(--warning-border)',
    stopping: 'bg-(--warning-ghost) text-(--warning) border-(--warning-border)',
    suspended: 'bg-(--error-ghost) text-(--error-light) border-(--error-border)',

    disk_full: 'bg-(--error-ghost) text-(--error-light) border-(--error-border)',
    restarting: 'bg-(--warning-ghost) text-(--warning) border-(--warning-border)',
    migrating: 'bg-(--warning-ghost) text-(--warning) border-(--warning-border)',
};

export function StatusBadge({ status }: { status: string }) {
    const cls = STATUS_CLASSES[status] ?? 'bg-(--base-03) text-(--base-06) border-(--base-04)';
    return (
        <span className={`inline-flex items-center px-1.5 py-0.5 rounded text-[10px] font-mono border ${cls}`}>
            {status}
        </span>
    );
}
