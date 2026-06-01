"use client";

import React from 'react';

// Skeleton primitives — drop-in replacements for spinner-only loading states.
// All variants use Tailwind's animate-pulse with the panel's --base-03 token
// so the placeholders blend with cards/inputs and inherit the brand palette.
//
// Use these instead of <LoadingState /> wherever the eventual content has a
// predictable shape (table rows, cards, stat tiles). The result is no
// "spinner → instant content reflow" jank.

interface BaseProps {
    className?: string;
}

/** Single pulsing block. Pass width/height + rounded via className. */
export function Skeleton({ className = '' }: BaseProps) {
    return <div className={`animate-pulse bg-(--base-03) rounded ${className}`} />;
}

/** Inline pulsing text bar — height matches a normal text line. */
export function SkeletonText({ width = 'w-32', className = '' }: BaseProps & { width?: string }) {
    return <div className={`animate-pulse bg-(--base-03) rounded h-3 ${width} ${className}`} />;
}

/** Circle (avatars, status dots, small icons). */
export function SkeletonCircle({ size = 'h-8 w-8', className = '' }: BaseProps & { size?: string }) {
    return <div className={`animate-pulse bg-(--base-03) rounded-full ${size} ${className}`} />;
}

/** Card-shaped block sized to match the `card` utility's padding + radius. */
export function SkeletonCard({ height = 'h-32', className = '' }: BaseProps & { height?: string }) {
    return <div className={`animate-pulse bg-(--base-03) rounded-(--radius-md) ${height} ${className}`} />;
}

/** A row of N pulsing list items — matches a typical name+meta list. */
export function SkeletonList({ rows = 5, className = '' }: BaseProps & { rows?: number }) {
    return (
        <div className={`space-y-2 ${className}`}>
            {Array.from({ length: rows }).map((_, i) => (
                <div key={i} className="flex items-center gap-3 px-3 py-2.5 rounded border border-(--base-03) bg-(--base-01)">
                    <SkeletonCircle size="h-6 w-6" />
                    <div className="flex-1 space-y-1.5">
                        <SkeletonText width="w-1/3" />
                        <SkeletonText width="w-1/4" className="h-2" />
                    </div>
                </div>
            ))}
        </div>
    );
}

/** Table with N rows of M cells. Header row optional. */
export function SkeletonTable({
    rows = 6,
    cols = 4,
    showHeader = true,
    className = '',
}: BaseProps & { rows?: number; cols?: number; showHeader?: boolean }) {
    return (
        <div className={`border border-(--base-03) rounded overflow-hidden ${className}`}>
            {showHeader && (
                <div className="flex gap-3 px-3 py-2.5 bg-(--base-02) border-b border-(--base-03)">
                    {Array.from({ length: cols }).map((_, i) => (
                        <SkeletonText key={i} width="w-20" className="h-2.5" />
                    ))}
                </div>
            )}
            {Array.from({ length: rows }).map((_, r) => (
                <div key={r} className="flex gap-3 px-3 py-3 border-b border-(--base-03) last:border-b-0">
                    {Array.from({ length: cols }).map((_, c) => (
                        <SkeletonText
                            key={c}
                            width={c === 0 ? 'w-28' : c === cols - 1 ? 'w-14' : 'w-20'}
                        />
                    ))}
                </div>
            ))}
        </div>
    );
}

/** Stat tile (icon + label + value, side-by-side). For dashboard headers. */
export function SkeletonStatTile({ className = '' }: BaseProps) {
    return (
        <div className={`card p-4 flex items-center gap-3 ${className}`}>
            <Skeleton className="w-9 h-9 rounded-md" />
            <div className="flex-1 space-y-1.5">
                <SkeletonText width="w-16" className="h-2" />
                <SkeletonText width="w-12" className="h-3.5" />
            </div>
        </div>
    );
}

/** Form row — label + input. Repeat to mock a settings form. */
export function SkeletonFormRow({ className = '' }: BaseProps) {
    return (
        <div className={`space-y-1 ${className}`}>
            <SkeletonText width="w-20" className="h-2" />
            <Skeleton className="h-9 w-full rounded" />
        </div>
    );
}

/** Title block — main heading + subtitle, for page headers. */
export function SkeletonHeader({ className = '' }: BaseProps) {
    return (
        <div className={`space-y-2 ${className}`}>
            <SkeletonText width="w-48" className="h-5" />
            <SkeletonText width="w-72" className="h-3" />
        </div>
    );
}

/** Card with a header + N stat tiles, ready to drop into a page top. */
export function SkeletonStatGrid({ tiles = 4, className = '' }: BaseProps & { tiles?: number }) {
    return (
        <div className={`grid grid-cols-2 md:grid-cols-4 gap-3 ${className}`}>
            {Array.from({ length: tiles }).map((_, i) => (
                <SkeletonStatTile key={i} />
            ))}
        </div>
    );
}

/** Chart skeleton — large pulsing block sized to match recharts ResponsiveContainer. */
export function SkeletonChart({ height = 'h-44', className = '' }: BaseProps & { height?: string }) {
    return (
        <div className={`card p-4 ${className}`}>
            <div className="flex items-center justify-between mb-3">
                <div className="flex items-center gap-2">
                    <Skeleton className="w-7 h-7 rounded-md" />
                    <SkeletonText width="w-16" className="h-2.5" />
                </div>
                <SkeletonText width="w-14" className="h-5" />
            </div>
            <Skeleton className={`w-full ${height} rounded`} />
        </div>
    );
}
