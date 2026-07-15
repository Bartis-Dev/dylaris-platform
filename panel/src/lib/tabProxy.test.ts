import { describe, it, expect } from 'vitest';
import { tabDashboardProxySrc, tabProxyPageSrc, shareLinkUrl } from './tabProxy';

describe('tabProxy url builders', () => {
  it('dashboard src is a token-less path (cookie-only auth)', () => {
    expect(tabDashboardProxySrc(1, 2)).toBe('/api/servers/1/tabs/2/proxy/');
  });
  it('page src is a token-less path keyed only by the share token', () => {
    expect(tabProxyPageSrc('TOK')).toBe('/api/tabproxy/TOK/');
  });
  it('page src encodes special characters in the share token', () => {
    expect(tabProxyPageSrc('a b')).toBe('/api/tabproxy/a%20b/');
  });
  it('dashboard src is absolute on the isolated origin when provided', () => {
    expect(tabDashboardProxySrc(1, 2, 'https://mc.example.com:25502')).toBe('https://mc.example.com:25502/api/servers/1/tabs/2/proxy/');
  });
  it('dashboard src stays relative when origin is empty', () => {
    expect(tabDashboardProxySrc(1, 2, '')).toBe('/api/servers/1/tabs/2/proxy/');
  });
  it('page src is absolute on the isolated origin when provided', () => {
    expect(tabProxyPageSrc('TOK', 'https://mc.example.com:25502')).toBe('https://mc.example.com:25502/api/tabproxy/TOK/');
  });
  it('page src encodes the token even on the isolated origin', () => {
    expect(tabProxyPageSrc('a b', 'https://mc.example.com:25502')).toBe('https://mc.example.com:25502/api/tabproxy/a%20b/');
  });
  it('shareLinkUrl builds a /c/ path', () => {
    expect(shareLinkUrl('TOK')).toContain('/c/TOK');
  });
});
