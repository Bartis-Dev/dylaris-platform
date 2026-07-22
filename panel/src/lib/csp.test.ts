import { describe, it, expect } from 'vitest';
import { buildCsp } from './csp';

describe('buildCsp', () => {
  const nonce = 'TESTNONCE123';

  it('prod: script-src is nonce-strict with strict-dynamic and no unsafe-inline/eval', () => {
    const csp = buildCsp(nonce, false);
    expect(csp).toContain(
      `script-src 'self' 'nonce-${nonce}' 'strict-dynamic' https://cdnjs.cloudflare.com`,
    );
    const scriptSrc = csp.split('; ').find(d => d.startsWith('script-src '))!;
    expect(scriptSrc).not.toContain("'unsafe-inline'");
    expect(scriptSrc).not.toContain("'unsafe-eval'");
  });

  it('dev: adds unsafe-eval to script-src (and keeps the nonce)', () => {
    const scriptSrc = buildCsp(nonce, true).split('; ').find(d => d.startsWith('script-src '))!;
    expect(scriptSrc).toContain("'unsafe-eval'");
    expect(scriptSrc).toContain(`'nonce-${nonce}'`);
  });

  it('interpolates the nonce into script-src', () => {
    expect(buildCsp('abc123', false)).toContain("'nonce-abc123'");
  });

  it('mirrors the beam img/connect/frame allowances and locks object/base/style', () => {
    const csp = buildCsp(nonce, false);
    expect(csp).toContain("default-src 'self'");
    expect(csp).toContain("style-src 'self' 'unsafe-inline'");
    expect(csp).toContain("img-src 'self' data: blob: https://cravatar.eu https://cdn.modrinth.com");
    expect(csp).toContain("font-src 'self'");
    expect(csp).toContain("connect-src 'self'");
    expect(csp).toContain("object-src 'none'");
    expect(csp).toContain("base-uri 'none'");
    expect(csp).toContain("form-action 'self'");
    expect(csp).toContain('frame-src https: http:');
    expect(csp).toContain("frame-ancestors 'none'");
  });

  it('no apiOrigin: connect-src is self-only (no vendor host)', () => {
    const csp = buildCsp(nonce, false);
    const connectSrc = csp.split('; ').find(d => d.startsWith('connect-src '))!;
    expect(connectSrc).toBe("connect-src 'self'");
  });

  it('cross-origin apiOrigin is appended to connect-src', () => {
    const csp = buildCsp(nonce, false, 'http://localhost:25500');
    const connectSrc = csp.split('; ').find(d => d.startsWith('connect-src '))!;
    expect(connectSrc).toBe("connect-src 'self' http://localhost:25500");
  });
});
