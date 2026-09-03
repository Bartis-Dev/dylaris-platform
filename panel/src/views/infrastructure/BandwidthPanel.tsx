"use client";

import { useEffect, useMemo, useState, useCallback } from 'react';
import { AlertTriangle } from 'lucide-react';
import {
  getGatewayBandwidthOverview,
  getGatewayBandwidthHistory,
  getGatewayRebalance,
  setWarpRebalanceMode,
  type RebalanceView,
} from '@/lib/api';
import BandwidthHostRow from './BandwidthRow';
import {
  hostRows,
  seriesKey,
  summarizeAlerts,
  summarizeDecision,
  BANDWIDTH_RANGES,
  GATEWAY_KINDS,
  type BandwidthRange,
  type BandwidthHistory,
  type GatewayBandwidthOverview,
} from '@/lib/bandwidth';

/**
 * Bandwidth: one row per HOST, one column per gateway kind.
 *
 * The layout is the point. Edges, warp leaders and beam relays share machines
 * today and get their own later, and the question this screen answers - is this
 * uplink running out - is a question about the HOST, not about any one service
 * on it. So the cap and the total live in the rail on the left, once per row,
 * and the cells hold only what belongs to a single service.
 *
 * Deliberately bandwidth ONLY. What each component costs in CPU and RAM is the
 * Gateway tab; mixing the two put two unrelated judgements in one card.
 */


// Written out rather than composed, because a class Tailwind cannot read in the
// source is a class it never generates - the utilisation figure would simply
// lose its colour at exactly the percentages that need one.


// timeAgo renders a compact relative age for a unix-seconds timestamp (the
// rebalancer decision feed's `ts`, same unit as BandwidthHistoryPoint.ts).
function timeAgo(ts: number): string {
  const seconds = Math.max(0, Math.floor(Date.now() / 1000 - ts));
  if (seconds < 60) return `${seconds}s ago`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  const days = Math.floor(hours / 24);
  return `${days}d ago`;
}

