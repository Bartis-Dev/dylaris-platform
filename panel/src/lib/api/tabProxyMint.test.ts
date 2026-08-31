import { describe, it, expect, afterEach, vi } from 'vitest';
import { mintTabProxyAuth } from './serverTabs';

const TAB = { id: 2, serverId: 1, proxyOrigin: 'https://abcdefghij0123456789.share.example.com' };

const ticketOK = { ok: true, status: 200, json: async () => ({ success: true, ticket: 'tab-ticket', expiresIn: 300 }) };

afterEach(() => { vi.unstubAllGlobals(); });

// The mint has to CARRY something, and for a while it carried nothing.
//
// It used to send the session as a Bearer header, read straight out of
// localStorage. When the session moved into an HttpOnly cookie that header went
// empty - and the call still looked right at every level: same function, same
// URL, same credentials:'include'. Nothing threw. The cookie simply belongs to
// the panel's host and is never sent to a tab host, so Core saw an anonymous
// request and every private tab stopped opening.
//
// That is the failure this file exists to catch, and it is why the assertion is
// about the REQUEST rather than the result: a mint with no credential still
// returns a perfectly well-formed refusal.
describe('the mint carries a ticket to the content host', () => {
    it('asks Core for a ticket, then presents it as a Bearer', async () => {
        const f = vi.fn()
            .mockResolvedValueOnce(ticketOK)
            .mockResolvedValueOnce({ status: 204 });
        vi.stubGlobal('fetch', f);

        const res = await mintTabProxyAuth(TAB);
        expect(res.success).toBe(true);

        // Step one is on OUR origin - the only place the session cookie is sent.
        const [ticketURL, ticketInit] = f.mock.calls[0];
        expect(String(ticketURL)).toContain('/servers/1/tabs/2/proxy-ticket');
        expect(ticketInit.method).toBe('POST');

        // Step two is on theirs, and must carry the ticket step one produced.
        const [mintURL, mintInit] = f.mock.calls[1];
        expect(String(mintURL)).toBe(`${TAB.proxyOrigin}/__dyl/mint`);
        expect(mintInit.headers.Authorization).toBe('Bearer tab-ticket');
        // Without this the Set-Cookie does not stick and the frame stays unauthorized.
        expect(mintInit.credentials).toBe('include');
    });

    // A refusal at step one is about THIS caller and THIS tab, and it must not
    // be reported as a broken tab host - that sends the operator to DNS and TLS
    // for a permission problem.
    it('stops at the refusal and never touches the content host', async () => {
        const f = vi.fn().mockResolvedValueOnce({
            ok: false, status: 403, json: async () => ({ message: 'Forbidden' }),
        });
        vi.stubGlobal('fetch', f);

        const res = await mintTabProxyAuth(TAB);
        expect(res.success).toBe(false);
        expect(res.status).toBe(403);
        expect(res.message).toBe('Forbidden');
        expect(f).toHaveBeenCalledTimes(1);
    });
});

// The mint is the panel's only cross-origin fetch, and the host it targets is
// derived from a setting the panel and Core each read from their OWN
// environment. When those two disagree the panel's CSP does not list the tab
// host, the browser refuses the call, and fetch throws before Core is asked.
//
// That is the single most likely misconfiguration of the whole feature, and it
// used to surface as "Connection failed" - which names the one component that
// is demonstrably fine. It cost a full debugging round once already.
describe('mintTabProxyAuth when the browser refuses the request', () => {
    it('names the host and the setting instead of blaming the connection', async () => {
        vi.stubGlobal('fetch', vi.fn()
            .mockResolvedValueOnce(ticketOK)
            .mockRejectedValueOnce(new TypeError('Failed to fetch')));
        const res = await mintTabProxyAuth(TAB);
        expect(res.success).toBe(false);
        expect(res.message).toContain('abcdefghij0123456789.share.example.com');
        expect(res.message).toContain('TAB_PROXY_HOST_SUFFIX');
        expect(res.message).not.toContain('Connection failed');
    });

    // An HTTP answer means the request DID arrive, so the message must come
    // from Core. Blaming the configuration for a 403 would send the operator to
    // the wrong screen - the mirror image of the bug above.
    it("keeps Core's own message when Core actually answered", async () => {
        vi.stubGlobal('fetch', vi.fn()
            .mockResolvedValueOnce(ticketOK)
            .mockResolvedValueOnce({ status: 403, json: async () => ({ message: 'Forbidden' }) }));
        const res = await mintTabProxyAuth(TAB);
        expect(res.success).toBe(false);
        expect(res.status).toBe(403);
        expect(res.message).toBe('Forbidden');
    });

    it('does not call fetch at all without a proxy origin', async () => {
        const f = vi.fn();
        vi.stubGlobal('fetch', f);
        const res = await mintTabProxyAuth({ ...TAB, proxyOrigin: '' });
        expect(res.success).toBe(false);
        expect(f).not.toHaveBeenCalled();
    });
});
