import { describe, it, expect } from 'vitest';
import { beamConnectionModeMeta, type BeamConnectionMode } from '@dylaris/ui-filebrowser/src/connectionMode';

describe('beamConnectionModeMeta', () => {
  const cases: { mode: BeamConnectionMode; label: string; descMatch: string }[] = [
    { mode: 'lan-fastpath', label: 'LAN fast-path', descMatch: 'local network' },
    { mode: 'relay', label: 'Relay', descMatch: 'node IP stays hidden' },
    { mode: 'direct', label: 'Direct', descMatch: 'public address' },
  ];

  for (const c of cases) {
    it(`${c.mode} -> label "${c.label}" with a descriptive tooltip`, () => {
      const meta = beamConnectionModeMeta(c.mode);
      expect(meta.label).toBe(c.label);
      expect(meta.description).toContain(c.descMatch);
      expect(meta.description.length).toBeGreaterThan(0);
    });
  }

  it('unknown mode falls back to a safe generic label', () => {
    const meta = beamConnectionModeMeta('bogus' as BeamConnectionMode);
    expect(meta.label).toBe('Connected');
    expect(meta.description.length).toBeGreaterThan(0);
  });
});
