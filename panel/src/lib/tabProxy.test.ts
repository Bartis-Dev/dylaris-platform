import { describe, it, expect, afterEach, vi } from 'vitest';
import { tabContentSrc, shareLinkUrl } from './tabProxy';

describe('tabContentSrc', () => {
    it('serves the tab at the ROOT of its own host', () => {
        // The root is the whole point. Under the path prefix this replaced, a
        // container's "/js/app.js" resolved against the origin root and missed
        // the prefix - which is what BlueMap and Dynmap emit, so neither could
        // be shown at all.
        expect(tabContentSrc('https://abcdefghij0123456789.share.example.com'))
            .toBe('https://abcdefghij0123456789.share.example.com/');
    });

    it('fails closed with no origin rather than falling back to a path', () => {
        // A same-origin fallback would put a tenant's container on the panel
        // origin, where its JavaScript can read the session token out of
        // localStorage. null makes the caller render an explanation instead.
        expect(tabContentSrc('')).toBeNull();
    });

    it('never emits a relative path', () => {
        for (const origin of ['', 'https://a.example.com']) {
            const got = tabContentSrc(origin);
            if (got !== null) expect(got.startsWith('http')).toBe(true);
        }
    });
});

describe('shareLinkUrl', () => {
    afterEach(() => { vi.unstubAllGlobals(); });

    it('is absolute in the browser so it can be copied and pasted', () => {
        vi.stubGlobal('window', { location: { origin: 'https://share.example.com' } });
        expect(shareLinkUrl('tok123')).toBe('https://share.example.com/c/tok123');
    });
});
