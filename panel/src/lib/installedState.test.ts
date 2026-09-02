import { describe, expect, it } from 'vitest';

import { installedState, isOnServer } from './installedState';
import type { InstalledMod } from '@/lib/api/modrinth';

const row = (over: Partial<InstalledMod> = {}): InstalledMod => ({
    id: 1,
    serverId: 7,
    subServerName: 'survival',
    modrinthProjectId: 'spark',
    modrinthProjectSlug: 'spark',
    modrinthVersionId: 'v2',
    title: 'Spark',
    fileName: 'spark-1.1.jar',
    sha512: '',
    installedAt: '2026-09-02T10:00:00Z',
    ...over,
});

// The build list answered "which build is NEWEST" and never "which build have
// I got" - the one question you open it with. Now it does, and the answer has
// to distinguish three states rather than two, because an install is queued
// work: Core writes the row, the node downloads, verifies and swaps the jar in,
// and only then reports.
describe('installedState', () => {
    it('names the build that is on the server', () => {
        expect(installedState(row(), 'v2')).toBe('installed');
    });

    it('says nothing about the other builds of the same project', () => {
        expect(installedState(row(), 'v1')).toBeNull();
        expect(installedState(row(), 'v3')).toBeNull();
    });

    it('says nothing when the project is not installed at all', () => {
        expect(installedState(undefined, 'v2')).toBeNull();
    });

    it('separates queued from landed', () => {
        expect(installedState(row({ status: 'installing' }), 'v2')).toBe('installing');
    });

    // The case the whole status column exists for. Marking this build as
    // installed is the lie the install path used to tell: Core wrote the row
    // before dispatching and never revisited it, so a download that 404ed
    // listed as an installed mod and the panel offered to update it.
    it('never presents a failed install as the build on the server', () => {
        expect(installedState(row({ status: 'failed' }), 'v2')).toBe('failed');
    });

    // Rows written before the node reported anything carry no status. They were
    // always read as installed and must keep reading that way, or every server
    // in the fleet would show its whole mod list as pending after one deploy.
    it('reads a row with no status as installed', () => {
        expect(installedState(row({ status: undefined }), 'v2')).toBe('installed');
    });
});

// The update check asks a different question than the build list, and "a row
// exists" is the wrong answer to it: a queued install has not happened yet and
// a failed one never will, so neither is something to compare a newer build
// against, and neither should wear the badge saying the server has this mod.
describe('isOnServer', () => {
    it.each([
        [undefined, false],
        [{ status: undefined }, true],
        [{ status: 'installed' as const }, true],
        [{ status: 'installing' as const }, false],
        [{ status: 'failed' as const }, false],
    ])('%o -> %s', (over, want) => {
        expect(isOnServer(over === undefined ? undefined : row(over))).toBe(want);
    });
});
