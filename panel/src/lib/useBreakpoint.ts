"use client";

import { useEffect, useState } from 'react';

// Layout breakpoints for the authed shell.
//
// One source of truth, because the shell's three parts have to agree: the
// sidebar collapses at the same width the navbar starts shedding labels, and the
// right-hand cluster moves at the same width the sidebar has room for it. Three
// components each carrying their own media query is how those drift apart.
//
// This is NOT Tailwind's scale. These are the widths at which THIS layout stops
// working - measured from the shell (a 264px sidebar plus the module strip plus
// the utility cluster), not from device classes.
export const BREAKPOINTS = {
    /** Below this the sidebar becomes a rail. */
    rail: 1280,
    /** Below this the navbar's right-hand actions move into the sidebar. */
    compact: 900,
    /** Below this the page stops scaling and scrolls horizontally instead. */
    floor: 720,
} as const;

export type Layout = 'wide' | 'narrow' | 'compact';

export function layoutForWidth(width: number): Layout {
    if (width >= BREAKPOINTS.rail) return 'wide';
    if (width >= BREAKPOINTS.compact) return 'narrow';
    return 'compact';
}

/**
 * The current layout band, and the raw width for anything that needs it.
 *
 * Starts at 'wide' on the server and on the very first client render: the shell
 * renders identically either way, so a wrong first guess would be a visible
 * reflow. 'wide' is chosen over "measure and flash" because the desktop app -
 * which is where this matters most - opens at 1280 by default.
 *
 * Uses a matchMedia listener per boundary rather than a resize handler: a drag
 * fires resize continuously, and re-rendering the whole shell on every frame of
 * it is exactly the cost this layout cannot afford.
 */
export function useLayout(): { layout: Layout; width: number } {
    const [state, setState] = useState<{ layout: Layout; width: number }>({
        layout: 'wide',
        width: BREAKPOINTS.rail,
    });

    useEffect(() => {
        if (typeof window === 'undefined') return;

        const read = () => {
            const w = window.innerWidth;
            setState(prev => {
                const next = layoutForWidth(w);
                // Only re-render when the BAND changes. The width is carried for
                // the rare caller that wants it, but tracking it exactly would
                // re-render the shell on every pixel of a window drag.
                if (prev.layout === next) return prev;
                return { layout: next, width: w };
            });
        };

        read();

        const queries = [
            window.matchMedia(`(min-width: ${BREAKPOINTS.rail}px)`),
            window.matchMedia(`(min-width: ${BREAKPOINTS.compact}px)`),
        ];
        queries.forEach(q => q.addEventListener('change', read));
        return () => queries.forEach(q => q.removeEventListener('change', read));
    }, []);

    return state;
}
