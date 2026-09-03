// Types + pure helpers for the gateway bandwidth dashboard. F0 stores throughput
// in bits/second (not bytes), so this module has its own bits/s formatter rather
// than reusing the bytes/s formatSpeed in InfrastructureView.

import type { WarpDecision } from './api/types';

export interface GatewayComponentView {
  component: string;
  id: string;
  host: string;
  region: string;
  rxBps: number;
  txBps: number;
  capMbit: number;
  utilPct: number;
  capKnown: boolean;
  alive: boolean;
  cpuPct: number;
  ramPct: number;
  uptimeSec?: number;
  /** The component's own gauges: warp reports peers, beam reports transfers. */
  gauges?: Record<string, number>;
}

export interface GatewayHostView {
  host: string;
  rxBps: number;
  txBps: number;
  budgetMbit: number;
  utilPct: number;
  capKnown: boolean;
  capMismatch: boolean;
  components: number;
}

export interface GatewayAlert {
  kind: 'host' | 'component';
  host: string;
  component?: string;
  id?: string;
  utilPct: number;
  threshold: number;
}

export interface GatewayBandwidthOverview {
  components: GatewayComponentView[];
  hosts: GatewayHostView[];
  alerts: GatewayAlert[];
}

export interface BandwidthHistoryPoint {
  ts: number;
  rxBps: number;
  txBps: number;
  capMbit: number;
}

/** One subject's history: a component (component+id) or a whole host. */
export interface BandwidthSeries {
  component?: string;
  id?: string;
  host?: string;
  region?: string;
  points: BandwidthHistoryPoint[];
}

/**
 * Every series the screen draws, in one response.
 *
 * The host series are computed by Core, not by adding the component series here:
 * two components on one host peak in different seconds, so a client-side sum of
 * bucket maxima would report a load the link never carried.
 */
export interface BandwidthHistory {
  stepSec: number;
  components: BandwidthSeries[];
  hosts: BandwidthSeries[];
}

/** The ranges the switcher offers. 24h is the ceiling: the raw rows are kept
 *  for 24 hours and nothing older exists to draw. */
export const BANDWIDTH_RANGES = ['15m', '1h', '6h', '24h'] as const;
export type BandwidthRange = (typeof BANDWIDTH_RANGES)[number];

/** seriesKey identifies one component series across renders and selections. */
export function seriesKey(s: { component?: string; id?: string }): string {
  return `${s.component ?? ''}:${s.id ?? ''}`;
}

/**
 * The three kinds that carry throughput, in the order the columns stand.
 * Core only mirrors these three (see carriesThroughput): the splice shares an
 * edge's namespace and the link ships without a system monitor, so neither has
 * throughput of its own to show.
 */
export const GATEWAY_KINDS = [
  { key: 'edge', label: 'Edge' },
  { key: 'warp', label: 'Warp' },
  { key: 'beam', label: 'Beam relay' },
] as const;
export type GatewayKind = (typeof GATEWAY_KINDS)[number]['key'];

/**
 * hostRows lays the components out as one row per host and one cell per kind.
 *
 * Hosts come from the host aggregates, so a host keeps its row (and its cap)
 * even in a tick where none of its components reported. A cell holds a LIST
 * rather than one component: nothing stops a host running two edges, and
 * dropping the second silently would be worse than a slightly taller cell.
 */
export function hostRows(
  hosts: GatewayHostView[],
  components: GatewayComponentView[],
): { host: GatewayHostView; cells: Record<GatewayKind, GatewayComponentView[]> }[] {
  const known = new Map(hosts.map(h => [h.host, h]));
  // A component whose host has no aggregate still has to appear somewhere, or
  // it vanishes from the screen while it is demonstrably reporting. That
  // includes a component reporting NO host at all - the host aggregate skips
  // those, since something with no hostname cannot be co-located with anything,
  // and dropping them here too would make a misconfigured component invisible
  // on the one screen meant to show it.
  for (const c of components) {
    if (!known.has(c.host)) {
      known.set(c.host, {
        host: c.host, rxBps: 0, txBps: 0, budgetMbit: 0,
        utilPct: 0, capKnown: false, capMismatch: false, components: 0,
      });
    }
  }
  return [...known.values()]
    .sort((a, b) => a.host.localeCompare(b.host))
    .map(host => {
      const cells = { edge: [], warp: [], beam: [] } as Record<GatewayKind, GatewayComponentView[]>;
      for (const c of components) {
        if (c.host !== host.host) continue;
        const bucket = cells[c.component as GatewayKind];
        if (bucket) bucket.push(c);
      }
      return { host, cells };
    });
}

// formatBitsPerSec renders a bits/second rate with base-1000 SI scaling.
export function formatBitsPerSec(bps: number): string {
  if (!isFinite(bps) || bps <= 0) return '0 bps';
  const units = ['bps', 'Kbps', 'Mbps', 'Gbps', 'Tbps'];
  const i = Math.min(units.length - 1, Math.floor(Math.log(bps) / Math.log(1000)));
  return `${(bps / Math.pow(1000, i)).toFixed(1)} ${units[i]}`;
}

export type UtilTone = 'ok' | 'warn' | 'crit';

// utilTone maps a utilisation percentage to a badge tone. warn defaults to the
// 80% alert threshold; crit to 92% (the point past which headroom is nearly gone).
export function utilTone(pct: number, warn = 80, crit = 92): UtilTone {
  if (pct >= crit) return 'crit';
  if (pct >= warn) return 'warn';
  return 'ok';
}

// barWidthPct clamps a percentage to the 0..100 range for a progress bar width.
export function barWidthPct(pct: number): number {
  if (!isFinite(pct) || pct < 0) return 0;
  return Math.min(100, pct);
}

export interface BandwidthBellItem {
  id: string;
  title: string;
  message: string;
}

// summarizeAlerts turns overview alerts into display rows for the dashboard banner.
// Guards null: the overview payload can carry alerts:null (a Go nil slice), and an
// unguarded .map here crashed the whole Bandwidth tab. Mirrors the bell's ov?.alerts.
export function summarizeAlerts(alerts: GatewayAlert[] | null | undefined): BandwidthBellItem[] {
  return (alerts ?? []).map((a) =>
    a.kind === 'host'
      ? {
          id: `gwbw-host-${a.host}`,
          title: `Host ${a.host} at ${a.utilPct.toFixed(0)}% bandwidth`,
          message: `Sustained above ${a.threshold}% of the host budget across co-located gateway components.`,
        }
      : {
          id: `gwbw-comp-${a.component}-${a.id}`,
          title: `${a.component} ${a.id} at ${a.utilPct.toFixed(0)}% bandwidth`,
          message: `Sustained above ${a.threshold}% of its configured cap on ${a.host}.`,
        },
  );
}

// summarizeDecision renders one rebalancer decision as a compact line for the
// panel feed. "would move" for dry-run, "moved" for armed.
export function summarizeDecision(d: WarpDecision): string {
  if (!d.moves || d.moves.length === 0) {
    return d.applied ? 'No moves applied' : 'No moves (dry-run)';
  }
  const verb = d.applied ? 'moved' : 'would move';
  const first = d.moves[0];
  const extra = d.moves.length > 1 ? ` (+${d.moves.length - 1} more)` : '';
  return `${verb} ${d.moves.length} peer(s): ${first.from} -> ${first.to}${extra}`;
}