export default function BandwidthPanel() {
  const [overview, setOverview] = useState<GatewayBandwidthOverview | null>(null);
  const [range, setRange] = useState<BandwidthRange>('1h');
  const [history, setHistory] = useState<BandwidthHistory | null>(null);
  const [rebalance, setRebalance] = useState<RebalanceView | null>(null);
  const [savingMode, setSavingMode] = useState(false);
  const [modeError, setModeError] = useState<string | null>(null);

  const load = useCallback(async () => {
    try {
      setOverview(await getGatewayBandwidthOverview());
    } catch {
      // best-effort; keep the last snapshot
    }
    try {
      setRebalance(await getGatewayRebalance());
    } catch {
      // best-effort; keep the last snapshot
    }
  }, []);

  useEffect(() => {
    load();
    const t = setInterval(load, 10000);
    return () => clearInterval(t);
  }, [load]);

  // History is one request for every series, refreshed on the persist cadence
  // (Core writes a row per component every 30s, so polling faster only re-sends
  // the same points).
  useEffect(() => {
    let cancelled = false;
    const fetchHistory = () => {
      getGatewayBandwidthHistory(range)
        .then(res => { if (!cancelled) setHistory(res); })
        .catch(() => { if (!cancelled) setHistory(null); });
    };
    fetchHistory();
    const t = setInterval(fetchHistory, 30000);
    return () => { cancelled = true; clearInterval(t); };
  }, [range]);

  const handleModeChange = async (next: 'off' | 'dry-run' | 'armed') => {
    if (!rebalance || savingMode) return;
    const prevMode = rebalance.mode;
    setModeError(null);
    setSavingMode(true);
    setRebalance({ ...rebalance, mode: next });
    try {
      // fetchAPI does not throw on a non-2xx response - it resolves the parsed
      // body - so success has to be checked explicitly, same as every other
      // panel writer (GatewayTab.tsx, InfrastructureView.tsx, ...).
      const res = await setWarpRebalanceMode(next);
      if (res?.success !== true) {
        setRebalance((cur) => (cur ? { ...cur, mode: prevMode } : cur));
        setModeError(res?.message || 'Failed to update rebalancer mode.');
        return;
      }
      setRebalance(await getGatewayRebalance());
    } catch {
      setRebalance((cur) => (cur ? { ...cur, mode: prevMode } : cur));
      setModeError('Failed to update rebalancer mode.');
    } finally {
      setSavingMode(false);
    }
  };

  const rows = useMemo(
    () => hostRows(overview?.hosts ?? [], overview?.components ?? []),
    [overview],
  );

  // Every chart looks its history up by the same key its cell carries. Built in
  // one place so the two sides cannot drift into building it differently, which
  // would leave every chart silently empty.
  const compSeries = useMemo(() => {
    const m = new Map<string, { tx: number[]; rx: number[] }>();
    for (const s of history?.components ?? []) {
      m.set(seriesKey(s), { tx: s.points.map(p => p.txBps), rx: s.points.map(p => p.rxBps) });
    }
    return m;
  }, [history]);

  // The fallback scale, used only where no cap is configured. Where a cap IS
  // known each chart is drawn against it, so a height means the same share of
  // the link in every box on the screen.
  const fallbackScale = useMemo(() => {
    let max = 0;
    for (const s of history?.components ?? []) {
      for (const p of s.points) {
        if (p.txBps > max) max = p.txBps;
        if (p.rxBps > max) max = p.rxBps;
      }
    }
    return max;
  }, [history]);

  if (!overview) {
    return <div className="card p-8 text-center text-(--base-06) text-sm">Loading bandwidth telemetry...</div>;
  }

  const bellItems = summarizeAlerts(overview.alerts);

  // Rebalancer mode control + recent decisions. Independent of whether any
  // gateway component is currently reporting telemetry, so it renders in
  // both the "no components" empty state and the normal overview below.
  const rebalancerSection = rebalance && (
    <div className="card p-4 flex flex-col gap-3">
      <div className="flex items-center justify-between">
        <span className="mono-label">Warp rebalancer</span>
        <select
          value={rebalance.mode}
          onChange={(e) => handleModeChange(e.target.value as 'off' | 'dry-run' | 'armed')}
          disabled={savingMode}
          className="input-field text-sm w-32"
        >
          <option value="off">Off</option>
          <option value="dry-run">Dry run</option>
          <option value="armed">Armed</option>
        </select>
      </div>
      {rebalance.mode === 'dry-run' && (
        <p className="text-xs text-(--base-06)">Computing only, no moves applied.</p>
      )}
      {rebalance.mode === 'armed' && (
        <p className="text-xs text-(--base-06)">Moves are applied automatically to relieve saturated warp leaders.</p>
      )}
      {modeError && <p className="text-xs text-(--error-light)">{modeError}</p>}
      <div className="flex flex-col gap-1.5 border-t border-(--base-03) pt-2">
        {rebalance.decisions.length === 0 ? (
          <p className="text-xs text-(--base-06)">No recent rebalancer activity</p>
        ) : (
          rebalance.decisions.map((d, i) => (
            <div key={`${d.ts}-${i}`} className="flex items-center justify-between text-xs gap-3">
              <span className="text-(--base-08)">{summarizeDecision(d)}</span>
              <span className="text-(--base-05) font-mono shrink-0">{timeAgo(d.ts)}</span>
            </div>
          ))
        )}
      </div>
    </div>
  );

  if (rows.length === 0) {
    return (
      <div className="flex flex-col gap-4">
        {rebalancerSection}
        <div className="card p-8 text-center text-(--base-06) text-sm">
          No gateway components reporting. Edges, beam relays and warp leaders publish bandwidth once BANDWIDTH_MBIT is set and traffic flows.
        </div>
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-4">
      {rebalancerSection}

      {bellItems.length > 0 && (
        <div className="border border-(--warning-border) bg-(--warning-ghost) rounded-lg p-3 flex flex-col gap-2">
          {bellItems.map((it) => (
            <div key={it.id} className="flex items-start gap-2">
              <AlertTriangle size={15} className="text-(--warning-light) mt-0.5 shrink-0" />
              <div>
                <div className="text-sm font-medium text-(--base-09)">{it.title}</div>
                <div className="text-xs text-(--base-07)">{it.message}</div>
              </div>
            </div>
          ))}
        </div>
      )}

      <div className="flex items-center justify-between gap-3 flex-wrap">
        <span className="mono-label">Throughput by host</span>
        <RangePicker range={range} onChange={setRange} />
      </div>

      {history?.note && (
        <div className="rounded-lg border border-(--base-03) bg-(--base-02) px-4 py-2.5 text-xs text-(--base-06)">
          {history.note}
        </div>
      )}

      {/* One row per host, one column per kind. The charts line up down the
          screen, which is what makes two hosts comparable at a glance. */}
      <div className="flex flex-col gap-3">
        {rows.map(({ host, cells }) => (
          <BandwidthHostRow
            key={host.host}
            host={host}
            kinds={GATEWAY_KINDS}
            cells={cells}
            seriesFor={c => compSeries.get(seriesKey(c)) ?? { tx: [], rx: [] }}
            scaleBps={fallbackScale}
            range={range}
          />
        ))}
      </div>

    </div>
  );
}

function RangePicker({ range, onChange }: { range: BandwidthRange; onChange: (r: BandwidthRange) => void }) {
  return (
    <div className="flex gap-1" role="group" aria-label="Time range">
      {BANDWIDTH_RANGES.map(r => (
        <button
          key={r}
          onClick={() => onChange(r)}
          aria-pressed={range === r}
          className={`px-2.5 py-1 rounded-sm text-xs font-medium transition-colors ${
            range === r ? 'bg-(--base-04) text-(--base-09)' : 'text-(--base-07) hover:text-(--base-09)'
          }`}
        >
          {r}
        </button>
      ))}
    </div>
  );
}

