import { describe, it, expect } from 'vitest';
import { pickNewestMatchingVersion, compareInstalledVsLatest } from './modVersionCompare';
import type { ModrinthVersion } from './api/modrinth';

function version(overrides: Partial<ModrinthVersion> & { id: string }): ModrinthVersion {
    return {
        project_id: 'proj',
        name: overrides.id,
        version_number: overrides.id,
        game_versions: ['1.20.1'],
        loaders: ['fabric'],
        version_type: 'release',
        featured: false,
        date_published: '2026-01-01T00:00:00Z',
        files: [],
        ...overrides,
    };
}

describe('pickNewestMatchingVersion', () => {
    it('returns null for an empty candidate list', () => {
        expect(pickNewestMatchingVersion([], { loaders: ['fabric'], mcVersions: ['1.20.1'] })).toBeNull();
    });

    it('picks the newest by date among matching candidates, regardless of array order', () => {
        const older = version({ id: 'v1', date_published: '2026-01-01T00:00:00Z' });
        const newest = version({ id: 'v2', date_published: '2026-03-01T00:00:00Z' });
        const mid = version({ id: 'v3', date_published: '2026-02-01T00:00:00Z' });
        const result = pickNewestMatchingVersion([older, newest, mid], { loaders: ['fabric'], mcVersions: ['1.20.1'] });
        expect(result?.id).toBe('v2');
    });

    it('excludes a candidate matching MC version but not loader', () => {
        const wrongLoader = version({ id: 'v1', loaders: ['forge'], game_versions: ['1.20.1'] });
        const result = pickNewestMatchingVersion([wrongLoader], { loaders: ['fabric'], mcVersions: ['1.20.1'] });
        expect(result).toBeNull();
    });

    it('excludes a candidate matching loader but not MC version', () => {
        const wrongVersion = version({ id: 'v1', loaders: ['fabric'], game_versions: ['1.19.4'] });
        const result = pickNewestMatchingVersion([wrongVersion], { loaders: ['fabric'], mcVersions: ['1.20.1'] });
        expect(result).toBeNull();
    });

    it('matches when a candidate lists multiple loaders/versions and any one overlaps the filter', () => {
        const v = version({ id: 'v1', loaders: ['fabric', 'quilt'], game_versions: ['1.19.4', '1.20.1'] });
        const result = pickNewestMatchingVersion([v], { loaders: ['quilt'], mcVersions: ['1.20.1'] });
        expect(result?.id).toBe('v1');
    });

    it('treats an empty/absent filter as matching everything', () => {
        const v = version({ id: 'v1', loaders: ['forge'], game_versions: ['1.16.5'] });
        expect(pickNewestMatchingVersion([v], {})?.id).toBe('v1');
        expect(pickNewestMatchingVersion([v])?.id).toBe('v1');
    });
});

describe('compareInstalledVsLatest', () => {
    const filter = { loaders: ['fabric'], mcVersions: ['1.20.1'] };

    it('returns not-installed when there is no installed version id', () => {
        const candidates = [version({ id: 'v1' })];
        expect(compareInstalledVsLatest(null, candidates, filter)).toBe('not-installed');
        expect(compareInstalledVsLatest(undefined, candidates, filter)).toBe('not-installed');
        expect(compareInstalledVsLatest('', candidates, filter)).toBe('not-installed');
    });

    it('returns up-to-date when the installed version is the newest match', () => {
        const older = version({ id: 'v1', date_published: '2026-01-01T00:00:00Z' });
        const newest = version({ id: 'v2', date_published: '2026-02-01T00:00:00Z' });
        expect(compareInstalledVsLatest('v2', [older, newest], filter)).toBe('up-to-date');
    });

    it('returns update-available when a newer matching build exists', () => {
        const installed = version({ id: 'v1', date_published: '2026-01-01T00:00:00Z' });
        const newer = version({ id: 'v2', date_published: '2026-02-01T00:00:00Z' });
        expect(compareInstalledVsLatest('v1', [installed, newer], filter)).toBe('update-available');
    });

    it('returns up-to-date when installed but no candidate matches the current filter (nothing better to offer)', () => {
        const nonMatching = version({ id: 'v1', loaders: ['forge'], game_versions: ['1.16.5'] });
        expect(compareInstalledVsLatest('some-other-installed-id', [nonMatching], filter)).toBe('up-to-date');
    });

    it('returns up-to-date for an empty candidate list when installed', () => {
        expect(compareInstalledVsLatest('v1', [], filter)).toBe('up-to-date');
    });
});
