import { describe, it, expect } from 'vitest';
import { resolveInfraTab, showInfraTabBar } from './infraTab';

const both = { machines: true, routes: true };
const machinesOnly = { machines: true, routes: false };
const routesOnly = { machines: false, routes: true };
const neither = { machines: false, routes: false };

describe('resolveInfraTab', () => {
    it('defaults to machines', () => {
        expect(resolveInfraTab(null, both)).toBe('machines');
        expect(resolveInfraTab('machines', both)).toBe('machines');
    });

    it('honours an explicit routes request when routes exist', () => {
        expect(resolveInfraTab('routes', both)).toBe('routes');
    });

    // First bug: a bookmarked ?tab=routes opened a panel whose two endpoints
    // 409 without gateway routing. Hiding the tab bar was not enough, because
    // the URL still decided.
    it('ignores a routes request where there is no gateway routing', () => {
        expect(resolveInfraTab('routes', machinesOnly)).toBe('machines');
    });

    // Second bug: the page returned "your own hardware is turned off" on a
    // BYON-off platform, with the routes tab unreachable behind it - while the
    // top bar happily offered the page to route-only customers.
    it('goes straight to routes when there are no machines', () => {
        expect(resolveInfraTab(null, routesOnly)).toBe('routes');
        expect(resolveInfraTab('machines', routesOnly)).toBe('routes');
    });

    it('is null when neither half exists', () => {
        expect(resolveInfraTab(null, neither)).toBeNull();
        expect(resolveInfraTab('routes', neither)).toBeNull();
    });

    it('treats an unknown tab value as the default', () => {
        expect(resolveInfraTab('nonsense', both)).toBe('machines');
    });
});

describe('showInfraTabBar', () => {
    it('appears only when there are two halves to move between', () => {
        expect(showInfraTabBar(both)).toBe(true);
        expect(showInfraTabBar(machinesOnly)).toBe(false);
        expect(showInfraTabBar(routesOnly)).toBe(false);
        expect(showInfraTabBar(neither)).toBe(false);
    });
});
