'use client';

import { useEffect, useState } from 'react';

/**
 * A page's tab, selectable from the URL.
 *
 * The settings search points at a SETTING, and half of them now live behind a
 * tab. Landing on the right page with the wrong tab selected leaves the operator
 * exactly where they were: looking for something on a screen that does not show
 * it.
 *
 * Read once on mount, not watched: after that the tab bar owns the state, and a
 * URL that kept forcing it would fight every click.
 */
export function useTabParam<T extends string>(valid: readonly T[], fallback: T): [T, (t: T) => void] {
    const [tab, setTab] = useState<T>(fallback);

    useEffect(() => {
        if (typeof window === 'undefined') return;
        const params = new URLSearchParams(window.location.search);
        // The hash form is kept because /settings/gateway#xdp was already a
        // documented deep link.
        const wanted = params.get('tab') || window.location.hash.replace(/^#/, '');
        if (wanted && (valid as readonly string[]).includes(wanted)) {
            setTab(wanted as T);
        }
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, []);

    return [tab, setTab];
}
