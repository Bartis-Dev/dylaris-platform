import { describe, it, expect } from 'vitest';
import { leavesPage } from './navGuard';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';

describe('leavesPage', () => {
    it('clicking the page you are already on is not leaving', () => {
        expect(leavesPage('/settings/gateway', '/settings/gateway')).toBe(false);
    });

    it('a parent link leaves the child page that is showing', () => {
        // The navbar Settings button while a settings form is open. It is lit
        // up, so it looks inert - and it unmounts the form.
        expect(leavesPage('/settings/gateway', '/settings')).toBe(true);
        // The sidebar's server entry while that server's properties editor is
        // open. Same link, same highlight, same lost edits.
        expect(leavesPage('/servers/abc/config/properties', '/servers/abc')).toBe(true);
    });

    it('a child link leaves the parent page', () => {
        expect(leavesPage('/settings', '/settings/gateway')).toBe(true);
    });

    it('an unrelated page is leaving', () => {
        expect(leavesPage('/settings/gateway', '/servers/abc')).toBe(true);
    });

    it('a sibling that merely shares a prefix string is leaving', () => {
        // /settings/servers is not inside /settings/server.
        expect(leavesPage('/settings/servers', '/settings/server')).toBe(true);
    });

    it('an empty href cannot be proven to stay, so it is treated as leaving', () => {
        expect(leavesPage('/settings/gateway', '')).toBe(true);
    });
});

// The helper is only worth anything if the guards actually ask it. Both call
// sites carried the prefix expression inline, and that expression WAS the
// defect - so this pins that neither grew it back.
describe('the navigation guards ask it', () => {
    const src = (...parts: string[]) => readFileSync(join(__dirname, '..', ...parts), 'utf8');

    it('GuardedLink decides with leavesPage, and no longer waves an ancestor through', () => {
        const code = src('components', 'GuardedLink.tsx');
        expect(code).toMatch(/leavesPage\(pathname, hrefStr\)/);
        expect(code).not.toMatch(/startsWith\(hrefStr \+ '\/'\)/);
    });

    it('the settings nav decides with leavesPage', () => {
        const code = src('app', '(authed)', 'settings', 'layout.tsx');
        expect(code).toMatch(/leavesPage\(pathname, href\)/);
        // `isCurrentTab` was the local name for the prefix match. The same
        // expression is still right one line away, where it decides which nav
        // item is LIT UP - so this pins the guard, not the whole file.
        expect(code).not.toMatch(/isCurrentTab/);
    });
});
