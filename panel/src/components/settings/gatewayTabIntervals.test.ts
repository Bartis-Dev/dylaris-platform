import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';

// The panel test suite is logic-only (no jsdom, no @testing-library), so this
// reads the source the way the Go side reads its command switch: it is a guard
// against the pattern coming back, not a render test.
const source = readFileSync(join(__dirname, 'GatewayTab.tsx'), 'utf8');

describe('GatewayTab interval cleanup', () => {
    // The routing-migration poll clears itself only when the run reports
    // finished. Without an unmount cleanup, navigating away mid-migration left
    // it polling every 3s and calling setState on an unmounted component. The
    // other interval in this file gets it right, which is what made the gap
    // easy to miss.
    it('clears the migration poll when the tab unmounts', () => {
        expect(source).toMatch(/useEffect\(\(\)\s*=>\s*\(\)\s*=>\s*\{[^}]*clearInterval\(pollRef\.current\)/);
    });

    // Every setInterval here must have a matching clearInterval, so a third one
    // added later without cleanup shows up as a mismatch rather than as a
    // silent leak.
    it('balances setInterval against clearInterval', () => {
        const sets = source.match(/\bsetInterval\(/g) ?? [];
        const clears = source.match(/\bclearInterval\(/g) ?? [];
        expect(clears.length).toBeGreaterThanOrEqual(sets.length);
    });
});
