// Types + pure helpers for the gateway bandwidth dashboard. F0 stores throughput
// in bits/second (not bytes), so this module has its own bits/s formatter rather
// than reusing the bytes/s formatSpeed in InfrastructureView.

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

// groupComponentsByHost buckets components under their host for the nested display.
export function groupComponentsByHost(
  components: GatewayComponentView[],
): Map<string, GatewayComponentView[]> {
  const m = new Map<string, GatewayComponentView[]>();
  for (const c of components) {
    const arr = m.get(c.host) ?? [];
    arr.push(c);
    m.set(c.host, arr);
  }
  return m;
}

export interface BandwidthBellItem {
  id: string;
  title: string;
  message: string;
}

// summarizeAlerts turns overview alerts into display rows for the dashboard banner.
export function summarizeAlerts(alerts: GatewayAlert[]): BandwidthBellItem[] {
  return alerts.map((a) =>
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
