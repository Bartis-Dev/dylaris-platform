import { describe, expect, it } from 'vitest';
import { migrationOutcome } from './BuildMigrationPanel';

// A migration returns 200 with the build it created AND a list of content that
// was supposed to travel and did not. The panel used to render only the first
// half: it toasted "Build created", closed the dialog and navigated to the new
// build, so mods that failed to download were discovered weeks later by their
// absence.
describe('migrationOutcome', () => {
    it('reports a clean migration as a success', () => {
        const out = migrationOutcome('1.2.0-mc1.21.1', '1.21.1', []);
        expect(out.ok).toBe(true);
        expect(out.message).toContain('1.2.0-mc1.21.1');
        expect(out.message).toContain('1.21.1');
    });

    it('treats a missing failed list as clean', () => {
        // An older Core, or a response shape that simply omits it. Absence is
        // not evidence of failure.
        expect(migrationOutcome('v1', '1.21.1', undefined).ok).toBe(true);
    });

    it('does NOT report success when content was lost', () => {
        const out = migrationOutcome('v1', '1.21.1', [{ title: 'Sodium' }]);
        expect(out.ok).toBe(false);
        expect(out.message).toContain('Sodium');
        // The build does exist, so the message must say so rather than reading
        // as a total failure - the operator has to go and look at it.
        expect(out.message).toContain('v1');
    });

    it('says one mod in the singular', () => {
        const out = migrationOutcome('v1', '1.21.1', [{ title: 'Sodium' }]);
        expect(out.message).toContain('1 mod could not');
        expect(out.message).not.toContain('1 mods');
    });

    it('names the first few and counts the rest rather than listing twenty', () => {
        const many = ['A', 'B', 'C', 'D', 'E'].map(title => ({ title }));
        const out = migrationOutcome('v1', '1.21.1', many);
        expect(out.ok).toBe(false);
        expect(out.message).toContain('5 mods');
        expect(out.message).toContain('A, B, C');
        expect(out.message).toContain('and 2 more');
        expect(out.message).not.toContain('D,');
    });

    it('names all of them when they fit', () => {
        const out = migrationOutcome('v1', '1.21.1', [{ title: 'A' }, { title: 'B' }, { title: 'C' }]);
        expect(out.message).toContain('A, B, C');
        expect(out.message).not.toContain('more');
    });
});
