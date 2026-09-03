import { describe, it, expect } from 'vitest';
import {
  formatBitsPerSec,
  utilTone,
  barWidthPct,
  hostRows,
  seriesKey,
  BANDWIDTH_RANGES,
  summarizeAlerts,
  summarizeDecision,
  type GatewayComponentView,
  type GatewayHostView,
  type GatewayAlert,
} from './bandwidth';
import type { WarpDecision } from './api/types';

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

describe('hostRows', () => {
  const comps = [
    { host: 'h1', component: 'warp', id: 'w1' },
    { host: 'h1', component: 'edge', id: 'e1' },
    { host: 'h2', component: 'beam', id: 'b1' },
  ] as GatewayComponentView[];
  const hosts = [
    { host: 'h2', rxBps: 0, txBps: 0, budgetMbit: 1000, utilPct: 0, capKnown: true, capMismatch: false, components: 1 },
    { host: 'h1', rxBps: 0, txBps: 0, budgetMbit: 1000, utilPct: 0, capKnown: true, capMismatch: false, components: 2 },
  ] as GatewayHostView[];

  it('puts every kind in its own cell of the host row', () => {
    // The layout claim, made testable: co-located services share a row, and
    // each one lands in the column of its kind. That is what makes two cards
    // in one row mean "these two run on the same machine".
    const rows = hostRows(hosts, comps);
    expect(rows.map(r => r.host.host)).toEqual(['h1', 'h2']);
    expect(rows[0].cells.edge.map(c => c.id)).toEqual(['e1']);
    expect(rows[0].cells.warp.map(c => c.id)).toEqual(['w1']);
    expect(rows[0].cells.beam).toEqual([]);
    expect(rows[1].cells.beam.map(c => c.id)).toEqual(['b1']);
  });

  it('keeps a host that has no aggregate yet', () => {
    // The host aggregate and the component mirror are separate Redis keys with
    // their own expiry. A component whose host aggregate is momentarily missing
    // would otherwise vanish from the screen while it is demonstrably reporting.
    const rows = hostRows([], comps);
    expect(rows.map(r => r.host.host)).toEqual(['h1', 'h2']);
    expect(rows[0].host.capKnown, 'an invented host must not claim a known cap').toBe(false);
  });

  it('keeps a second component of the same kind on one host', () => {
    // Nothing stops a host running two edges, and dropping the second silently
    // is worse than a taller cell.
    const two = [
      { host: 'h1', component: 'edge', id: 'e1' },
      { host: 'h1', component: 'edge', id: 'e2' },
    ] as GatewayComponentView[];
    expect(hostRows([], two)[0].cells.edge.map(c => c.id)).toEqual(['e1', 'e2']);
  });

  it('still shows a component that reports no host', () => {
    // The host aggregate skips these: something with no hostname cannot be
    // co-located with anything. Skipping them HERE too would make a
    // misconfigured component invisible on the screen meant to show it.
    const homeless = [{ host: '', component: 'beam', id: 'b9' }] as GatewayComponentView[];
    const rows = hostRows([], homeless);
    expect(rows).toHaveLength(1);
    expect(rows[0].cells.beam.map(c => c.id)).toEqual(['b9']);
  });

  it('ignores a kind it does not have a column for', () => {
    // The splice and the link publish to the same telemetry stream. Core does
    // not mirror them here, but an unknown kind must not throw either.
    const odd = [{ host: 'h1', component: 'splice', id: 's1' }] as GatewayComponentView[];
    const rows = hostRows([], odd);
    expect(rows).toHaveLength(1);
    expect(rows[0].cells.edge.concat(rows[0].cells.warp, rows[0].cells.beam)).toEqual([]);
  });
});

describe('seriesKey', () => {
  it('is the same for a component view and its history series', () => {
    // The cells look their sparkline up by this key. If the two sides ever
    // built it differently, every sparkline would silently be empty.
    const view = { component: 'warp', id: 'eu-1' } as GatewayComponentView;
    const series = { component: 'warp', id: 'eu-1', points: [] };
    expect(seriesKey(view)).toBe(seriesKey(series));
  });
  it('separates the same id under two kinds', () => {
    expect(seriesKey({ component: 'edge', id: 'a' })).not.toBe(seriesKey({ component: 'warp', id: 'a' }));
  });
});

describe('BANDWIDTH_RANGES', () => {
  it('stops at 24h, which is how long the rows are kept', () => {
    // gateway_bandwidth_stats keeps 24 hours. A longer range would draw a chart
    // that is empty at its left edge and reads like an outage.
    expect(BANDWIDTH_RANGES).toEqual(['15m', '1h', '6h', '24h']);
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

  // Regression: the overview payload can carry alerts:null (Go nil slice). An
  // unguarded .map crashed the whole Bandwidth tab; summarizeAlerts must tolerate it.
  it('returns [] for null/undefined without throwing', () => {
    expect(summarizeAlerts(null)).toEqual([]);
    expect(summarizeAlerts(undefined)).toEqual([]);
  });
});

describe('summarizeDecision', () => {
  it('describes an applied move', () => {
    const d: WarpDecision = { ts: 1, mode: 'armed', applied: true, moves: [{ pubkey: 'p1', from: 'L-a', to: 'L-b', txBps: 100 }] };
    expect(summarizeDecision(d)).toContain('L-a');
    expect(summarizeDecision(d)).toContain('L-b');
    expect(summarizeDecision(d)).toContain('1'); // one move
  });
  it('labels a dry-run as would-move', () => {
    const d: WarpDecision = { ts: 1, mode: 'dry-run', applied: false, moves: [{ pubkey: 'p1', from: 'L-a', to: 'L-b', txBps: 100 }] };
    expect(summarizeDecision(d).toLowerCase()).toContain('would');
  });
  it('handles an empty move list', () => {
    const d: WarpDecision = { ts: 1, mode: 'dry-run', applied: false, moves: [] };
    expect(summarizeDecision(d).toLowerCase()).toContain('no move');
  });
});
