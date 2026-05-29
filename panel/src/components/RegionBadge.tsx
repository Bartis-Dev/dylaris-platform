"use client";

import React from 'react';
import { Globe } from 'lucide-react';
import { useAppData } from '@/lib/AppDataContext';
import type { Region } from '@/lib/api/regions';

interface RegionBadgeProps {
    /** Region ID (e.g. 'default', 'eu', 'us-east'). When missing or 'default'
     *  in a single-region setup the badge auto-hides. */
    region?: string;
    /** Show the friendly display name instead of the ID. */
    displayName?: boolean;
    /** Size variant — 'xs' is the chip you'd put next to a status dot,
     *  'sm' is the standalone label. */
    size?: 'xs' | 'sm';
    /** Optional className for parent-driven positioning. */
    className?: string;
    /** Force show even when single-region 'default' would normally auto-hide. */
    alwaysShow?: boolean;
}

// RegionBadge renders a compact pill for a region. It consults the
// AppDataContext-cached region list to colour itself per the admin-configured
// region (color + display name). When the single-region-setup heuristic
// applies (only the 'default' region exists), the badge auto-hides — no
// visual clutter for setups where region is meaningless.
export default function RegionBadge({ region, displayName, size = 'xs', className, alwaysShow }: RegionBadgeProps) {
    const { regions } = useAppData();

    if (!region) return null;
    // Auto-hide in single-region setups unless explicitly forced.
    if (!alwaysShow && region === 'default' && regions.length <= 1) return null;

    const entry: Region | undefined = regions.find(r => r.id === region);
    const label = displayName && entry?.displayName ? entry.displayName : region;
    const color = entry?.color;

    const baseCls = size === 'sm'
        ? 'px-2 py-0.5 text-xs'
        : 'px-1.5 py-0.5 text-[10px]';

    return (
        <span
            className={`inline-flex items-center gap-1 rounded-sm font-mono uppercase tracking-[0.06em] border bg-(--base-03) text-(--base-07) border-(--base-04) ${baseCls} ${className || ''}`}
            title={entry?.displayName ? `Region: ${entry.displayName}` : `Region: ${region}`}
        >
            {color && (
                <span
                    className="inline-block w-1.5 h-1.5 rounded-full shrink-0"
                    style={{ background: color }}
                    aria-hidden
                />
            )}
            {!color && <Globe size={10} className="shrink-0 text-(--base-06)" />}
            <span className="truncate max-w-[80px]">{label}</span>
        </span>
    );
}
