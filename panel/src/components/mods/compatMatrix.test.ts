import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';

// The availability check is slow enough to be raced and severe enough to be
// misread, and both failure modes are invisible in a screenshot. The suite is
// logic-only (no jsdom), so this reads the source: it guards against these
// patterns coming back, not against a render.

const read = (f: string) => readFileSync(join(__dirname, f), 'utf8');

describe('the availability check', () => {
    const hook = read('useCompat.ts');
    const matrix = read('CompatMatrix.tsx');

    it('drops a stale answer instead of letting it overwrite a newer one', () => {
        // A cold check takes seconds. Two clicks in a row would otherwise let
        // the FIRST answer land after the second and replace it, so the screen
        // would show a verdict for a range the user is no longer looking at.
        expect(hook).toMatch(/const ticket = useRef\(0\)/);
        expect(hook).toMatch(/const mine = \+\+ticket\.current/);
        expect(hook).toMatch(/if \(mine !== ticket\.current\) return/);
    });

    it('clears the result when the mode changes', () => {
        // The matrix was computed for one set of target versions. Leaving it on
        // screen after the mode changes reads as a result for the new mode.
        expect(hook).toMatch(/useEffect\(\(\) => \{ setData\(null\); setError\(''\); \}, \[mode, specific, key\]\)/);
    });

    it('refuses a specific-version check with no version picked', () => {
        expect(hook).toMatch(/if \(mode === 'specific' && !specific\)/);
    });

    it('does not render an empty side bucket', () => {
        // A pack with no client-only mods must not show a grey "Client only
        // 0/0". An empty bucket is not a warning and reads like one.
        expect(matrix).toMatch(/if \(!bucket \|\| bucket\.total === 0\) return null/);
    });

    it('colours a both-sided loss red and a single-sided one amber', () => {
        // The severity rule the whole feature turns on: a mod needed on both
        // sides takes the server down, a single-sided one degrades one side.
        // The server decides the bucket status; this is the per-item dot in the
        // expanded list, which has to agree with it.
        expect(matrix).toMatch(/m\.side === 'both' \? 'bg-\(--error-light\)' : 'bg-\(--warning-light\)'/);
    });

    it('distinguishes "not published here" from "could not be checked"', () => {
        // An archived or unlisted project is not the same claim as "no version
        // for this Minecraft version", and collapsing them would report a mod
        // as missing on every single version for a reason that is not true.
        expect(matrix).toMatch(/m\.reason === 'unresolvable'/);
        expect(matrix).toMatch(/could not be checked/);
    });

    it('says availability, not compatibility', () => {
        // A green row means every mod has a version published. It cannot mean
        // the pack still works: a mod can be present and broken by a dependency
        // it no longer matches. The copy must not overstate this.
        expect(matrix).toMatch(/Availability only/);
    });
});

describe('the unlinked-content warning', () => {
    const warning = read('UnlinkedContentWarning.tsx');

    it('renders the rule without a tally for the creation dialog', () => {
        // Shown before a pack exists, where there is nothing to count yet.
        expect(warning).toMatch(/count\?: number/);
        expect(warning).toMatch(/count === undefined/);
    });

    it('names the remediation rather than only the problem', () => {
        expect(warning).toMatch(/Replace with Modrinth/);
        expect(warning).toMatch(/Identify them against Modrinth/);
    });
});
