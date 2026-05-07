"use client";

import React from 'react';

const STATUS_CLASSES: Record<string, string> = {
    online: 'bg-(--success-ghost) text-(--success-light) border-(--success-border)',
    offline: 'bg-(--base-03) text-(--base-06) border-(--base-04)',
    stopped: 'bg-(--base-03) text-(--base-06) border-(--base-04)',
    installing: 'bg-(--warning-ghost) text-(--warning) border-(--warning-border)',
    pending_setup: 'bg-(--base-03) text-(--base-05) border-(--base-03)',
    starting: 'bg-(--warning-ghost) text-(--warning) border-(--warning-border)',
    stopping: 'bg-(--warning-ghost) text-(--warning) border-(--warning-border)',
    suspended: 'bg-(--error-ghost) text-(--error-light) border-(--error-border)',
};

export function StatusBadge({ status }: { status: string }) {
    const cls = STATUS_CLASSES[status] ?? 'bg-(--base-03) text-(--base-06) border-(--base-04)';
    return (
        <span className={`inline-flex items-center px-1.5 py-0.5 rounded text-[10px] font-mono border ${cls}`}>
            {status}
        </span>
    );
}
