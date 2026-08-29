import { describe, expect, it } from 'vitest';
import { spliceImageMismatch } from './spliceDrift';

const A = 'sha256:b6706f1451565170358dfddf0e7551f665ba54917a414c2329809c586a9a970e';
const B = 'sha256:d43ab386939d46a84d2d64ec4912caf1b66bd69115d17a533b4c6634d67192df';

describe('spliceImageMismatch', () => {
    it('flags the real production case: same version, different images', () => {
        expect(spliceImageMismatch(A, B)).toBe(true);
    });

    it('is quiet when the running image is the one the pin names', () => {
        expect(spliceImageMismatch(A, A)).toBe(false);
    });

    it('does not read an unknown half as agreement', () => {
        expect(spliceImageMismatch(undefined, B)).toBe(false);
        expect(spliceImageMismatch(A, undefined)).toBe(false);
        expect(spliceImageMismatch('', '')).toBe(false);
    });
});
