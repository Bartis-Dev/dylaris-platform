import { describe, it, expect } from 'vitest';
import { shareLinkExpired } from './shareLink';

describe('shareLinkExpired', () => {
    const now = Date.UTC(2026, 7, 23, 12, 0, 0);

    it('is false when there is no deadline at all', () => {
        expect(shareLinkExpired(null, now)).toBe(false);
        expect(shareLinkExpired(undefined, now)).toBe(false);
        expect(shareLinkExpired('', now)).toBe(false);
    });

    it('is false while the deadline is ahead', () => {
        expect(shareLinkExpired('2026-08-23T12:00:01Z', now)).toBe(false);
    });

    it('is true once it is reached - the instant is used up, not the one after', () => {
        expect(shareLinkExpired('2026-08-23T12:00:00Z', now)).toBe(true);
        expect(shareLinkExpired('2026-08-23T11:59:59Z', now)).toBe(true);
    });

    it('does not call a link dead over a value it cannot read', () => {
        expect(shareLinkExpired('not a date', now)).toBe(false);
    });
});
