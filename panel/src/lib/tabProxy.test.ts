import { describe, it, expect } from 'vitest';
import { tabDashboardProxySrc, tabProxyPageSrc, shareLinkUrl } from './tabProxy';

describe('tabProxy url builders', () => {
  it('dashboard src is a token-less path (cookie-only auth)', () => {
    expect(tabDashboardProxySrc(1, 2)).toBe('/api/servers/1/tabs/2/proxy/');
  });
  it('public page src fails closed (returns null) without an isolated origin', () => {
    // B5: a public share must never render same-origin, so no origin -> no src.
    expect(tabProxyPageSrc('TOK')).toBeNull();
    expect(tabProxyPageSrc('TOK', '')).toBeNull();
    expect(tabProxyPageSrc('TOK', undefined)).toBeNull();
  });
  it('dashboard src is absolute on the isolated origin when provided', () => {
    expect(tabDashboardProxySrc(1, 2, 'https://mc.example.com:25502')).toBe('https://mc.example.com:25502/api/servers/1/tabs/2/proxy/');
  });
  it('dashboard src stays relative when origin is empty (in-dashboard fallback by design)', () => {
    expect(tabDashboardProxySrc(1, 2, '')).toBe('/api/servers/1/tabs/2/proxy/');
    expect(tabDashboardProxySrc(1, 2)).toBe('/api/servers/1/tabs/2/proxy/');
  });
  it('public page src is absolute on the isolated origin when provided', () => {
    expect(tabProxyPageSrc('TOK', 'https://mc.example.com:25502')).toBe('https://mc.example.com:25502/api/tabproxy/TOK/');
  });
  it('public page src encodes the token on the isolated origin', () => {
    expect(tabProxyPageSrc('a b', 'https://mc.example.com:25502')).toBe('https://mc.example.com:25502/api/tabproxy/a%20b/');
  });
  it('shareLinkUrl builds a /c/ path', () => {
    expect(shareLinkUrl('TOK')).toContain('/c/TOK');
  });
});
