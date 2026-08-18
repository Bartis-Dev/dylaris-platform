"use client";

import React, { createContext, useCallback, useContext, useEffect, useState } from 'react';
import { useLayout, type Layout } from '@/lib/useBreakpoint';

// Whether the server sidebar is collapsed to its rail.
//
// Shared rather than local because two components have to agree on it: the
// sidebar itself, and the navbar's branding block, which is only aligned with
// the sidebar because it carries the SAME width. A collapsed sidebar under a
// full-width logo is the misalignment this context exists to prevent.

const STORAGE_KEY = 'dylaris:sidebarCollapsed';

interface SidebarCollapseValue {
    collapsed: boolean;
    toggle: () => void;
    /** Width both the rail and the navbar branding block use. */
    width: string;
}

const Ctx = createContext<SidebarCollapseValue>({
    collapsed: false,
    toggle: () => {},
    width: 'w-72',
});

export function useSidebarCollapse(): SidebarCollapseValue {
    return useContext(Ctx);
}

/**
 * Resolves the collapsed state from the layout band and any manual override.
 *
 * A manual choice wins WITHIN a band but is dropped when the band changes.
 * Otherwise someone who expanded the sidebar once at 1400px would keep a broken
 * layout at 950px forever, which is the failure mode a plain persisted boolean
 * has - and the one people notice, because it only appears after a resize they
 * have long forgotten making.
 */
export function resolveCollapsed(layout: Layout, override: { layout: Layout; collapsed: boolean } | null): boolean {
    const auto = layout !== 'wide';
    if (override && override.layout === layout) return override.collapsed;
    return auto;
}

export function SidebarCollapseProvider({ children }: { children: React.ReactNode }) {
    const { layout } = useLayout();
    const [override, setOverride] = useState<{ layout: Layout; collapsed: boolean } | null>(null);

    // Restore a manual choice made in this same band. Read once, client-side:
    // the server has no window to measure, so persisting across a reload is the
    // only way the choice survives at all.
    useEffect(() => {
        try {
            const raw = localStorage.getItem(STORAGE_KEY);
            if (!raw) return;
            const parsed = JSON.parse(raw) as { layout: Layout; collapsed: boolean };
            if (parsed && typeof parsed.collapsed === 'boolean' && parsed.layout) {
                setOverride(parsed);
            }
        } catch { /* a corrupt value simply means no override */ }
    }, []);

    const collapsed = resolveCollapsed(layout, override);

    const toggle = useCallback(() => {
        const next = { layout, collapsed: !collapsed };
        setOverride(next);
        try {
            localStorage.setItem(STORAGE_KEY, JSON.stringify(next));
        } catch { /* private mode; the choice just does not survive a reload */ }
    }, [layout, collapsed]);

    return (
        <Ctx.Provider value={{ collapsed, toggle, width: collapsed ? 'w-14' : 'w-72' }}>
            {children}
        </Ctx.Provider>
    );
}
