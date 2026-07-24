// Pure helpers for the Content tab's per-row install/remove/update state:
// picking the newest Modrinth version matching the active loader + MC-version
// filter, and comparing an installed version against it. No I/O here - the
// caller fetches the candidate version list (see getModrinthVersions in
// lib/api/modrinth) and passes it in.

import type { ModrinthVersion } from './api/modrinth';

export interface VersionFilter {
    loaders?: string[];
    mcVersions?: string[];
}

export type ModStatus = 'not-installed' | 'up-to-date' | 'update-available';

function matchesFilter(v: ModrinthVersion, filter: VersionFilter): boolean {
    if (filter.loaders?.length && !v.loaders.some(l => filter.loaders!.includes(l))) return false;
    if (filter.mcVersions?.length && !v.game_versions.some(g => filter.mcVersions!.includes(g))) return false;
    return true;
}

// Newest (by date_published) candidate whose loaders + game_versions overlap
// the filter. Each filter field is OR-within-field (any listed loader, any
// listed MC version) and AND-across-fields, mirroring how Modrinth's own
// search/version-list facets behave. An empty/absent filter field matches
// everything for that field. Returns null when nothing matches (including an
// empty candidate list).
export function pickNewestMatchingVersion(
    candidates: ModrinthVersion[],
    filter: VersionFilter = {},
): ModrinthVersion | null {
    const matching = candidates.filter(v => matchesFilter(v, filter));
    if (matching.length === 0) return null;
    return matching.reduce((newest, v) =>
        v.date_published.localeCompare(newest.date_published) > 0 ? v : newest
    );
}

// Compares an installed version against the newest candidate matching the
// current filter:
// - no installedVersionId -> 'not-installed'
// - installed, and it already is the newest match -> 'up-to-date'
// - installed, but nothing in the candidate list matches the current filter
//   -> 'up-to-date' too (there is nothing better to offer under this filter,
//   so we don't nag with an update button that has nothing to install)
// - installed, and a different (newer-selected) candidate matches the filter
//   -> 'update-available'
export function compareInstalledVsLatest(
    installedVersionId: string | null | undefined,
    candidates: ModrinthVersion[],
    filter: VersionFilter = {},
): ModStatus {
    if (!installedVersionId) return 'not-installed';
    const latest = pickNewestMatchingVersion(candidates, filter);
    if (!latest || latest.id === installedVersionId) return 'up-to-date';
    return 'update-available';
}
