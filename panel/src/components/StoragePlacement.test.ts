import { describe, it, expect } from 'vitest';
import { effectiveOrder } from './StoragePlacement';

// This must resolve the order exactly the way the node's orderedPaths() does.
// If the two drift apart the admin edits a list that does not match where
// servers actually land, which is worse than having no UI at all.
describe('effectiveOrder', () => {
  it('keeps the node order when nothing is configured', () => {
    expect(effectiveOrder(['/a', '/b', '/c'], [])).toEqual(['/a', '/b', '/c']);
  });

  it('honours a full configured order', () => {
    expect(effectiveOrder(['/a', '/b', '/c'], ['/c', '/a', '/b'])).toEqual(['/c', '/a', '/b']);
  });

  it('puts unlisted paths last, so a disk added later needs no edit', () => {
    expect(effectiveOrder(['/a', '/b', '/c'], ['/c'])).toEqual(['/c', '/a', '/b']);
  });

  it('drops entries the node no longer reports', () => {
    expect(effectiveOrder(['/a', '/b'], ['/gone', '/b', '/also-gone'])).toEqual(['/b', '/a']);
  });

  it('collapses duplicates', () => {
    expect(effectiveOrder(['/a', '/b', '/c'], ['/b', '/b', '/a'])).toEqual(['/b', '/a', '/c']);
  });

  it('returns every path exactly once', () => {
    const paths = ['/a', '/b', '/c', '/d'];
    const got = effectiveOrder(paths, ['/d', '/gone', '/b', '/b']);
    expect([...got].sort()).toEqual([...paths].sort());
  });

  it('handles an empty node with a stale order', () => {
    expect(effectiveOrder([], ['/a'])).toEqual([]);
  });
});
