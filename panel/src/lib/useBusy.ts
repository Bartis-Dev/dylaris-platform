'use client';

import { useCallback, useRef, useState } from 'react';

/**
 * Guards a mutating action against being fired twice while its request is
 * still in flight, and reports the in-flight state so the button can disable
 * itself.
 *
 * A create button that stays enabled during its own request can be clicked
 * twice, and both clicks reach the server. Measured on the testbed rather than
 * assumed: two clicks on "Create key" in one tick produced two API keys one
 * millisecond apart, and only ONE plaintext was ever shown - so the second key
 * was live and its secret was lost. Backup jobs, restores and the server
 * wizard already guarded this; the other create/save buttons did not.
 *
 * The ref is what actually blocks the second call. React state does not update
 * until after the current tick, so two clicks dispatched in the same tick
 * would both read `busy === false`. The state exists only to drive `disabled`.
 */
export function useBusy(): [boolean, (fn: () => unknown | Promise<unknown>) => Promise<void>] {
    const [busy, setBusy] = useState(false);
    const inFlight = useRef(false);

    const run = useCallback(async (fn: () => unknown | Promise<unknown>) => {
        if (inFlight.current) return;
        inFlight.current = true;
        setBusy(true);
        try {
            await fn();
        } finally {
            inFlight.current = false;
            setBusy(false);
        }
    }, []);

    return [busy, run];
}
