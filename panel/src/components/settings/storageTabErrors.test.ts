import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';

// Both storage tabs reported every rejected write as a bare "Save failed." and
// dropped the server's sentence. That mattered once Core started distinguishing
// the cases: a duplicate name (409) and a storage that no longer exists (404)
// are both actionable, and both looked identical. The delete path was worse -
// BackupsTab showed nothing at all on a failed delete.
//
// Every other settings tab already surfaces `res.message` (Beam, Billing,
// CannedResponses, CoreStorage, DNS, Database), and BackupsTab itself does it
// for the connection test. These two save paths were the exception.
//
// The suite is logic-only (no jsdom), so this reads the source: it guards
// against the pattern returning, not against a render.

const FILES = ['BackupsTab.tsx', 'StorageConnectionsTab.tsx'];

describe('storage settings tabs surface the server reason', () => {
    for (const file of FILES) {
        const source = readFileSync(join(__dirname, file), 'utf8');

        it(`${file} never shows a bare failure toast`, () => {
            // A toast built from a string literal alone throws the reason away.
            const bare = source.match(/showToast\(\s*'(?:Save|Delete) failed\.'/g) ?? [];
            expect(bare).toEqual([]);
        });

        it(`${file} passes res.message through on every failure branch`, () => {
            const fallbacks = source.match(/'(?:Save|Delete) failed\.'/g) ?? [];
            const passthroughs = source.match(/res\.message \|\| '(?:Save|Delete) failed\.'/g) ?? [];
            expect(fallbacks.length).toBeGreaterThan(0); // the branches still exist
            expect(passthroughs.length).toBe(fallbacks.length);
        });
    }

    // The reason only reaches the component if the API client declares it; the
    // storage endpoints returned `{ success: boolean }`, so `res.message` did
    // not type-check and the literal was the only option.
    it('the storage API clients declare message on their write calls', () => {
        const api = readFileSync(join(__dirname, '..', '..', 'lib', 'api', 'types.ts'), 'utf8');
        for (const fn of [
            'createBackupStorage',
            'updateBackupStorage',
            'deleteBackupStorage',
            'saveBackupConfig',
            'createStorageConnection',
            'updateStorageConnection',
            'deleteStorageConnection',
        ]) {
            const decl = api.match(new RegExp(`export const ${fn} = [^=]*=>`))?.[0] ?? '';
            expect(decl, `${fn} must declare message?: string`).toContain('message?: string');
        }
    });
});
