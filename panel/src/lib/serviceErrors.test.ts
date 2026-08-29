import { describe, it, expect } from 'vitest';
import { flattenServiceErrors, attentionCount, isAttention } from './serviceErrors';

describe('flattenServiceErrors', () => {
    it('merges every service into one list, newest first', () => {
        const out = flattenServiceErrors({
            link: [
                { ts: '2026-08-29T16:35:36Z', level: 'ERROR', source: 'stream', message: 'b' },
                { ts: '2026-08-29T16:31:24Z', level: 'ERROR', source: 'stream', message: 'a' },
            ],
            edge: [
                { ts: '2026-08-29T16:40:00Z', level: 'WARN', source: 'splice-handler', message: 'c' },
            ],
        });
        expect(out.map(e => e.message)).toEqual(['c', 'b', 'a']);
        expect(out[0].service).toBe('edge');
        expect(out[1].service).toBe('link');
    });

    it('is empty for null, undefined and {}', () => {
        expect(flattenServiceErrors(null)).toEqual([]);
        expect(flattenServiceErrors(undefined)).toEqual([]);
        expect(flattenServiceErrors({})).toEqual([]);
    });

    it('survives a service whose value is not an array', () => {
        // The API omits empty services, but a shape change must not blank the
        // whole panel section - that is the failure mode this guards.
        const out = flattenServiceErrors({
            hub: 'boom' as unknown as [],
            link: [{ ts: '2026-08-29T16:00:00Z', level: 'ERROR', source: 's', message: 'kept' }],
        });
        expect(out.map(e => e.message)).toEqual(['kept']);
    });

    it('keeps an entry with an unparseable timestamp, sorted last', () => {
        const out = flattenServiceErrors({
            link: [
                { ts: 'not-a-date', level: 'ERROR', source: 's', message: 'broken-ts' },
                { ts: '2026-08-29T16:00:00Z', level: 'ERROR', source: 's', message: 'good-ts' },
            ],
        });
        expect(out.map(e => e.message)).toEqual(['good-ts', 'broken-ts']);
    });
});

describe('attentionCount', () => {
    it('counts ERROR and WARN but not INFO', () => {
        const flat = flattenServiceErrors({
            hub: [
                { ts: '2026-08-29T16:03:06Z', level: 'INFO', source: 'HUB', message: 'lease acquired' },
                { ts: '2026-08-29T16:02:35Z', level: 'WARN', source: 'HUB', message: 'lease lost' },
            ],
            link: [
                { ts: '2026-08-29T16:35:36Z', level: 'ERROR', source: 'stream', message: 'refused' },
            ],
        });
        expect(flat).toHaveLength(3);
        expect(attentionCount(flat)).toBe(2);
    });

    it('treats the level case-insensitively', () => {
        expect(isAttention({ ts: '', level: 'error', source: '', message: '' })).toBe(true);
        expect(isAttention({ ts: '', level: 'info', source: '', message: '' })).toBe(false);
    });
});
