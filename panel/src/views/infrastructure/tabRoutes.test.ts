import { describe, expect, it } from 'vitest';
import { existsSync, readFileSync } from 'node:fs';
import { join } from 'node:path';

/**
 * The infrastructure tabs became real URLs, and that changed what "hidden"
 * means.
 *
 * Before, a tab that should not exist had no button, and NOT DRAWING IT was the
 * whole check - there was no other way in. Now every tab is one typed address
 * away, so a page whose only protection was a missing button is a page with no
 * protection. This is the same shape as the SFTP surface that read a grant table
 * instead of the resolver: the guard sat where the door was described rather
 * than on the door.
 *
 * Two things are asserted, and neither is visible on screen when it breaks:
 * every tab the bar can draw has a page behind it, and every gated tab's page
 * refuses on its own.
 */

const APP = join(__dirname, '../../app/(authed)/infrastructure');
const SHELL = readFileSync(join(__dirname, 'Shell.tsx'), 'utf8');

/** The slugs the tab bar can render, read from the shell rather than restated. */
function shellSlugs(): string[] {
    const out = [...SHELL.matchAll(/slug:\s*'([a-z-]+)'/g)].map(m => m[1]);
    // A restatement of the list would pass forever after somebody adds a tab.
    expect(out.length, 'no slugs found in Shell.tsx - this test stopped reading it').toBeGreaterThan(0);
    return out;
}

/** Tabs that must not render when their backend is absent, and what gates them. */
const GATED: Record<string, string> = {
    edges: 'gatewayDeployed',
    routes: 'gatewayDeployed',
    bandwidth: 'gatewayEnabled',
};

describe('infrastructure tab routes', () => {
    it('every tab the bar can draw has a page behind it', () => {
        for (const slug of shellSlugs()) {
            expect(existsSync(join(APP, slug, 'page.tsx')), `no page for tab "${slug}"`).toBe(true);
        }
    });

    it('includes the three node kinds as separate tabs', () => {
        const slugs = shellSlugs();
        for (const s of ['nodes', 'external', 'byon']) expect(slugs).toContain(s);
    });

    it('the three node pages ask for different kinds', () => {
        const kindOf = (slug: string) =>
            readFileSync(join(APP, slug, 'page.tsx'), 'utf8').match(/kind="([a-z]+)"/)?.[1];
        expect(kindOf('nodes')).toBe('platform');
        expect(kindOf('external')).toBe('external');
        expect(kindOf('byon')).toBe('byon');
    });

    it('every gated page guards itself rather than relying on the tab bar', () => {
        for (const [slug, gate] of Object.entries(GATED)) {
            const src = readFileSync(join(APP, slug, 'page.tsx'), 'utf8');
            expect(src, `${slug} does not read ${gate}`).toContain(gate);
            expect(src, `${slug} does not refuse when ${gate} is false`).toContain('TabGuard');
        }
    });

    it('the index redirects rather than becoming an address of its own', () => {
        // Two URLs showing one screen is how a link somebody shared stops
        // matching the tab that is highlighted.
        const idx = readFileSync(join(APP, 'page.tsx'), 'utf8');
        expect(idx).toContain('redirect(');
        expect(idx).toContain('/infrastructure/nodes');
    });

    it('the admin check and the single fetch live in the layout', () => {
        // One fetch for the whole screen: a page fetching for itself turns one
        // overview request into one per tab switch. And the admin check has to
        // cover every tab, not the landing one.
        const layout = readFileSync(join(APP, 'layout.tsx'), 'utf8');
        expect(layout).toContain('user?.isAdmin');
        expect(layout).toContain('InfraProvider');
    });
});
