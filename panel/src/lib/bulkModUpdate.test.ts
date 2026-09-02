import { describe, expect, it } from 'vitest';

import {
    updatableMods, nextBuildFor, summarise, runWasClean, emptyTally, updateScopeLabel,
} from './bulkModUpdate';
import type { InstalledMod, ModrinthVersion } from '@/lib/api/modrinth';

const mod = (over: Partial<InstalledMod> = {}): InstalledMod => ({
    id: 1, serverId: 7, subServerName: 'survival',
    modrinthProjectId: 'spark', modrinthProjectSlug: 'spark',
    modrinthVersionId: 'v2', title: 'Spark', fileName: 'spark-1.1.jar',
    sha512: '', installedAt: '2026-09-01T00:00:00Z', ...over,
});

const ver = (id: string, published: string, over: Partial<ModrinthVersion> = {}): ModrinthVersion => ({
    id,
    project_id: 'spark',
    name: id,
    version_number: id,
    version_type: 'release',
    date_published: published,
    loaders: ['fabric'],
    game_versions: ['1.21'],
    files: [],
    ...over,
} as ModrinthVersion);

// A bulk run must not act on a mod the server is not actually running. A queued
// install has not happened yet and a failed one never will, so updating either
// would be updating from a build that is not there.
describe('updatableMods', () => {
    it('skips a queued and a failed install', () => {
        const list = [
            mod({ id: 1, modrinthProjectId: 'a' }),
            mod({ id: 2, modrinthProjectId: 'b', status: 'installing' }),
            mod({ id: 3, modrinthProjectId: 'c', status: 'failed' }),
            mod({ id: 4, modrinthProjectId: 'd', status: 'installed' }),
        ];
        expect(updatableMods(list).map(m => m.modrinthProjectId)).toEqual(['a', 'd']);
    });
});

// The bulk run has to reach the same verdict as the badge beside each row, or
// it updates mods the row calls current and contradicts what the reader just
// looked at. Same selector, same filter.
describe('nextBuildFor', () => {
    const candidates = [
        ver('v1', '2026-01-01T00:00:00Z'),
        ver('v2', '2026-05-01T00:00:00Z'),
        ver('v3', '2026-08-01T00:00:00Z'),
    ];

    it('offers the newest matching build', () => {
        expect(nextBuildFor(mod(), candidates, {})?.id).toBe('v3');
    });

    it('offers nothing when the installed build IS the newest', () => {
        expect(nextBuildFor(mod({ modrinthVersionId: 'v3' }), candidates, {})).toBeNull();
    });

    it('offers nothing when no build matches the filter', () => {
        expect(nextBuildFor(mod(), candidates, { loaders: ['forge'] })).toBeNull();
    });

    // Going BACKWARDS is still a change, and the newest matching build under a
    // narrowed filter can be older than what is installed. That is the honest
    // answer for the filter the reader has set.
    it('offers an older build when the filter excludes the installed one', () => {
        const narrowed = [ver('v1', '2026-01-01T00:00:00Z', { game_versions: ['1.20'] })];
        expect(nextBuildFor(mod(), narrowed, { mcVersions: ['1.20'] })?.id).toBe('v1');
    });
});

// A bulk action that reports only its successes is how somebody concludes a run
// was clean when a third of it was not.
describe('summarise', () => {
    it('names every category that happened', () => {
        const t = { ...emptyTally(), updated: 12, current: 3, failed: 2, unknown: 1 };
        const got = summarise(t);
        expect(got).toContain('12 updated');
        expect(got).toContain('3 already current');
        expect(got).toContain('2 failed');
        expect(got).toContain('1 could not be checked');
    });

    it('leaves out the categories that did not happen', () => {
        expect(summarise({ ...emptyTally(), updated: 4 })).toBe('4 updated.');
    });

    it('says so when there was nothing to do', () => {
        expect(summarise(emptyTally())).toBe('Nothing to do.');
    });

    // "could not be checked" is not a success. A version lookup that failed
    // returns an empty list, which looks exactly like "no build matches" - so it
    // is counted apart, and it makes the run not clean.
    it('a run with an unchecked mod is not clean', () => {
        expect(runWasClean({ ...emptyTally(), updated: 9, unknown: 1 })).toBe(false);
        expect(runWasClean({ ...emptyTally(), updated: 9, failed: 1 })).toBe(false);
        expect(runWasClean({ ...emptyTally(), updated: 9, current: 1 })).toBe(true);
    });
});

// The filter that scopes a bulk run lives in the Browse sidebar, which is not
// rendered on the Installed tab. The label therefore has to NAME the values
// rather than point at a control the reader cannot see from the button.
describe('updateScopeLabel', () => {
    it('names both halves of the filter', () => {
        const got = updateScopeLabel(['fabric'], ['1.21']);
        expect(got).toContain('fabric');
        expect(got).toContain('1.21');
    });

    it('names whichever half is set', () => {
        expect(updateScopeLabel(['paper'], [])).toContain('paper');
        expect(updateScopeLabel([], ['1.20.4'])).toContain('1.20.4');
    });

    // No filter is a real state and a consequential one - the run then considers
    // every build there is, which is not what "no filter" sounds like.
    it('says so when nothing is filtered', () => {
        expect(updateScopeLabel([], [])).toMatch(/no loader or version filter/i);
    });
});
