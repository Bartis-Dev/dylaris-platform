import { describe, it, expect } from 'vitest';
import {
  applyStorageConnectionEvent,
  normaliseBackendConnection,
  normaliseStorageConnection,
  selectStorageBanner,
  INITIAL_STORAGE_CONNECTION,
  type StorageConnectionState,
} from './storageConnection';

const allOk: StorageConnectionState = {
  path: { state: 'ok', since: null },
  s3: { state: 'ok', since: null },
};

describe('normaliseBackendConnection', () => {
  it('reads a well-formed degraded value', () => {
    expect(normaliseBackendConnection({ state: 'unavailable', since: '2026-07-20T10:00:00Z' }))
      .toEqual({ state: 'unavailable', since: '2026-07-20T10:00:00Z' });
  });

  // Garbage must never invent an outage: the banner is an interrupt, and a
  // false one is worse than a missed one.
  it.each([
    ['null', null],
    ['undefined', undefined],
    ['a string', 'unavailable'],
    ['a number', 42],
    ['an array', []],
    ['an empty object', {}],
    ['a missing state key', { since: '2026-07-20T10:00:00Z' }],
    ['a non-string state', { state: 7 }],
    ['an unknown state word', { state: 'exploded' }],
    ['a null state', { state: null }],
  ])('defaults to ok on %s without throwing', (_label, input) => {
    expect(() => normaliseBackendConnection(input)).not.toThrow();
    expect(normaliseBackendConnection(input)).toEqual({ state: 'ok', since: null });
  });

  it('drops a non-string since', () => {
    expect(normaliseBackendConnection({ state: 'reconnecting', since: 12345 }))
      .toEqual({ state: 'reconnecting', since: null });
  });

  it('forces since to null when the state is ok', () => {
    expect(normaliseBackendConnection({ state: 'ok', since: '2026-07-20T10:00:00Z' }))
      .toEqual({ state: 'ok', since: null });
  });
});

describe('normaliseStorageConnection', () => {
  it('reads a well-formed endpoint body', () => {
    expect(normaliseStorageConnection({
      path: { state: 'ok', since: null },
      s3: { state: 'reconnecting', since: '2026-07-20T10:00:00Z' },
    })).toEqual({
      path: { state: 'ok', since: null },
      s3: { state: 'reconnecting', since: '2026-07-20T10:00:00Z' },
    });
  });

  it.each([
    ['null', null],
    ['a string', 'nope'],
    ['an empty object', {}],
    ['only one key present', { path: { state: 'ok' } }],
  ])('survives %s and reports both backends ok', (_label, input) => {
    expect(() => normaliseStorageConnection(input)).not.toThrow();
    expect(normaliseStorageConnection(input)).toEqual(INITIAL_STORAGE_CONNECTION);
  });
});

