import { describe, it, expect, vi, afterEach } from 'vitest';
import { API_URL } from './core';
import { backupDownloadUrl } from './types';

// The backup archive link is a plain anchor navigation, which cannot send the
// Authorization header. The URL must therefore carry the token in the
// querystring (the GET-only fallback AuthMiddleware accepts for downloads), or
// the request 401s. These tests pin that contract.

afterEach(() => {
    vi.unstubAllGlobals();
});

function stubLocalStorage(store: Record<string, string>) {
    vi.stubGlobal('window', {});
    vi.stubGlobal('localStorage', {
        getItem: (k: string) => (k in store ? store[k] : null),
    });
}

describe('backupDownloadUrl', () => {
    it('appends the authToken so a plain anchor navigation authenticates', () => {
        stubLocalStorage({ authToken: 'jwt-abc' });
        expect(backupDownloadUrl(8)).toBe(`${API_URL}/backup-runs/8/download?token=jwt-abc`);
    });

    it('falls back to the legacy "token" key when authToken is absent', () => {
        stubLocalStorage({ token: 'legacy-xyz' });
        expect(backupDownloadUrl(3)).toBe(`${API_URL}/backup-runs/3/download?token=legacy-xyz`);
    });

    it('URL-encodes a token that contains reserved characters', () => {
        stubLocalStorage({ authToken: 'a+b/c=d' });
        expect(backupDownloadUrl(1)).toContain(`?token=${encodeURIComponent('a+b/c=d')}`);
    });

    it('still emits the token param (empty) when there is no browser storage', () => {
        // node/SSR: no window -> guard yields an empty token, but the ?token= key
        // stays so the shape is stable and the server sees an (invalid) GET token
        // rather than no auth channel at all.
        expect(backupDownloadUrl(5)).toBe(`${API_URL}/backup-runs/5/download?token=`);
    });
});
