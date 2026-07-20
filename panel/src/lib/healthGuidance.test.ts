import { describe, it, expect } from 'vitest';
import { healthGuidance } from './healthGuidance';

// The slugs Core emits, copied from core/database/rediserror.go
// (RedisFailure.Slug). Duplicated deliberately: this list is the contract, and
// a test that derived it from the implementation could not catch the
// implementation dropping one.
const CORE_REDIS_SLUGS = ['unreachable', 'auth', 'permission', 'server_error'];

describe('healthGuidance', () => {
    it('covers every failure class Core can report for redis', () => {
        for (const slug of CORE_REDIS_SLUGS) {
            const g = healthGuidance('redis', slug);
            expect(g, `no guidance for redis cause "${slug}"`).not.toBeNull();
            expect(g!.advice.length).toBeGreaterThan(0);
        }
    });

    it('marks the classes an operator has to fix as not self-healing', () => {
        // These are exactly the two that must not gate the container
        // healthcheck, because restarting Core cannot repair either one.
        expect(healthGuidance('redis', 'auth')!.selfHealing).toBe(false);
        expect(healthGuidance('redis', 'permission')!.selfHealing).toBe(false);
    });

    it('marks the classes that recover on their own as self-healing', () => {
        expect(healthGuidance('redis', 'unreachable')!.selfHealing).toBe(true);
        expect(healthGuidance('redis', 'server_error')!.selfHealing).toBe(true);
    });

    it('gives each class its own advice', () => {
        const seen = new Map<string, string>();
        for (const slug of CORE_REDIS_SLUGS) {
            const advice = healthGuidance('redis', slug)!.advice;
            const clash = seen.get(advice);
            expect(clash, `"${slug}" and "${clash}" share the same advice`).toBeUndefined();
            seen.set(advice, slug);
        }
    });

    it('tells an operator that a rejected credential has three possible causes', () => {
        // Redis answers WRONGPASS identically for a wrong password, an unknown
        // user and a disabled user. Advice naming only one sends the operator
        // to the wrong place, so all three have to be mentioned.
        const advice = healthGuidance('redis', 'auth')!.advice.toLowerCase();
        expect(advice).toContain('password');
        expect(advice).toContain('not exist');
        expect(advice).toContain('disabled');
    });

    it('returns null when there is nothing to add', () => {
        // No cause at all: every component except redis is in this state today.
        expect(healthGuidance('redis', undefined)).toBeNull();
        expect(healthGuidance('database', undefined)).toBeNull();
        // A component with no guidance table.
        expect(healthGuidance('gateway', 'unreachable')).toBeNull();
        // An unknown cause from a newer Core must degrade quietly rather than
        // render an empty guidance block.
        expect(healthGuidance('redis', 'something_new')).toBeNull();
    });
});
