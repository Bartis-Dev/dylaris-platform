import { describe, it, expect } from 'vitest';
import { latestValue } from './statsSeries';

describe('latestValue', () => {
    it('reads the last sample when it has the value', () => {
        expect(latestValue([{ heap: 100 }, { heap: 250 }], 'heap')).toBe(250);
    });

    // The bug this exists for: the heap field is omitted on any tick with no
    // fresh GC reading, so a running server showed 0 MB whenever the final
    // sample fell between collections.
    it('falls back to the last sample that has one', () => {
        expect(latestValue([{ heap: 250 }, {}, {}], 'heap')).toBe(250);
    });

    it('is 0 when no sample ever carried the value', () => {
        const empty: { heap?: number }[] = [{}, {}];
        expect(latestValue(empty, 'heap')).toBe(0);
        expect(latestValue([], 'heap')).toBe(0);
    });

    // 0 is a legitimate reading and must not be skipped over as if missing.
    it('accepts a genuine zero', () => {
        expect(latestValue([{ heap: 250 }, { heap: 0 }], 'heap')).toBe(0);
    });

    it('skips values that are not usable numbers', () => {
        expect(latestValue([{ heap: 250 }, { heap: NaN }], 'heap')).toBe(250);
        expect(latestValue([{ heap: 250 }, { heap: undefined }], 'heap')).toBe(250);
    });
});
