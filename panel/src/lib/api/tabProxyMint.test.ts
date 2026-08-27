import { describe, it, expect, afterEach, vi } from 'vitest';
import { mintTabProxyAuth } from './serverTabs';

// The mint is the panel's only cross-origin fetch, and the host it targets is
// derived from a setting the panel and Core each read from their OWN
// environment. When those two disagree the panel's CSP does not list the tab
// host, the browser refuses the call, and fetch throws before Core is asked.
//
// That is the single most likely misconfiguration of the whole feature, and it
// used to surface as "Connection failed" - which names the one component that
// is demonstrably fine. It cost a full debugging round once already.
describe('mintTabProxyAuth when the browser refuses the request', () => {
    afterEach(() => { vi.unstubAllGlobals(); });

    it('names the host and the setting instead of blaming the connection', async () => {
        vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('Failed to fetch')));
        const res = await mintTabProxyAuth('https://abcdefghij0123456789.share.example.com');
        expect(res.success).toBe(false);
        expect(res.message).toContain('abcdefghij0123456789.share.example.com');
        expect(res.message).toContain('TAB_PROXY_HOST_SUFFIX');
        expect(res.message).not.toContain('Connection failed');
    });

    // An HTTP answer means the request DID arrive, so the message must come
    // from Core. Blaming the configuration for a 403 would send the operator to
    // the wrong screen - the mirror image of the bug above.
    it("keeps Core's own message when Core actually answered", async () => {
        vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
            status: 403,
            json: async () => ({ message: 'Forbidden' }),
        }));
        const res = await mintTabProxyAuth('https://abcdefghij0123456789.share.example.com');
        expect(res.success).toBe(false);
        expect(res.status).toBe(403);
        expect(res.message).toBe('Forbidden');
    });

    it('does not call fetch at all without a proxy origin', async () => {
        const f = vi.fn();
        vi.stubGlobal('fetch', f);
        const res = await mintTabProxyAuth('');
        expect(res.success).toBe(false);
        expect(f).not.toHaveBeenCalled();
    });
});
