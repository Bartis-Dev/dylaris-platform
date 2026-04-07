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
