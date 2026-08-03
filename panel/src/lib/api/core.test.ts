import { describe, it, expect } from 'vitest';
import { handleResponse, handleError } from './core';

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
