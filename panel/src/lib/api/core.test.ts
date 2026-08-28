import { describe, it, expect, afterEach } from 'vitest';
import { handleResponse, handleError, coreOrigin } from './core';

const mockResponse = (body: object, ok: boolean) =>
    ({ ok, json: async () => body }) as unknown as Response;

describe('handleResponse', () => {
    it('returns success: true and spreads data on 2xx', async () => {
        const result = await handleResponse(mockResponse({ data: 'test', count: 3 }, true));
        expect(result.success).toBe(true);
        expect((result as any).data).toBe('test');
        expect((result as any).count).toBe(3);
    });

    it('returns success: false with server message on error', async () => {
        const result = await handleResponse(mockResponse({ message: 'Not found' }, false));
        expect(result.success).toBe(false);
        expect(result.message).toBe('Not found');
    });

    it('falls back to "Unknown error" when message is missing', async () => {
        const result = await handleResponse(mockResponse({}, false));
        expect(result.success).toBe(false);
        expect(result.message).toBe('Unknown error');
    });
});

describe('handleError', () => {
    it('returns success: false with Connection failed message', () => {
        const result = handleError(new Error('network error'));
        expect(result.success).toBe(false);
        expect(result.message).toBe('Connection failed');
    });

    it('returns success: false for non-Error inputs', () => {
        const result = handleError('timeout');
        expect(result.success).toBe(false);
        expect(result.message).toBe('Connection failed');
    });
});

// Every API call in the panel funnels through handleResponse, so what it does
// with an unparseable body decides what the operator is told for a whole class
// of failures. It used to throw, which the callers' catch turned into
// handleError's "Connection failed" - about a server that had answered.
describe('handleResponse with a non-JSON body', () => {
    const nonJson = (status: number, ok: boolean) =>
        ({
            ok,
            status,
            json: async () => { throw new SyntaxError('Unexpected token < in JSON'); },
        }) as unknown as Response;

    it('reports the status instead of throwing on a text/plain error', async () => {
        const result = await handleResponse(nonJson(404, false));
        expect(result.success).toBe(false);
        expect(result.message).toBe('Request failed (404)');
    });

    it('distinguishes a gateway error from a not-found', async () => {
        const result = await handleResponse(nonJson(502, false));
        expect(result.message).toBe('Request failed (502)');
    });

    it('treats an empty successful body as success, not a failure', async () => {
        const result = await handleResponse(nonJson(204, true));
        expect(result.success).toBe(true);
    });

    it('still prefers a server-supplied message when the body does parse', async () => {
        const result = await handleResponse(mockResponse({ message: 'Not found' }, false));
        expect(result.message).toBe('Not found');
    });
});

// coreOrigin feeds the BYON / route-only deploy snippets, where a wrong value
// is not a broken screen but a compose file the customer runs on their own
// machine and that then fails somewhere else entirely.
describe('coreOrigin', () => {
    const saved = process.env.NEXT_PUBLIC_API_URL;
    afterEach(() => {
        if (saved === undefined) delete process.env.NEXT_PUBLIC_API_URL;
        else process.env.NEXT_PUBLIC_API_URL = saved;
    });

    // warp appends /api/warp/enroll itself, so the suffix has to come off.
    it('strips the /api suffix from the API base', () => {
        process.env.NEXT_PUBLIC_API_URL = 'https://api.example.com/api';
        expect(coreOrigin()).toBe('https://api.example.com');
    });

    // The regression: the snippet used to be filled from window.location.origin,
    // which is the PANEL's host. On the split-host layout Core is a different
    // machine and the panel host serves no /api/warp/enroll at all.
    it('follows the API host, not the host serving the panel', () => {
        process.env.NEXT_PUBLIC_API_URL = 'https://api.example.com/api';
        expect(coreOrigin()).not.toContain('panel');
    });

    // Same-origin installs are the case that made the old code look correct.
    it('stays on one host when the API is served beside the panel', () => {
        process.env.NEXT_PUBLIC_API_URL = 'https://panel.example.com/api';
        expect(coreOrigin()).toBe('https://panel.example.com');
    });
});
