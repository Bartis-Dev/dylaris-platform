import { describe, it, expect, vi, afterEach } from 'vitest';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { getProfile } from './auth';

// `ready` in AppDataContext gates the ENTIRE authed app: until it flips, the
// layout renders a bare "Loading...". Two things go wrong at that gate and
// both were live on the testbed.
//
// 1. An unreachable API was read as an invalid session. getProfile caught
//    everything and returned null, and its only caller reads null as "log
//    out". MEASURED: `docker compose stop core`, reload the panel, and both
//    localStorage tokens were gone and the user was on /login - holding a
//    token that was still perfectly valid. A deploy logged out every open tab.
//
// 2. One failing or hanging boot slice held the whole app. init awaited
//    Promise.all over six refreshers, and refreshSettings awaits three calls
//    with no catch of its own, so a single rejection rejected init and
//    setReady(true) was never reached.

const realFetch = globalThis.fetch;

afterEach(() => {
    globalThis.fetch = realFetch;
    vi.restoreAllMocks();
});

describe('getProfile separates unreachable from unauthenticated', () => {
    it('rejects when the API cannot be reached', async () => {
        globalThis.fetch = vi.fn().mockRejectedValue(new TypeError('Failed to fetch')) as unknown as typeof fetch;
        // Not "resolves to null": null is the caller's signal to end the
        // session, and a network blip must never be able to produce it.
        await expect(getProfile()).rejects.toBeTruthy();
    });

    it('rejects when the request times out rather than hanging', async () => {
        globalThis.fetch = vi.fn().mockRejectedValue(
            Object.assign(new Error('signal timed out'), { name: 'TimeoutError' }),
        ) as unknown as typeof fetch;
        await expect(getProfile()).rejects.toBeTruthy();
    });

    it('resolves to null on a real 401, which IS a dead session', async () => {
        globalThis.fetch = vi.fn().mockResolvedValue({
            ok: false,
            status: 401,
            json: async () => ({ success: false, message: 'unauthorized' }),
        }) as unknown as typeof fetch;
        await expect(getProfile()).resolves.toBeNull();
    });

    it('passes an abort signal, or the timeout case above cannot happen', () => {
        const src = code(readFileSync(join(__dirname, 'auth.ts'), 'utf8'));
        const at = src.indexOf('export const getProfile');
        expect(at).toBeGreaterThan(-1);
        expect(src.slice(at, at + 400)).toMatch(/signal:\s*AbortSignal\.timeout\(/);
    });
});

// Full-line comments come out before any structural assertion. Both of these
// assertions passed against the comments explaining the fix on the first
// attempt - the catch block "mentions" onUnauthenticated only to say it must
// not call it, and the Promise.all note names setReady(true). Third time this
// exact trap has shown up in these tests: assert on the CALL, never on a
// source line that merely contains the name.
function code(src: string): string {
    return src.split('\n').filter(l => !l.trim().startsWith('//')).join('\n');
}

describe('the boot gate cannot be held open by one slice', () => {
    const ctx = code(readFileSync(join(__dirname, '..', 'AppDataContext.tsx'), 'utf8'));

    it('an unreachable API does not clear the session', () => {
        // The catch around the boot getProfile must NOT reach for
        // onUnauthenticated - that is what wiped the tokens.
        const at = ctx.indexOf('profile = await getProfile();', ctx.indexOf('const init ='));
        expect(at).toBeGreaterThan(-1);
        const handler = ctx.slice(at, at + 700);
        expect(handler).toMatch(/setApiUnreachable\(true\)/);
        const catchBlock = handler.slice(handler.indexOf('catch'), handler.indexOf('setApiUnreachable'));
        expect(catchBlock).not.toMatch(/onUnauthenticated/);
    });

    it('the boot data uses allSettled so one rejection cannot reject init', () => {
        expect(ctx).toMatch(/Promise\.allSettled\(\[refreshModules\(\)/);
        // Promise.all here is what rejected init and left `ready` false forever.
        expect(ctx.slice(ctx.indexOf('const init ='), ctx.indexOf('init();'))).not.toMatch(/Promise\.all\(\[refresh/);
    });

    it('and races a deadline so a hanging slice cannot hold it either', () => {
        // allSettled still waits forever on a promise that never settles, and
        // fetch has no default timeout.
        const init = ctx.slice(ctx.indexOf('const init ='), ctx.indexOf('init();'));
        expect(init).toMatch(/Promise\.race\(\[/);
        expect(init).toMatch(/GATE_TIMEOUT_MS/);
        // setReady must sit AFTER the race, not inside a branch that the
        // timeout path skips.
        expect(init.indexOf('setReady(true)')).toBeGreaterThan(init.indexOf('Promise.race(['));
    });
});
