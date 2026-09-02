import { afterEach, describe, expect, it, vi } from 'vitest';

import { listServerTabs } from './serverTabs';

afterEach(() => {
    vi.unstubAllGlobals();
});

function respond(status: number, body: unknown) {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify(body), {
        status,
        headers: { 'Content-Type': 'application/json' },
    })));
}

// An empty tab list is a statement, and three screens act on it: the shell
// removes the entries from the navigation, the settings page offers "No custom
// tabs yet" beside a Create button, and the tab page resolves an id against the
// list and renders "Tab not found. It may have been deleted."
//
// That last one is why this raises now. A tab that exists, reached while one
// request fails, told its owner it had been deleted - and the obvious next move
// is to go and build it again.
describe('listServerTabs', () => {
    it('raises instead of reporting that a server has no tabs', async () => {
        respond(502, { success: false, message: 'Bad gateway' });
        await expect(listServerTabs(1)).rejects.toThrow();
    });

    it('raises on a permission failure too', async () => {
        respond(403, { success: false, message: 'You may not view this server' });
        await expect(listServerTabs(1)).rejects.toThrow('You may not view this server');
    });

    it('returns the tabs on success', async () => {
        respond(200, { success: true, tabs: [{ id: 4, name: 'Map' }] });
        await expect(listServerTabs(1)).resolves.toHaveLength(1);
    });

    // A server that genuinely has none must still answer with an empty list -
    // this is the state the change must not swallow into an error.
    it('a server with no tabs is an empty list, not a failure', async () => {
        respond(200, { success: true, tabs: [] });
        await expect(listServerTabs(1)).resolves.toEqual([]);
    });
});
