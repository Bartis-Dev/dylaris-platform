import { describe, it, expect } from 'vitest';
import { entitlementOf, daysLeft, formatDaysLeft } from './entitlementState';

describe('entitlementOf', () => {
    // The defect this replaces: two hand-written copies listed the fields they
    // knew about, the per-kind deadlines were added to the API later, and
    // neither copy carried them. The admin's grant rows read those fields, so a
    // tenant with a live grant showed as "Not granted" - the grant worked, the
    // screen said it had not happened.
    it('carries the per-kind grant deadlines', () => {
        const got = entitlementOf({
            success: true,
            byon: true,
            routeOnly: false,
            source: 'grant',
            grantKind: 'byon',
            grantExpiresAt: '2026-10-01T00:00:00Z',
            grantByonExpiresAt: '2026-10-01T00:00:00Z',
            grantRouteExpiresAt: '2026-12-01T00:00:00Z',
        });
        expect(got.grantByonExpiresAt).toBe('2026-10-01T00:00:00Z');
        expect(got.grantRouteExpiresAt).toBe('2026-12-01T00:00:00Z');
    });

    // The property that stops this from happening again: a field nobody here
    // has heard of still reaches the UI.
    it('carries a field it does not know about', () => {
        const got = entitlementOf({ success: true, somethingNew: 'x' } as never) as unknown as Record<string, unknown>;
        expect(got.somethingNew).toBe('x');
    });

    it('defaults the three the UI cannot render as undefined', () => {
        const got = entitlementOf({ success: true });
        expect(got.byon).toBe(false);
        expect(got.routeOnly).toBe(false);
        expect(got.source).toBe('none');
    });
});

describe('daysLeft', () => {
    const now = new Date('2026-09-01T12:00:00Z');

    it('rounds up, because eight hours left is not zero days', () => {
        expect(daysLeft('2026-09-01T20:00:00Z', now)).toBe(1);
        expect(daysLeft('2026-09-04T12:00:00Z', now)).toBe(3);
    });

    it('is 0 once the deadline has passed', () => {
        expect(daysLeft('2026-08-31T12:00:00Z', now)).toBe(0);
    });

    // No grant and a broken value are different from "zero days": the caller
    // shows the plain state instead of a number that would be a lie.
    it('is null when there is nothing to say', () => {
        expect(daysLeft(undefined, now)).toBeNull();
        expect(daysLeft('not a date', now)).toBeNull();
    });
});

describe('formatDaysLeft', () => {
    const now = new Date('2026-09-01T12:00:00Z');

    it('does not say "1 days"', () => {
        expect(formatDaysLeft('2026-09-02T12:00:00Z', now)).toBe('1 day left');
        expect(formatDaysLeft('2026-09-03T12:00:00Z', now)).toBe('2 days left');
    });

    it('says expired rather than "0 days left"', () => {
        expect(formatDaysLeft('2026-08-01T12:00:00Z', now)).toBe('expired');
    });

    it('says nothing when there is no grant', () => {
        expect(formatDaysLeft(undefined, now)).toBe('');
    });
});
