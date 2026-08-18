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

/**
 * How many entries in a set are BREAKING - a change that does not apply itself.
 *
 * The other types describe what changed; this one is the only one that says the
 * operator has to do something, so it is the one piece of the feed that must be
 * impossible to miss. Counting is separate from rendering so the modal can lead
 * with it instead of leaving it to be spotted in a list.
 */
export function breakingCount<T extends UpdateEntryLike>(entries: T[]): number {
    return entries.filter(e => (e.type ?? '').trim().toLowerCase() === 'breaking').length;
}

/** True when any entry in the set is breaking. */
export function hasBreaking<T extends UpdateEntryLike>(entries: T[]): boolean {
    return breakingCount(entries) > 0;
}

/**
 * Counts per change type, in the feed's own order of first appearance, for the
 * one-line "what came with this update" summary above the latest group.
 */
export function typeCounts<T extends UpdateEntryLike>(entries: T[]): Array<{ type: string; count: number }> {
    const out: Array<{ type: string; count: number }> = [];
    for (const e of entries) {
        const type = (e.type ?? '').trim().toLowerCase() || 'update';
        const found = out.find(o => o.type === type);
        if (found) found.count++;
        else out.push({ type, count: 1 });
    }
    return out;
}

/** Entries of one component, in the order the feed listed them. */
export interface ServiceGroup<T extends UpdateEntryLike> {
    /** Lowercased service name, '' for entries that carry none. */
    service: string;
    entries: T[];
}

/**
 * Groups entries by component, in order of first appearance.
 *
 * Unlike groupByDate this is a MAP, not a consecutive run: the feed is written
 * one line per change, so a day's entries arrive interleaved across components.
 * Run-grouping them would produce "Core, Panel, Core, Panel" for a single day,
 * which is the shape this replaces - the reader wants everything Core did, once.
 */
export function groupByService<T extends UpdateEntryLike>(entries: T[]): ServiceGroup<T>[] {
    const out: ServiceGroup<T>[] = [];
    const index = new Map<string, ServiceGroup<T>>();
    for (const e of entries) {
        const service = (e.service ?? '').trim().toLowerCase();
        let group = index.get(service);
        if (!group) {
            group = { service, entries: [] };
            index.set(service, group);
            out.push(group);
        }
        group.entries.push(e);
    }
    return out;
}

/**
 * The newest date's entries grouped by component, and everything older grouped
 * by component ACROSS all remaining dates.
 *
 * The older half deliberately loses its date grouping. Reading history one day
 * at a time means the same component's changes are scattered over a dozen
 * headings; per-component is how someone actually reads it ("what changed in
 * the node since I last looked"). Each entry keeps its own date for the detail.
 */
export function splitLatestByService<T extends UpdateEntryLike>(entries: T[]): {
    latestDate: string;
    latest: ServiceGroup<T>[];
    earlier: ServiceGroup<T>[];
} {
    const { latest, earlier } = splitLatest(entries);
    return {
        latestDate: latest?.date ?? '',
        latest: latest ? groupByService(latest.entries) : [],
        earlier: groupByService(earlier.flatMap(g => g.entries)),
    };
}

/** Display name for a service key. Unknown keys are title-cased, not dropped. */
export function serviceLabel(service: string): string {
    const s = service.trim();
    if (!s) return 'Other';
    const known: Record<string, string> = {
        core: 'Core',
        panel: 'Panel',
        node: 'Node',
        'log-shipper': 'Log shipper',
        edge: 'Edge',
        link: 'Link',
        hub: 'Hub',
        warp: 'Warp',
        splice: 'Splice',
        beam: 'Beam',
    };
    return known[s.toLowerCase()] ?? s.charAt(0).toUpperCase() + s.slice(1);
}
