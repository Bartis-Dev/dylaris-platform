import { afterEach, describe, expect, it, vi } from 'vitest';

import { getLinkUpdateStates } from './linkUpdates';
import { listInstalledMods, getServerModpackContents } from './modrinth';

afterEach(() => {
    vi.unstubAllGlobals();
});

function respond(status: number, body: unknown) {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify(body), {
        status,
        headers: { 'Content-Type': 'application/json' },
    })));
}

// getLinkUpdateStates answers with a BARE map on success, so the usual
// `data.success` test is not available to it - the failure envelope is what has
// to be recognised. The `|| {}` this replaced could never fire, because an
// envelope is a perfectly truthy object: it was returned AS the state map, every
// node lookup missed it, and the panel concluded that no node was reporting.
describe('getLinkUpdateStates', () => {
    it('raises rather than passing the failure envelope off as node states', async () => {
        respond(403, { success: false, message: 'You may not read nodes' });
        await expect(getLinkUpdateStates()).rejects.toThrow('You may not read nodes');
    });

    it('returns the map on success', async () => {
        respond(200, { 'node-token': { managed: true, updateAvailable: true } });
        const states = await getLinkUpdateStates();
        expect(states['node-token']?.updateAvailable).toBe(true);
    });

    // No node has reported yet, which is a real state and the panel explains it.
    it('an empty map is a state, not a failure', async () => {
        respond(200, {});
        await expect(getLinkUpdateStates()).resolves.toEqual({});
    });
});

// The two mod lists are deliberately NOT alike, and this pins the difference so
// that "make them consistent" cannot quietly remove it.
describe('the two mod lists fail differently on purpose', () => {
    it('listInstalledMods raises: the tab reads it to decide what is already there', async () => {
        respond(500, { success: false, message: 'Could not list mods' });
        await expect(listInstalledMods(1)).rejects.toThrow('Could not list mods');
    });

    it('listInstalledMods returns the mods on success', async () => {
        respond(200, { success: true, mods: [{ id: 1, fileName: 'spark.jar' }] });
        await expect(listInstalledMods(1)).resolves.toHaveLength(1);
    });

    // getServerModpackContents stays fail-open, and its own comment says why:
    // the cross-check is advisory, so the tab has to keep working without the
    // snapshot. An advisory decoration missing is not a claim about anything.
    it('getServerModpackContents still fails open', async () => {
        respond(500, { success: false, message: 'no snapshot' });
        await expect(getServerModpackContents(1)).resolves.toEqual([]);
    });
});
