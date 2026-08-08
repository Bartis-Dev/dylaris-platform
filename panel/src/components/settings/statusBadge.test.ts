import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';

// Every card in the User Management tab carried the literal string "Active" in a
// green pill. On three of the five cards that was false: the auto-delete job
// whose master toggle was off, the SMTP profile with no host (mailer.LoadConfig
// refuses on an empty host, so every verification mail fails), and the audit
// retention horizon that no sweep applies until it has been saved.
//
// A status badge that is a constant cannot be wrong only sometimes - it is
// either always true or sometimes a lie, so an unconditional one has to say out
// loud why it is always true.
//
// The suite is logic-only (no jsdom), so this reads the source: it guards
// against the constant coming back, not against a render.

const SOURCE = readFileSync(join(__dirname, 'UserManagementTab.tsx'), 'utf8');

describe('the settings card status badges report state, not a constant', () => {
    it('no card hardcodes the green Active pill any more', () => {
        expect(SOURCE).not.toMatch(/rounded-sm">Active</);
    });

    it('the three stateful cards drive the badge from what is in force', () => {
        // Not from the form being edited: flipping a toggle and walking away
        // without saving must not move the badge.
        expect(SOURCE).toContain('<StatusBadge active={sending}');
        expect(SOURCE).toContain('<StatusBadge active={running}');
        expect(SOURCE).toContain('<StatusBadge active={inForceDays > 0}');
    });

    it('every unconditional badge is justified on the line above it', () => {
        const lines = SOURCE.split('\n');
        const unjustified: number[] = [];
        lines.forEach((line, i) => {
            if (!/<StatusBadge\s+active\s*\/>/.test(line)) return;
            if (!/Always in force/.test(lines[i - 1] ?? '')) unjustified.push(i + 1);
        });
        expect(unjustified).toEqual([]);
    });

    it('an inactive badge is visually distinct, not just differently worded', () => {
        // Same-green-different-text would read as "on" at a glance.
        expect(SOURCE).toMatch(/active\s*\?\s*'text-\(--success-light\) bg-\(--success-ghost\)'\s*:\s*'text-\(--base-06\) bg-\(--base-03\)'/);
    });
});
