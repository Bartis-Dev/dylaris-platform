import { describe, it, expect } from 'vitest';
import { groupByDate, splitLatest, bellState } from './updateGroups';

const e = (date: string, summary: string) => ({ date, summary, service: 'core', type: 'fix' });

describe('groupByDate', () => {
    it('groups consecutive entries that share a date', () => {
        const groups = groupByDate([e('2026-08-17', 'a'), e('2026-08-17', 'b'), e('2026-08-16', 'c')]);
        expect(groups.map(g => [g.date, g.entries.length])).toEqual([
            ['2026-08-17', 2],
            ['2026-08-16', 1],
        ]);
    });

    it('keeps the feed order instead of sorting', () => {
        // The feed is append-only and Core reverses it; imposing a sort here
        // would fight that and reorder same-day entries.
        const groups = groupByDate([e('2026-08-01', 'older first'), e('2026-08-17', 'newer second')]);
        expect(groups.map(g => g.date)).toEqual(['2026-08-01', '2026-08-17']);
    });

    it('starts a new group when a date repeats non-consecutively', () => {
        const groups = groupByDate([e('2026-08-17', 'a'), e('2026-08-16', 'b'), e('2026-08-17', 'c')]);
        expect(groups).toHaveLength(3);
    });

    it('keeps undated entries instead of dropping them', () => {
        const groups = groupByDate([{ summary: 'malformed line' }]);
        expect(groups).toHaveLength(1);
        expect(groups[0].date).toBe('');
        expect(groups[0].entries[0].summary).toBe('malformed line');
    });

    it('returns nothing for an empty feed', () => {
        expect(groupByDate([])).toEqual([]);
    });
});

describe('splitLatest', () => {
    it('separates the newest date from the history', () => {
        const { latest, earlier } = splitLatest([e('2026-08-17', 'a'), e('2026-08-17', 'b'), e('2026-08-16', 'c')]);
        expect(latest?.date).toBe('2026-08-17');
        expect(latest?.entries).toHaveLength(2);
        expect(earlier.map(g => g.date)).toEqual(['2026-08-16']);
    });

    it('gives a null latest for an empty feed so the caller can render its empty state', () => {
        expect(splitLatest([])).toEqual({ latest: null, earlier: [] });
    });

    it('leaves earlier empty when there is only one date', () => {
        const { latest, earlier } = splitLatest([e('2026-08-17', 'a')]);
        expect(latest?.date).toBe('2026-08-17');
        expect(earlier).toEqual([]);
    });
});

describe('bellState', () => {
    it('is new while something is unseen', () => {
        expect(bellState(3, 5)).toBe('new');
    });

    // The distinction this function exists for: before it, "seen" and "nothing
    // at all" rendered identically, so opening the panel made the icon go grey
    // and look like a dead control.
    it('is unread once seen but entries remain', () => {
        expect(bellState(0, 5)).toBe('unread');
    });

    it('is idle only when there is nothing to show', () => {
        expect(bellState(0, 0)).toBe('idle');
    });

    it('treats unseen as authoritative even with no entries listed', () => {
        // Entries are capped per service, so a large unseen count can outrun the
        // list. The badge must still light up.
        expect(bellState(2, 0)).toBe('new');
    });
});
