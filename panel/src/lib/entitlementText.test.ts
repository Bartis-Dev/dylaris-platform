import { describe, it, expect } from 'vitest';
import { entitlementExplanation } from './entitlementText';
import type { Entitlement } from '@/lib/api/entitlement';

const e = (source: string, extra: Partial<Entitlement> = {}): Entitlement => ({
    byon: false, routeOnly: false, source, ...extra,
});

describe('entitlementExplanation', () => {
    // The distinction that matters operationally: "this user has nothing" is a
    // per-user problem, "nobody has defined plans" is a platform setup state and
    // means everyone currently has everything. Rendering both as "no" would send
    // the operator to the wrong screen.
    it('separates an unconfigured platform from an unentitled user', () => {
        const unlimited = entitlementExplanation(e('unlimited'));
        const none = entitlementExplanation(e('none'));
        expect(unlimited).not.toBe(none);
        expect(unlimited).toMatch(/no plans are defined/i);
        expect(none).toMatch(/neither their plan nor a grant/i);
    });

    it('names suspension as the cause when suspended', () => {
        expect(entitlementExplanation(e('suspended'))).toMatch(/suspended/i);
    });

    it('distinguishes plan, grant and both', () => {
        expect(entitlementExplanation(e('plan'))).toMatch(/their plan/i);
        expect(entitlementExplanation(e('grant'))).toMatch(/grant/i);
        expect(entitlementExplanation(e('plan+grant'))).toMatch(/plan plus/i);
    });

    it('says a grant-only access ends by itself, so nobody hunts for a revoke', () => {
        expect(entitlementExplanation(e('grant'))).toMatch(/ends on its own/i);
    });

    it('falls back to the no-access wording for an unknown source', () => {
        expect(entitlementExplanation(e('something-new'))).toMatch(/neither/i);
    });
});
