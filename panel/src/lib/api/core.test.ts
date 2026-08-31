import { describe, it, expect, afterEach, vi } from 'vitest';
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
    afterEach(() => { vi.unstubAllGlobals(); });

    // The API base comes from what CORE injected, which is the only source left
    // now that the build-time variable is gone.
    const served = (apiUrl: string, origin = 'https://panel.example.com') => {
        vi.stubGlobal('window', { __DYLARIS_CONFIG__: { apiUrl }, location: { origin } });
    };

    // warp appends /api/warp/enroll itself, so the suffix has to come off.
    it('strips the /api suffix from the API base', () => {
        served('https://api.example.com/api');
        expect(coreOrigin()).toBe('https://api.example.com');
    });

    // The regression: the snippet used to be filled from window.location.origin,
    // which is the PANEL's host. On a split-host layout Core is a different
    // machine and the panel host serves no /api/warp/enroll at all. That layout
    // is not supported for browsers any more, but a configured API origin still
    // has to win over the page's own.
    it('follows the API host, not the host serving the panel', () => {
        served('https://api.example.com/api');
        expect(coreOrigin()).not.toContain('panel');
    });

    it('stays on one host when the API is served beside the panel', () => {
        served('https://panel.example.com/api');
        expect(coreOrigin()).toBe('https://panel.example.com');
    });

    // The normal case, and the one the deploy snippets are actually generated
    // in: nothing injected, so the API base is relative and there is no origin
    // in it to strip. The page's own is the answer, because that is where /api
    // was just fetched from - and an empty string here would put a compose file
    // in a customer's hands that enrolls against nothing.
    it('uses the page origin when the API base is relative', () => {
        vi.stubGlobal('window', { location: { origin: 'https://panel.example.com' } });
        expect(coreOrigin()).toBe('https://panel.example.com');
    });
});
