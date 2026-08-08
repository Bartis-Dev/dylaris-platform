import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';

// A settings form that pre-fills itself from a GET must not be savable when
// that GET failed. Otherwise the form renders its hardcoded defaults - which
// look exactly like a stored configuration - and one click writes them over the
// real one.
//
// Most tabs are already safe, by one of two mechanisms:
//
//   null-initialised state + an early return
//     UserManagementTab (auth policy, SMTP), UsersTab (account policy):
//     `useState<T | null>(null)` and `if (!policy) return`, so there is nothing
//     to submit.
//
//   a null-gated snapshot
//     ServersTab, FileManagerTab, ModpacksTab, DNSTab, BeamTab, FeaturesTab:
//     `dirty = snapshotRef.current !== null && ...` and the save bar is driven
//     by `dirty`, so a failed load never shows one. DNSTab documents exactly
//     this ("the save bar never appears and these unconfirmed values cannot be
//     written back. Only the display was lying").
//
// These three had neither: a non-null default, a plain always-enabled Save
// button, and a load whose failure branch did not exist at all. They now use
// the third mechanism, the one ConfigEditorModal already uses - a loadFailed
// flag that both explains itself and disables Save.
//
// The suite is logic-only (no jsdom), so this reads the source: it guards
// against the pattern returning, not against a render.

const FILES = ['MaintenanceTab.tsx', 'TicketSettingsTab.tsx', 'RolesTab.tsx'];

describe('a settings form whose pre-fill failed cannot be saved', () => {
    for (const file of FILES) {
        const source = readFileSync(join(__dirname, file), 'utf8');

        it(`${file} tracks a failed pre-fill`, () => {
            expect(source).toContain('loadFailed');
            // The failure branch is the whole point: `if (res.success)` with no
            // else is what left the defaults on screen looking authoritative.
            expect(source).toMatch(/setLoadFailed\(true\)/);
        });

        it(`${file} disables Save while the pre-fill is unconfirmed`, () => {
            const guards = source.match(/disabled=\{[^}]*loadFailed[^}]*\}/g) ?? [];
            expect(guards.length).toBeGreaterThan(0);
        });

        it(`${file} says why saving is blocked`, () => {
            // A disabled button with no explanation reads as a broken page.
            expect(source).toMatch(/loadFailed &&|loadFailed \?/);
            expect(source).toContain('could not be loaded');
        });
    }

    // MaintenanceTab is the one where writing the default is not merely wrong
    // but unsafe: the DB migration holds block_all for its whole run, and the
    // default is banner_only. If that default ever stops being weaker than
    // block_all this test should be revisited, so pin it.
    it('MaintenanceTab still defaults to a weaker block level than the migration relies on', () => {
        const source = readFileSync(join(__dirname, 'MaintenanceTab.tsx'), 'utf8');
        expect(source).toMatch(/const defaultState[\s\S]*?blockLevel:\s*'banner_only'/);
        expect(source).toMatch(/const defaultState[\s\S]*?active:\s*false/);
    });

    // The RolesTab editor is the sharpest case: its PUT replaces the panel role
    // AND both override sets, so a failed read could drop deny overrides and
    // thereby GRANT capabilities. Its guard must gate the body, not just the
    // button, so an empty assignment is never displayed as if it were real.
    it('RolesTab does not render an assignment it could not load', () => {
        const source = readFileSync(join(__dirname, 'RolesTab.tsx'), 'utf8');
        expect(source).toMatch(/loadFailed \?/);
    });
});
