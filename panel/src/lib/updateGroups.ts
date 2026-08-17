// Grouping for the "What's new" modal.
//
// The feed is an append-only log, newest first once reversed by Core, so a
// "release" is not a thing it records - the closest honest unit is a date. The
// modal shows the newest date on its own at the top ("the last update") and every
// older date beneath it, which is what makes it readable without inventing
// version numbers the platform deliberately does not have.

export interface UpdateEntryLike {
    date?: string;
    service?: string;
    type?: string;
    summary?: string;
}

export interface UpdateGroup<T extends UpdateEntryLike> {
    /** The group's date, verbatim from the feed. Empty when entries carry none. */
    date: string;
    entries: T[];
}

/**
 * Groups entries by date, preserving the order they arrive in (Core sends newest
 * first). Entries without a date land in one trailing group with an empty date
 * rather than being dropped - a malformed line should still be readable.
 */
export function groupByDate<T extends UpdateEntryLike>(entries: T[]): UpdateGroup<T>[] {
    const out: UpdateGroup<T>[] = [];
    for (const e of entries) {
        const date = (e.date ?? '').trim();
        const last = out[out.length - 1];
        // Consecutive-run grouping, not a map: it keeps the feed's own order
        // instead of imposing a sort the feed never promised.
        if (last && last.date === date) {
            last.entries.push(e);
        } else {
            out.push({ date, entries: [e] });
        }
    }
    return out;
}

/**
 * Splits the grouped feed into the newest group and the rest. Returns
 * latest = null for an empty feed, so a caller can render its empty state
 * without a length check.
 */
export function splitLatest<T extends UpdateEntryLike>(entries: T[]): {
    latest: UpdateGroup<T> | null;
    earlier: UpdateGroup<T>[];
} {
    const groups = groupByDate(entries);
    if (groups.length === 0) return { latest: null, earlier: [] };
    return { latest: groups[0], earlier: groups.slice(1) };
}

/**
 * The three states the navbar icon can be in, in order of loudness:
 *  - 'new'    something arrived that this admin has not opened yet: glow + badge.
 *  - 'unread' entries exist and have been seen: outlined violet, no badge.
 *  - 'idle'   nothing to show (up to date, or an empty feed): grey.
 *
 * Kept as a pure function because the difference between the last two is exactly
 * the bug this fixes: before, "seen" and "nothing at all" rendered identically,
 * so opening the panel made the button look like a dead control.
 */
export type BellState = 'new' | 'unread' | 'idle';

export function bellState(unseen: number, entryCount: number): BellState {
    if (unseen > 0) return 'new';
    if (entryCount > 0) return 'unread';
    return 'idle';
}
