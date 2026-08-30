import { describe, it, expect } from 'vitest';
import { poolsWorthShowing, poolTone } from './TrafficPools';
import type { TrafficPool } from '@/lib/api/billing';

const pool = (p: Partial<TrafficPool>): TrafficPool => ({
    kind: 'edge', region: 'eu-central', usedGb: 0, includedGb: 1000, pct: 0, warn: 0, ...p,
});

describe('poolsWorthShowing', () => {
    it('drops a pool with no configured allowance', () => {
        // null is not a limit of zero. There is nothing to be a percentage of,
        // and a bar filling against no limit is an alarm nobody can act on.
        const got = poolsWorthShowing([
            pool({ region: 'eu-central' }),
            pool({ region: 'us-east', includedGb: null, usedGb: 9000 }),
        ]);
        expect(got.map(p => p.region)).toEqual(['eu-central']);
    });

    it('drops a zero allowance too, which would draw a permanently full bar', () => {
        expect(poolsWorthShowing([pool({ includedGb: 0 })])).toEqual([]);
    });

    it('survives an absent pool list', () => {
        expect(poolsWorthShowing(undefined)).toEqual([]);
    });
});

describe('poolTone', () => {
    it('says nothing below the first threshold', () => {
        expect(poolTone(0)).toBeNull();
    });

    it('warns at 80 and escalates at 90', () => {
        // Two thresholds, not one: the first is a heads-up with most of the
        // month's remainder left to act on, the second is the last one that
        // still arrives before the allowance is gone.
        expect(poolTone(80)?.label).toBe('80% used');
        expect(poolTone(90)?.label).toBe('90% used');
    });

    it('reads past the allowance as over, not as 90%', () => {
        expect(poolTone(100)?.label).toBe('over the allowance');
    });
});
