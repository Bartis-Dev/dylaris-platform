import { describe, it, expect } from 'vitest';
import { toLocalInput, fromLocalInput } from './localDateTime';

// The pair has to round-trip in the reader's own zone: these ran as untested
// inline helpers, and the failure they can produce (an expiry an hour off for
// anyone not on UTC) looks like working software.
describe('localDateTime', () => {
    it('round-trips an instant through the local-time field', () => {
        const iso = new Date(Date.UTC(2026, 8, 1, 8, 30)).toISOString();
        const back = fromLocalInput(toLocalInput(iso));
        expect(new Date(back).getTime()).toBe(new Date(iso).getTime());
    });

    it('renders in local time, not UTC', () => {
        const iso = new Date(Date.UTC(2026, 8, 1, 8, 30)).toISOString();
        const d = new Date(iso);
        const pad = (n: number) => String(n).padStart(2, '0');
        expect(toLocalInput(iso)).toBe(
            `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`,
        );
    });

    it('keeps the minute it was given', () => {
        expect(toLocalInput(new Date(2026, 8, 1, 8, 30).toISOString())).toMatch(/T08:30$/);
    });

    it.each(['', 'not a date', '2026-13-45T99:99'])('is empty for unusable input %j', (bad) => {
        expect(toLocalInput(bad)).toBe('');
        expect(fromLocalInput(bad)).toBe('');
    });

    it('treats an empty field as no value rather than the epoch', () => {
        expect(fromLocalInput('')).toBe('');
    });
});
