import { describe, it, expect } from 'vitest';
import { readFileSync, existsSync } from 'node:fs';
import { join } from 'node:path';

// Core has always put the service error streams (edge, link, hub, beam, node,
// warp) into /api/infrastructure/overview as `errors`, and this view read the
// response and kept every field EXCEPT that one. Nothing failed anywhere: the
// producers wrote, the ACLs granted, Core scanned the right key names - there
// was even a named constant plus a test on the Go side making sure the scanned
// service names match the producers - and then the browser dropped the payload.
// Six components' diagnostics reached no screen at all.
//
// It cost a debugging session: a route-only address refused connections, and
// the ONLY line naming the cause ("failed to connect to 127.0.0.1:25550") was
// in the link's stream. The edge, where the operator looked, had nothing to
// say, because from the edge's side the proxy had succeeded.
//
// Asserted against the source rather than by rendering, matching
// lib/api/discardedResult.test.ts: the defect is a field silently going
// unused, which a render test passes straight over.
//
// The three claims now live in three files - the fetch, the tab bar and the
// page - so each one names its own site. Pointing all three at one file was
// what made this test survive the split while checking nothing.

const read = (p: string) => readFileSync(join(__dirname, p), 'utf8');
const CONTEXT = read('infrastructure/context.tsx');
const SHELL = read('infrastructure/Shell.tsx');
const ERRORS_PAGE = read('../app/(authed)/infrastructure/errors/page.tsx');

describe('the infrastructure screen keeps the service error streams', () => {
    it('stores res.errors instead of dropping it', () => {
        expect(CONTEXT).toMatch(/errors:\s*flattenServiceErrors\(res\.errors\)/);
    });

    it('renders them', () => {
        expect(ERRORS_PAGE).toContain('<ServiceErrorList');
    });

    it('offers a way to reach them', () => {
        // A payload kept in state and never reachable is the same defect one
        // layer along.
        expect(SHELL).toMatch(/slug:\s*'errors'/);
    });

    it('has a route behind that tab', () => {
        // A tab bar entry whose href resolves to nothing throws no error - it
        // renders as a link and 404s on click. The tab and the page are now two
        // separate files, so their agreement has to be asserted.
        expect(existsSync(join(__dirname, '../app/(authed)/infrastructure/errors/page.tsx'))).toBe(true);
    });
});
