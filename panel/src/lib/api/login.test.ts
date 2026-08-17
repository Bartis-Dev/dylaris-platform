import { describe, it, expect, vi, afterEach } from 'vitest';
import { login } from './auth';

// A Response body is a one-shot stream: the SECOND json() throws, exactly like
// the real thing. That is the whole point of this file - login() used to read
// the body twice, and the second read's throw was reported to the user as
// "Request failed (401)" instead of the message the server actually sent.
function oneShot(status: number, body: object | null) {
    let read = false;
    return {
        ok: status >= 200 && status < 300,
        status,
        json: async () => {
            if (read) throw new TypeError('body stream already read');
            read = true;
            if (body === null) throw new SyntaxError('Unexpected token < in JSON');
            return body;
        },
    } as unknown as Response;
}

const withFetch = (res: Response) => {
    globalThis.fetch = vi.fn(async () => res) as unknown as typeof fetch;
};

afterEach(() => {
    vi.restoreAllMocks();
});

describe('login', () => {
    it('surfaces the server message on a rejected login', async () => {
        withFetch(oneShot(401, { success: false, message: 'Invalid username or password' }));
        const res = await login('nobody', 'wrong');
        expect(res.success).toBe(false);
        expect(res.message).toBe('Invalid username or password');
    });

    it('reports the status only when the body is not JSON', async () => {
        withFetch(oneShot(502, null));
        const res = await login('u', 'p');
        expect(res.success).toBe(false);
        expect(res.message).toBe('Request failed (502)');
    });

    it('routes a 401 carrying requires2FA to the code step', async () => {
        withFetch(oneShot(401, { requires2FA: true, message: '2FA code required' }));
        const res = await login('u', 'p');
        expect(res.success).toBe(false);
        expect((res as any).requires2FA).toBe(true);
        expect(res.message).toBe('2FA code required');
    });

    it('routes a 403 carrying requiresVerification with the email', async () => {
        withFetch(oneShot(403, { requiresVerification: true, email: 'a@b.c', message: 'Verify first' }));
        const res = await login('u', 'p');
        expect((res as any).requiresVerification).toBe(true);
        expect((res as any).email).toBe('a@b.c');
    });

    it('keeps the server message on a 403 that carries neither flag', async () => {
        withFetch(oneShot(403, { message: 'Account suspended' }));
        const res = await login('u', 'p');
        expect(res.success).toBe(false);
        expect(res.message).toBe('Account suspended');
    });

    it('returns the token on success', async () => {
        withFetch(oneShot(200, { success: true, token: 'jwt-value' }));
        const res = await login('u', 'p');
        expect(res.success).toBe(true);
        expect((res as any).token).toBe('jwt-value');
    });
});
