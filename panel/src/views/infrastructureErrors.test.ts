import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
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

const SRC = readFileSync(join(__dirname, 'InfrastructureView.tsx'), 'utf8');

describe('InfrastructureView keeps the service error streams', () => {
    it('stores res.errors instead of dropping it', () => {
        expect(SRC).toMatch(/errors:\s*flattenServiceErrors\(res\.errors\)/);
    });

    it('renders them', () => {
        expect(SRC).toContain('<ServiceErrorList');
    });

    it('offers a way to reach them', () => {
        // A payload kept in state and never reachable is the same defect one
        // layer along.
        expect(SRC).toMatch(/label:\s*'Errors'/);
    });
});
