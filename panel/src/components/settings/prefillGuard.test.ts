import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';

// A settings form that pre-fills itself from a GET must not be savable when
// that GET failed. Otherwise the form renders its hardcoded defaults - which
// look exactly like a stored configuration - and one click writes them over the
// real one.
//
// This used to be checked per tab, because each tab carried its own copy of the
// guard: its own loadFailed flag, its own disabled= expression, its own
// explanation. That is exactly the duplication SettingsCard and useSettingsForm
// were introduced to end, so the guard now lives in those two files and the
// tabs inherit it. The test follows it there.
//
// The mechanism, in two halves:
//
//   useSettingsForm: a load that resolves null leaves the snapshot null, and
//   `dirty` is computed against the snapshot - so a failed load is never dirty
//   and there is nothing to submit.
//
//   SettingsCard: `loadFailed` refuses the save outright AND renders a banner
//   saying why, so the inert button is not read as a broken page.
//
// The suite is logic-only (no jsdom), so this reads the source: it guards
// against the pattern returning, not against a render.

const read = (f: string) => readFileSync(join(__dirname, f), 'utf8');

describe('the shared pre-fill guard', () => {
    const hook = readFileSync(join(__dirname, '..', '..', 'lib', 'useSettingsForm.ts'), 'utf8');
    const card = read('SettingsCard.tsx');

    it('a load that fails leaves no snapshot to be dirty against', () => {
        // The failure branch is the whole point: `if (res.success)` with no
        // else is what left the defaults on screen looking authoritative.
        expect(hook).toMatch(/setLoadFailed\(true\)/);
        expect(hook).toMatch(/setSnapshot\(null\)/);
        expect(hook).toMatch(/const dirty = snapshot !== null &&/);
    });

    it('a card whose form failed to load refuses to save', () => {
        // The state machine is covered properly in settingsCardSave.test.ts;
        // this only pins that the card asks it rather than deciding again.
        expect(card).toMatch(/if \(form\.loadFailed \|\| blockedReason\) return 'blocked';/);
        expect(card).toMatch(/const canSave = saveState\(form, blockedReason\) === 'ready';/);
    });

    it('and says why, rather than showing a dead button', () => {
        // A disabled button with no explanation reads as a broken page.
        expect(card).toMatch(/\{loadFailed && \(/);
        expect(card).toContain('could not be loaded');
    });
});

// These two had neither guard once: a non-null default, a plain always-enabled
// Save button, and a load whose failure branch did not exist at all. Pinning
// that they route through the hook is what keeps them covered, because the hook
// is where the guard now is.
describe('the tabs that were missing the guard still inherit it', () => {
    for (const file of ['MaintenanceTab.tsx', 'TicketSettingsTab.tsx']) {
        const source = read(file);

        it(`${file} drives its form through useSettingsForm`, () => {
            expect(source).toContain('useSettingsForm<');
            // Resolving null on failure is what arms the guard. A tab that
            // returned its defaults here instead would look identical and be
            // saveable.
            expect(source).toMatch(/return null;|\? res\.\w+ : null/);
        });

        it(`${file} hands that form to the card that owns the Save`, () => {
            expect(source).toMatch(/<SettingsCard[\s\S]{0,400}?form=\{form\}/);
        });
    }

    // MaintenanceTab is the one where writing the default is not merely wrong
    // but unsafe: the DB migration holds block_all for its whole run, and the
    // default is banner_only. If that default ever stops being weaker than
    // block_all this test should be revisited, so pin it.
    it('MaintenanceTab still defaults to a weaker block level than the migration relies on', () => {
        const source = read('MaintenanceTab.tsx');
        expect(source).toMatch(/const defaultState[\s\S]*?blockLevel:\s*'banner_only'/);
        expect(source).toMatch(/const defaultState[\s\S]*?active:\s*false/);
    });

    // It also keeps its own wording, because "saving this would lift a
    // maintenance mode that is holding right now" is not something the generic
    // banner can say.
    it('MaintenanceTab explains its own load failure rather than taking the generic one', () => {
        expect(read('MaintenanceTab.tsx')).toMatch(/loadFailedMessage=/);
    });
});

// The RolesTab editor is the sharpest case: its PUT replaces the panel role AND
// both override sets, so a failed read could drop deny overrides and thereby
// GRANT capabilities. Its guard must gate the body, not just the button, so an
// empty assignment is never displayed as if it were real. It is hand-rolled and
// stays that way - the card guard disables a save; this one refuses to render.
describe('RolesTab guards the body, not just the button', () => {
    const source = read('RolesTab.tsx');

    it('tracks a failed pre-fill', () => {
        expect(source).toContain('loadFailed');
        expect(source).toMatch(/setLoadFailed\(true\)/);
    });

    it('does not render an assignment it could not load', () => {
        expect(source).toMatch(/loadFailed \?/);
    });

    it('says why', () => {
        expect(source).toContain('could not be loaded');
    });
});
