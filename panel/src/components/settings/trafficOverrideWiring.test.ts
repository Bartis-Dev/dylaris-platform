import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';

/**
 * The per-tenant traffic override is written from the user's billing dialog and
 * the platform default from the settings tab. They are two screens asking one
 * question, and every way that goes wrong is invisible on the screen itself.
 *
 * These read the source rather than render it, because what is being checked is
 * which value reaches the API - and a render shows the form, not the payload.
 */

const read = (p: string) => readFileSync(join(__dirname, p), 'utf8');

const usersTab = read('UsersTab.tsx');
const limitsTab = read('TrafficLimitsTab.tsx');

/** The body of the named function or arrow, up to the first line that closes it at its own indent. */
function bodyOf(src: string, marker: string): string {
    const start = src.indexOf(marker);
    expect(start, `${marker} not found - this test names a call site that no longer exists`).toBeGreaterThan(-1);
    const rest = src.slice(start);
    // setTrafficLimit calls are one statement; take enough to cover the object literal.
    const end = rest.indexOf('\n    };');
    return end === -1 ? rest : rest.slice(0, end);
}

describe('traffic override wiring', () => {
    it('has ONE allowance editor, used by both screens', () => {
        // A second copy would be free to drift into wording an operator reads
        // as a different question, and neither screen would look wrong.
        for (const [name, src] of [['UsersTab', usersTab], ['TrafficLimitsTab', limitsTab]] as const) {
            expect(src, `${name} must use the shared TrafficAllowanceFields`)
                .toContain("from '@/components/settings/TrafficAllowanceFields'");
        }
        // The editor owns the two LimitFields. A screen rendering its own pair
        // is the drift this guards against.
        expect(limitsTab).not.toContain('<LimitField');
    });

    it('the billing dialog writes the TENANT scope, never the platform default', () => {
        const save = bodyOf(usersTab, 'const saveTrafficOverride');
        expect(save).toContain('scope: trafficScope');
        // The one value that must never appear here: writing the default scope
        // from a per-user dialog would move every tenant's allowance at once.
        expect(save).not.toContain('user_default');
        expect(usersTab).toContain('const trafficScope = `user:${user.id}`');
    });

    it('the billing dialog encodes both values through writeFor', () => {
        // writeFor is what turns the checkbox plus a nullable number into the
        // three modes the API needs. Hand-building the modes is how "remove the
        // override" silently becomes "store a null", which stops the scope walk
        // instead of handing it back to the default - the opposite outcome, and
        // identical on screen.
        const save = bodyOf(usersTab, 'const saveTrafficOverride');
        expect(save).toContain('writeFor(tlCell.set, tlCell.includedGb)');
        expect(save).toContain('writeFor(tlCell.set, tlCell.maxPurchaseGb)');
        expect(save).toContain('includedMode: included.mode');
        expect(save).toContain('purchaseMode: purchase.mode');
    });

    it('the billing dialog stores a non-regional kind under the one region the resolver asks about', () => {
        // A relay row filed under "eu-central" is a number an operator set, saw
        // echoed back, and that enforces nothing.
        const save = bodyOf(usersTab, 'const saveTrafficOverride');
        expect(save).toContain('region: limitRegionFor(effectiveTrafficRegion, tlKind)');
    });
});
