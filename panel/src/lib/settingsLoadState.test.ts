import { describe, it, expect } from 'vitest';
import { settingsLoadState, settingsFormUsable } from './settingsLoadState';

describe('settingsLoadState', () => {
    it('shows the skeleton while the request is out', () => {
        expect(settingsLoadState(true, false)).toBe('loading');
    });

    it('shows the form once values arrived', () => {
        expect(settingsLoadState(false, false)).toBe('ready');
    });

    // The whole point. A settings form renders its own defaults, and defaults
    // look exactly like a platform nobody configured: no limits, no hoster
    // domains, every toggle off. Lifting the skeleton on failure presents that
    // as the stored configuration.
    it('never falls through to the form after a failed load', () => {
        expect(settingsLoadState(false, true)).toBe('failed');
    });

    // A load that is BOTH still running and already known to have failed is a
    // retry in flight. The skeleton wins, or the screen flickers between an
    // error and a spinner on every attempt.
    it('prefers the skeleton while a retry is in flight', () => {
        expect(settingsLoadState(true, true)).toBe('loading');
    });
});

describe('settingsFormUsable', () => {
    // dirty is measured against a snapshot only a successful load sets, so
    // after a failure nothing typed can be saved and no save bar appears to say
    // so. A form in that state must not be offered at all.
    it('refuses the form after a failed load', () => {
        expect(settingsFormUsable(false, true)).toBe(false);
    });

    it('refuses it while still loading', () => {
        expect(settingsFormUsable(true, false)).toBe(false);
    });

    it('allows it only once the values are in', () => {
        expect(settingsFormUsable(false, false)).toBe(true);
    });
});
