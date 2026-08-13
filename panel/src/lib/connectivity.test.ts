import { describe, it, expect } from 'vitest';
import { nodeConnectivity, dotFor, connLabel } from './connectivity';

const NOW = 1_730_000_000_000;
const iso = (msAgo: number) => new Date(NOW - msAgo).toISOString();

describe('nodeConnectivity', () => {
  it('online -> ok', () => {
    expect(nodeConnectivity('online', iso(999_999), NOW).tier).toBe('ok');
  });
  it('unknown/undefined/absent status -> ok (rollout-safe)', () => {
    expect(nodeConnectivity(undefined, iso(999_999), NOW).tier).toBe('ok');
    expect(nodeConnectivity('', iso(999_999), NOW).tier).toBe('ok');
  });
  it('offline < 60s -> reconnecting', () => {
    expect(nodeConnectivity('offline', iso(30_000), NOW).tier).toBe('reconnecting');
  });
  it('offline 60s-5min -> unreachable', () => {
    expect(nodeConnectivity('offline', iso(90_000), NOW).tier).toBe('unreachable');
  });
  it('offline >= 5min -> down', () => {
    expect(nodeConnectivity('offline', iso(6 * 60_000), NOW).tier).toBe('down');
  });
  it('boundaries: exactly 60s -> unreachable, exactly 5min -> down', () => {
    expect(nodeConnectivity('offline', iso(60_000), NOW).tier).toBe('unreachable');
    expect(nodeConnectivity('offline', iso(300_000), NOW).tier).toBe('down');
  });
  it('offline with no last-seen -> down', () => {
    expect(nodeConnectivity('offline', null, NOW).tier).toBe('down');
  });
});

describe('dotFor', () => {
  it('ok tier returns the passed server-status class unchanged', () => {
    expect(dotFor('ok', 'bg-(--success-light)')).toBe('bg-(--success-light)');
  });
  it('non-ok tiers return the connectivity tone, ignoring the server class', () => {
    expect(dotFor('reconnecting', 'bg-(--success-light)')).toBe('bg-(--warning) animate-pulse');
    expect(dotFor('unreachable', 'bg-(--success-light)')).toBe('bg-(--warning)');
    expect(dotFor('down', 'bg-(--success-light)')).toBe('bg-(--error)');
  });
});

describe('connLabel', () => {
  it('ok -> empty, reconnecting -> Reconnecting...', () => {
    expect(connLabel('ok', new Date().toISOString())).toBe('');
    expect(connLabel('reconnecting', new Date().toISOString())).toBe('Reconnecting...');
  });
  it('unreachable/down include a shared-timeAgo last-seen and roll to days', () => {
    const twoDaysAgo = new Date(Date.now() - 2 * 24 * 3600 * 1000).toISOString();
    expect(connLabel('down', twoDaysAgo)).toContain('last seen 2d ago');
    expect(connLabel('unreachable', null)).toBe('Node not responding (node or its warp tunnel)');
  });
});
