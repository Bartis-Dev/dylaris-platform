import { describe, it, expect } from 'vitest';
import { routeSubmitRequest } from './routeSubmit';

describe('routeSubmitRequest', () => {
    it('posts the picker value when creating', () => {
        const picked = { subdomain: 'survival', hosterDomain: 'dylaris.com', targetPort: 25565 };
        expect(routeSubmitRequest(null, picked, 25570)).toEqual({
            subdomain: 'survival',
            hosterDomain: 'dylaris.com',
            targetPort: 25570,
        });
    });

    // Editing is posting the SAME domain again - the only overwrite Core allows
    // on a route you own. A different domain would leave the original in place
    // and spend a second address out of the allowance.
    it('posts the edited domain, not whatever the picker holds', () => {
        const stale = { subdomain: 'creative', hosterDomain: 'dylaris.com', targetPort: 25565 };
        expect(routeSubmitRequest('survival.dylaris.com', stale, 25570)).toEqual({
            domain: 'survival.dylaris.com',
            targetPort: 25570,
        });
    });

    // Core resolves subdomain/hosterDomain/customDomain AHEAD of a plain domain,
    // so any of them surviving into an edit would decide which route is
    // rewritten - and it would not be the one on screen.
    it('carries no domain-building fields into an edit', () => {
        const stale = {
            subdomain: 'creative',
            hosterDomain: 'dylaris.com',
            customDomain: 'mc.example.com',
            targetPort: 25565,
        };
        const req = routeSubmitRequest('survival.dylaris.com', stale, 25565) as unknown as Record<string, unknown>;
        for (const key of ['subdomain', 'hosterDomain', 'customDomain']) {
            expect(req[key]).toBeUndefined();
        }
    });

    it('takes the port from the form in both modes', () => {
        expect(routeSubmitRequest('a.example.com', { targetPort: 1 }, 25599).targetPort).toBe(25599);
        expect(routeSubmitRequest(null, { targetPort: 1 }, 25599).targetPort).toBe(25599);
    });
});
