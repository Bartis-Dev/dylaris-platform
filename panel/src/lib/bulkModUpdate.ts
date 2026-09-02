import type { InstalledMod, ModrinthVersion } from '@/lib/api/modrinth';
import { pickNewestMatchingVersion } from '@/lib/modVersionCompare';
import { isOnServer } from '@/lib/installedState';

/** What one mod's slot in a bulk run ended as. */
export type BulkOutcome = 'updated' | 'current' | 'failed' | 'unknown';

export interface BulkProgress {
    /** How many slots have been decided so far. */
    done: number;
    total: number;
    /** The mod being worked on, for the label. */
    current: string;
}

export interface BulkTally {
    updated: number;
    current: number;
    failed: number;
    unknown: number;
}

export function emptyTally(): BulkTally {
    return { updated: 0, current: 0, failed: 0, unknown: 0 };
}

/**
 * Which mods a bulk update should even look at.
 *
 * isOnServer, not "a row exists": a queued install has not happened yet and a
 * failed one never will, so updating either would be updating from a build the
 * server is not running.
 */
export function updatableMods(installed: InstalledMod[]): InstalledMod[] {
    return installed.filter(m => isOnServer(m));
}

/**
 * The build to move one mod to, or null when it is already current.
 *
 * The SAME selection the per-row check uses - pickNewestMatchingVersion, with
 * the tab's active loader and Minecraft-version filter. Deliberately not a
 * second rule: a bulk run that decides "newest" differently from the badge
 * beside each row would update mods the row calls current, and disagree with
 * the thing the reader just looked at.
 */
export function nextBuildFor(
    mod: InstalledMod,
    candidates: ModrinthVersion[],
    filter: { loaders?: string[]; mcVersions?: string[] },
): ModrinthVersion | null {
    const newest = pickNewestMatchingVersion(candidates, {
        loaders: filter.loaders,
        mcVersions: filter.mcVersions,
    });
    if (!newest || newest.id === mod.modrinthVersionId) return null;
    return newest;
}

/**
 * One sentence for the end of a run.
 *
 * Every category is named, including the ones that are zero-worthy but not
 * zero: "12 updated" alone hides the three that failed, and a bulk action that
 * reports only its successes is how someone concludes a run was clean when a
 * third of it was not.
 */
export function summarise(t: BulkTally): string {
    const parts: string[] = [];
    if (t.updated) parts.push(`${t.updated} updated`);
    if (t.current) parts.push(`${t.current} already current`);
    if (t.failed) parts.push(`${t.failed} failed`);
    if (t.unknown) parts.push(`${t.unknown} could not be checked`);
    if (parts.length === 0) return 'Nothing to do.';
    return parts.join(', ') + '.';
}

/** A run that produced no failure and nothing unchecked is the good outcome. */
export function runWasClean(t: BulkTally): boolean {
    return t.failed === 0 && t.unknown === 0;
}

/**
 * What a bulk run will actually consider, spelled out.
 *
 * The loader and Minecraft-version filter lives in the Browse sidebar, which is
 * NOT rendered on the Installed tab - so the run is scoped by something the
 * reader cannot see from where they press the button. Naming the values is the
 * cheapest honest fix; pointing at a filter that is off-screen is not.
 */
export function updateScopeLabel(loaders: string[], mcVersions: string[]): string {
    const parts: string[] = [];
    if (loaders.length) parts.push(loaders.join(', '));
    if (mcVersions.length) parts.push(`MC ${mcVersions.join(', ')}`);
    if (parts.length === 0) return 'Updates consider every build, with no loader or version filter set.';
    return `Updates are limited to ${parts.join(' · ')}.`;
}
