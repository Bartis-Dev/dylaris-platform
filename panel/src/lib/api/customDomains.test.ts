import { afterEach, describe, expect, it, vi } from 'vitest';

import {
    listCustomDomains,
    issueCustomDomainToken,
    verifyCustomDomainTXT,
} from './customDomains';

// Core answers a failed request with a JSON envelope and a status. handleResponse
// turns that into a RESOLVED { success: false, message } - deliberately, so a
// caller can render the message instead of "Connection failed".
function respond(status: number, body: unknown) {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify(body), {
        status,
        headers: { 'Content-Type': 'application/json' },
    })));
}

afterEach(() => {
    vi.unstubAllGlobals();
});

// Every one of these used to flatten a failure into a value that reads as a
// perfectly ordinary state, and the three states they flattened to are the three
// answers a customer bringing their own domain most needs to trust:
//
//   list    -> []         "you have no custom domains"
//   token   -> undefined  the unblock button does nothing, silently
//   verify  -> resolves   the check succeeded
//
// The list one is the worst, because CustomDomainsPanel renders NOTHING for an
// empty list. A 403 or a 502 removed the whole section, and with it the TXT
// record that is the only way back from a permanent block.
describe('a failed custom-domain request is not a state', () => {
    it('listCustomDomains raises rather than reporting no domains', async () => {
        respond(500, { success: false, message: 'Could not load your custom domains' });
        await expect(listCustomDomains()).rejects.toThrow('Could not load your custom domains');
    });

    it('listCustomDomains still returns the domains on success', async () => {
        respond(200, { success: true, domains: [{ domain: 'a.example', state: 'pending', attempts: 0 }] });
        await expect(listCustomDomains()).resolves.toHaveLength(1);
    });

    it('a success carrying no domains is an empty list, not an error', async () => {
        respond(200, { success: true });
        await expect(listCustomDomains()).resolves.toEqual([]);
    });

    it('issueCustomDomainToken raises rather than returning undefined', async () => {
        respond(404, { success: false, message: 'No claim on that domain for your account' });
        await expect(issueCustomDomainToken('a.example')).rejects.toThrow('No claim on that domain');
    });

    // 409 is the case that actually happens: the record is published but has not
    // propagated. It has to reach the customer as "not yet", never as a tick.
    it('verifyCustomDomainTXT raises on the 409 that means "not visible yet"', async () => {
        respond(409, {
            success: false,
            message: 'The TXT record was not found yet. DNS changes can take a few minutes to publish.',
        });
        await expect(verifyCustomDomainTXT('a.example')).rejects.toThrow('not found yet');
    });

    it('verifyCustomDomainTXT returns the message when it really did verify', async () => {
        respond(200, { success: true, message: 'Ownership verified. You can add routes on a.example again.' });
        await expect(verifyCustomDomainTXT('a.example')).resolves.toContain('Ownership verified');
    });

    // A proxy in front of Core answers with HTML, which parses as nothing. That
    // path has no message to carry, and it must still not read as success.
    it('a non-JSON failure body still raises', async () => {
        vi.stubGlobal('fetch', vi.fn(async () => new Response('<html>502</html>', { status: 502 })));
        await expect(listCustomDomains()).rejects.toThrow();
    });
});
