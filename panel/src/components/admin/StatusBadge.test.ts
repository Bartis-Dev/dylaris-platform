import { describe, it, expect } from 'vitest';
import { STATUS_CLASSES } from './StatusBadge';

// The badge falls back to a neutral grey for anything it does not know, which
// is the right default but also hides a gap: disk_full rendered exactly like
// stopped on the admin server list, the one screen an operator scans for
// trouble. This list is the set of statuses the backend can actually report
// (core UpdateServerStatus + the node's status writes), so a new one added
// server-side fails here instead of silently rendering as "nothing wrong".
const REPORTABLE = [
  'online',
  'offline',
  'stopped',
  'stopping',
  'starting',
  'restarting',
  'installing',
  'pending_setup',
  'migrating',
  'disk_full',
  'suspended',
];

describe('StatusBadge STATUS_CLASSES', () => {
  it('has an entry for every status the backend can report', () => {
    const missing = REPORTABLE.filter((s) => !(s in STATUS_CLASSES));
    expect(missing).toEqual([]);
  });

  it('renders the two hard-failure states as errors, not as neutral', () => {
    for (const s of ['disk_full', 'suspended']) {
      expect(STATUS_CLASSES[s]).toContain('--error');
    }
  });

  it('renders the transient states as warnings', () => {
    for (const s of ['starting', 'stopping', 'restarting', 'installing', 'migrating']) {
      expect(STATUS_CLASSES[s]).toContain('--warning');
    }
  });

  it('does not mark a healthy or deliberately-stopped server as a problem', () => {
    for (const s of ['online', 'stopped', 'offline', 'pending_setup']) {
      expect(STATUS_CLASSES[s]).not.toContain('--error');
      expect(STATUS_CLASSES[s]).not.toContain('--warning');
    }
  });
});
