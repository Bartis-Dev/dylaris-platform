import { describe, it, expect, afterEach, vi } from 'vitest';
import { saveCoreStorage } from './coreStorage';
import { handleResponse } from './core';
import type { CoreStorageConfig } from '@/lib/coreStorage';

const baseConfig: CoreStorageConfig = {
    backend: 'path', path: '/mnt/shared', pathConfirmed: true,
    s3Endpoint: '', s3Bucket: '', s3Region: '', s3AccessKey: '', s3SecretKey: '',
    s3PathStyle: false, s3Prefix: '', s3SecretSet: false, connectionId: 0,
};

const mockResponse = (body: object, ok: boolean) =>
    ({ ok, status: ok ? 200 : 409, json: async () => body }) as unknown as Response;

const stubFetchOnce = (body: object, ok: boolean) => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(mockResponse(body, ok)));
};

describe('saveCoreStorage', () => {
    afterEach(() => {
        vi.unstubAllGlobals();
    });

    it('proves the fix: handleResponse discards `round` on a refusal, saveCoreStorage keeps it', async () => {
        const round = {
            roundId: 'r1', confirmed: 1, total: 2, done: true, ok: false,
            results: [
                { coreId: 'core-a', status: 'ok' },
                { coreId: 'core-b', status: 'not-shared', missingPeers: ['core-a'] },
            ],
        };
        const body = { success: false, message: 'Only 1 of 2 Cores could prove they can read and write this storage.', round };

        // The OLD code path: routing this exact refusal body through the
        // shared handleResponse. Its non-2xx branch only keeps `message`.
        const viaHandleResponse = await handleResponse(mockResponse(body, false));
        expect((viaHandleResponse as { round?: unknown }).round).toBeUndefined();

        // The FIXED code path: saveCoreStorage parses the same body itself.
        stubFetchOnce(body, false);
        const res = await saveCoreStorage(baseConfig);
        expect(res.success).toBe(false);
        expect(res.round).toEqual(round);
    });

    it('keeps the success shape byte-identical to what handleResponse produced', async () => {
        stubFetchOnce({ success: true }, true);
        const res = await saveCoreStorage(baseConfig);
        expect(res).toEqual({ success: true });
    });

    it('falls back to "Unknown error" when the refusal body has no message, matching handleResponse', async () => {
        stubFetchOnce({}, false);
        const res = await saveCoreStorage(baseConfig);
        expect(res.success).toBe(false);
        expect(res.message).toBe('Unknown error');
        expect(res.round).toBeUndefined();
    });

    it('surfaces a 503 refusal round the same way as a 409', async () => {
        const round = { roundId: 'r2', confirmed: 0, total: 1, done: false, ok: false, results: [] };
        stubFetchOnce({ success: false, message: 'Could not verify shared storage access.', round }, false);
        const res = await saveCoreStorage(baseConfig);
        expect(res.round).toEqual(round);
    });
});
