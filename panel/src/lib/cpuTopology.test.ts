import { describe, it, expect } from 'vitest';
import { groupCoresByCache, formatL3, formatClock } from './cpuTopology';
import type { CpuCore } from './api/types';

function core(id: number, cacheGroup: number, l3KB: number, maxClockMHz = 0): CpuCore {
    return { id, type: 'standard', sibling: -1, maxClockMHz, cacheGroup, l3KB };
}

describe('groupCoresByCache', () => {
    it('collapses to a single synthetic group when no cache data (all cacheGroup -1)', () => {
        const groups = groupCoresByCache([core(0, -1, 0), core(1, -1, 0), core(2, -1, 0)]);
        expect(groups).toHaveLength(1);
        expect(groups[0].cacheGroup).toBe(-1);
        expect(groups[0].isVCache).toBe(false);
        expect(groups[0].cores.map(c => c.id)).toEqual([0, 1, 2]);
    });

    it('does not flag V-Cache for a uniform single L3 domain', () => {
        const groups = groupCoresByCache([core(0, 0, 32768), core(1, 0, 32768)]);
        expect(groups).toHaveLength(1);
        expect(groups[0].isVCache).toBe(false);
    });

    it('flags the max-L3 group as V-Cache when >1 distinct non-zero L3', () => {
        const groups = groupCoresByCache([
            core(0, 0, 98304), core(1, 0, 98304),
            core(2, 1, 32768), core(3, 1, 32768),
        ]);
        expect(groups).toHaveLength(2);
        expect(groups[0].cacheGroup).toBe(0);
        expect(groups[0].l3KB).toBe(98304);
        expect(groups[0].isVCache).toBe(true);
        expect(groups[1].cacheGroup).toBe(1);
        expect(groups[1].isVCache).toBe(false);
    });

    it('orders groups by ascending cacheGroup index', () => {
        const groups = groupCoresByCache([core(0, 1, 32768), core(1, 0, 98304)]);
        expect(groups.map(g => g.cacheGroup)).toEqual([0, 1]);
    });

    it('returns an empty array for no cores', () => {
        expect(groupCoresByCache([])).toEqual([]);
    });
});

describe('formatL3', () => {
    it('formats exact MB', () => {
        expect(formatL3(98304)).toBe('96MB');
        expect(formatL3(32768)).toBe('32MB');
    });
    it('formats sub-MB as KB', () => {
        expect(formatL3(512)).toBe('512KB');
    });
    it('returns empty for zero or negative', () => {
        expect(formatL3(0)).toBe('');
        expect(formatL3(-1)).toBe('');
    });
});

describe('formatClock', () => {
    it('formats one-decimal GHz', () => {
        expect(formatClock(4800)).toBe('4.8G');
        expect(formatClock(3200)).toBe('3.2G');
        expect(formatClock(5000)).toBe('5.0G');
    });
    it('returns empty for zero', () => {
        expect(formatClock(0)).toBe('');
    });
});
