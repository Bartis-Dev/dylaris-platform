import type { InstalledMod } from '@/lib/api/modrinth';

/**
 * Is THIS build the one on the server, and if so, did it get there?
 *
 * Returns null when the build has nothing to do with what is installed - which
 * includes the case that matters most: a different version of the same project
 * is installed, so this build is neither current nor failed, it is simply
 * another build you could install.
 *
 * The three non-null answers are deliberately not collapsed into a boolean.
 * Installing a mod is queued work: Core writes the row, the node downloads,
 * verifies and swaps the jar in, and only then reports. A row that says
 * "failed" names a build the server is NOT running, and marking it as installed
 * would be the same lie the whole install path used to tell - the panel offered
 * to update a mod whose download had 404ed.
 *
 * A row with no status at all predates the reporting and is read as installed,
 * because that is what it was always taken to mean.
 */
export function installedState(
    installed: InstalledMod | undefined,
    versionId: string,
): 'installed' | 'installing' | 'failed' | null {
    if (!installed || installed.modrinthVersionId !== versionId) return null;
    switch (installed.status) {
        case 'installing':
            return 'installing';
        case 'failed':
            return 'failed';
        default:
            return 'installed';
    }
}

/**
 * Is this mod actually ON the server right now?
 *
 * The answer the update check needs, and it is not "a row exists". A queued
 * install has not happened yet and a failed one never will, so neither is
 * something to compare a newer build against.
 */
export function isOnServer(installed: InstalledMod | undefined): boolean {
    return !!installed && installed.status !== 'installing' && installed.status !== 'failed';
}