describe('applyStorageConnectionEvent', () => {
  // The copy-paste guard: a path event must not land on the s3 half, and the
  // untouched half must survive byte-for-byte.
  it('applies a path event without disturbing s3', () => {
    const start: StorageConnectionState = {
      path: { state: 'ok', since: null },
      s3: { state: 'reconnecting', since: '2026-07-20T09:00:00Z' },
    };
    const next = applyStorageConnectionEvent(start, {
      backend: 'path', state: 'unavailable', since: '2026-07-20T10:00:00Z',
    });
    expect(next.path).toEqual({ state: 'unavailable', since: '2026-07-20T10:00:00Z' });
    expect(next.s3).toEqual({ state: 'reconnecting', since: '2026-07-20T09:00:00Z' });
  });

  it('applies an s3 event without disturbing path', () => {
    const start: StorageConnectionState = {
      path: { state: 'unavailable', since: '2026-07-20T09:00:00Z' },
      s3: { state: 'ok', since: null },
    };
    const next = applyStorageConnectionEvent(start, {
      backend: 's3', state: 'reconnecting', since: '2026-07-20T10:00:00Z',
    });
    expect(next.s3).toEqual({ state: 'reconnecting', since: '2026-07-20T10:00:00Z' });
    expect(next.path).toEqual({ state: 'unavailable', since: '2026-07-20T09:00:00Z' });
  });

  it('clears one half on recovery and leaves the other degraded', () => {
    const start: StorageConnectionState = {
      path: { state: 'unavailable', since: '2026-07-20T09:00:00Z' },
      s3: { state: 'reconnecting', since: '2026-07-20T09:30:00Z' },
    };
    const next = applyStorageConnectionEvent(start, { backend: 'path', state: 'ok', since: null });
    expect(next.path).toEqual({ state: 'ok', since: null });
    expect(next.s3).toEqual({ state: 'reconnecting', since: '2026-07-20T09:30:00Z' });
  });

  it.each([
    ['null', null],
    ['a string', 'storage.connection.changed'],
    ['a missing backend key', { state: 'unavailable' }],
    ['an unknown backend', { backend: 'gluster', state: 'unavailable' }],
    ['a non-string backend', { backend: 3, state: 'unavailable' }],
  ])('returns the current state unchanged on %s', (_label, payload) => {
    expect(() => applyStorageConnectionEvent(allOk, payload)).not.toThrow();
    expect(applyStorageConnectionEvent(allOk, payload)).toBe(allOk);
  });
});

describe('selectStorageBanner', () => {
  it('shows nothing when both backends are ok', () => {
    expect(selectStorageBanner(allOk)).toBeNull();
  });

  it('produces exactly one view when both backends are degraded', () => {
    const view = selectStorageBanner({
      path: { state: 'unavailable', since: '2026-07-20T10:00:00Z' },
      s3: { state: 'reconnecting', since: '2026-07-20T09:00:00Z' },
    });
    expect(view).not.toBeNull();
    expect(view!.severity).toBe('error');
    // One coherent message that names both halves, not two stacked banners.
    expect(view!.message).toContain('Filesystem Path');
    expect(view!.message).toContain('S3-compatible');
    // The outage started when the FIRST backend dropped.
    expect(view!.since).toBe('2026-07-20T09:00:00Z');
  });

  // Same two instants, but the earlier one is written with an offset instead of
  // Z. A lexicographic comparison picks the wrong one here, so this pins that
  // the stamps are compared as instants.
  it('picks the earliest since across mixed timezone spellings', () => {
    const view = selectStorageBanner({
      path: { state: 'unavailable', since: '2026-07-20T10:00:00Z' },
      s3: { state: 'reconnecting', since: '2026-07-20T11:00:00+02:00' },
    });
    expect(view!.since).toBe('2026-07-20T11:00:00+02:00');
  });

  // The whole point of the two vocabularies: fails-now must not read like
  // waits-for-recovery.
  it('gives unavailable and reconnecting different copy and severity', () => {
    const unavailable = selectStorageBanner({
      path: { state: 'unavailable', since: '2026-07-20T10:00:00Z' },
      s3: { state: 'ok', since: null },
    });
    const reconnecting = selectStorageBanner({
      path: { state: 'ok', since: null },
      s3: { state: 'reconnecting', since: '2026-07-20T10:00:00Z' },
    });

    expect(unavailable!.title).not.toBe(reconnecting!.title);
    expect(unavailable!.message).not.toBe(reconnecting!.message);
    expect(unavailable!.severity).toBe('error');
    expect(reconnecting!.severity).toBe('warning');
    // Fails-now must not claim uploads wait, and vice versa.
    expect(unavailable!.message).toContain('fail immediately');
    expect(reconnecting!.message).toContain('wait instead of failing');
    expect(unavailable!.message).not.toContain('wait instead of failing');
  });

  it('carries the since of the degraded backend only', () => {
    const view = selectStorageBanner({
      path: { state: 'ok', since: null },
      s3: { state: 'reconnecting', since: '2026-07-20T10:00:00Z' },
    });
    expect(view!.since).toBe('2026-07-20T10:00:00Z');
  });
});
