import { describe, it, expect } from 'vitest';
import {
  formatBitsPerSec,
  utilTone,
  barWidthPct,
  groupComponentsByHost,
  summarizeAlerts,
  type GatewayComponentView,
  type GatewayAlert,
} from './bandwidth';

describe('formatBitsPerSec', () => {
  it('renders zero', () => {
    expect(formatBitsPerSec(0)).toBe('0 bps');
  });
  it('scales to Kbps/Mbps/Gbps (base 1000)', () => {
    expect(formatBitsPerSec(1500)).toBe('1.5 Kbps');
    expect(formatBitsPerSec(2_500_000)).toBe('2.5 Mbps');
    expect(formatBitsPerSec(2_500_000_000)).toBe('2.5 Gbps');
  });
});

describe('utilTone', () => {
  it('maps below warn to ok', () => expect(utilTone(79)).toBe('ok'));
  it('maps warn band', () => expect(utilTone(85)).toBe('warn'));
  it('maps crit band', () => expect(utilTone(95)).toBe('crit'));
});

describe('barWidthPct', () => {
  it('clamps above 100', () => expect(barWidthPct(150)).toBe(100));
  it('floors below 0', () => expect(barWidthPct(-5)).toBe(0));
  it('passes through mid-range', () => expect(barWidthPct(42)).toBe(42));
});

describe('groupComponentsByHost', () => {
  it('groups components under their host', () => {
    const comps = [
      { host: 'h1', component: 'warp', id: 'a' },
      { host: 'h1', component: 'edge', id: 'b' },
      { host: 'h2', component: 'beam', id: 'c' },
    ] as GatewayComponentView[];
    const g = groupComponentsByHost(comps);
    expect(g.get('h1')?.length).toBe(2);
    expect(g.get('h2')?.length).toBe(1);
  });
});

describe('summarizeAlerts', () => {
  it('builds bell items for host and component alerts', () => {
    const alerts: GatewayAlert[] = [
      { kind: 'host', host: 'h1', utilPct: 91.4, threshold: 80 },
      { kind: 'component', host: 'h1', component: 'warp', id: 'eu-1', utilPct: 88, threshold: 80 },
    ];
    const items = summarizeAlerts(alerts);
    expect(items).toHaveLength(2);
    expect(items[0].id).toBe('gwbw-host-h1');
    expect(items[0].title).toContain('91%');
    expect(items[1].id).toBe('gwbw-comp-warp-eu-1');
  });
});
