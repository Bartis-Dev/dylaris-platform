import { describe, it, expect, vi, afterEach } from 'vitest';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { getSetupStatus } from './setup';

// The authed layout awaits getSetupStatus before it renders anything, so this
// request decides whether the panel appears at all. Its catch was written for
// "hard transport failure" - but a host that accepts the connection and never
// answers does not reject, and fetch has no default timeout, so that case left
// the whole panel in its skeleton shell with no error and no retry.
//
// Reproduced live: the testbed panel calls localhost:25500, Chromium resolves
// that to ::1, only the IPv4 path is wired, and the request hangs forever.

const realFetch = globalThis.fetch;

afterEach(() => {
    globalThis.fetch = realFetch;
    vi.restoreAllMocks();
});

describe('getSetupStatus cannot hang the panel', () => {
    it('passes an abort signal so a no-answer host cannot leave it pending', () => {
        // The behavioural test below proves the catch handles an abort; this
        // proves an abort can actually happen. Without the signal there is
        // nothing to reject and the assertion above is vacuous.
        const source = readFileSync(join(__dirname, 'setup.ts'), 'utf8');
        expect(source).toMatch(/signal:\s*AbortSignal\.timeout\(/);
    });

    it('falls back to complete when the request times out', async () => {
        globalThis.fetch = vi.fn().mockRejectedValue(
            Object.assign(new Error('signal timed out'), { name: 'TimeoutError' }),
        ) as unknown as typeof fetch;

        const status = await getSetupStatus();

        // Not 'fresh_install': a backend we cannot reach must not be read as a
        // backend that has never been set up, or an unreachable API would send
        // an existing installation into the first-run wizard.
        expect(status.mode).toBe('complete');
        expect(status.success).toBe(false);
    });

    it('still reports a real status when the request answers', async () => {
        globalThis.fetch = vi.fn().mockResolvedValue({
            ok: true,
            status: 200,
            text: async () => JSON.stringify({ success: true, mode: 'fresh_install', adminSecretConfigured: false }),
            json: async () => ({ success: true, mode: 'fresh_install', adminSecretConfigured: false }),
        }) as unknown as typeof fetch;

        const status = await getSetupStatus();

        expect(status.mode).toBe('fresh_install');
    });
});
