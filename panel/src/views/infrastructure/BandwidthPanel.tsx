"use client";

import { useEffect, useMemo, useState, useCallback } from 'react';
import { AreaChart, Area, XAxis, YAxis, Tooltip, ResponsiveContainer, CartesianGrid } from 'recharts';
import { AlertTriangle } from 'lucide-react';
import {
  getGatewayBandwidthOverview,
  getGatewayBandwidthHistory,
  getGatewayRebalance,
  setWarpRebalanceMode,
  type RebalanceView,
} from '@/lib/api';
import Sparkline from '@/components/infra/Sparkline';
import {
  formatBitsPerSec,
  utilTone,
  barWidthPct,
  hostRows,
  seriesKey,
  summarizeAlerts,
  summarizeDecision,
  BANDWIDTH_RANGES,
  GATEWAY_KINDS,
  type BandwidthRange,
  type BandwidthHistory,
  type BandwidthSeries,
  type GatewayBandwidthOverview,
  type GatewayComponentView,
  type GatewayHostView,
  type UtilTone,
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

const tooltipStyle = {
  backgroundColor: 'var(--base-02)',
  border: '1px solid var(--base-04)',
  borderRadius: 'var(--radius-md)',
  fontSize: '12px',
  color: 'var(--base-09)',
  boxShadow: 'var(--shadow-md)',
  padding: '8px 12px',
};

// Written out rather than composed, because a class Tailwind cannot read in the
// source is a class it never generates - the utilisation figure would simply
// lose its colour at exactly the percentages that need one.
const toneText: Record<UtilTone, string> = {
  ok: 'text-(--base-07)',
  warn: 'text-(--warning-light)',
  crit: 'text-(--error)',
};

const toneBar: Record<UtilTone, string> = {
  ok: 'bg-(--success-light)',
  warn: 'bg-(--warning-light)',
  crit: 'bg-(--error)',
};

/** What the big chart is currently showing. */
type Selection = { kind: 'host'; key: string } | { kind: 'component'; key: string };

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

function UtilBar({ pct, known }: { pct: number; known: boolean }) {
  if (!known) {
    return <div className="text-[11px] text-(--base-06) font-mono">no cap</div>;
  }
  const tone = utilTone(pct);
  return (
    <div className="flex items-center gap-2">
      <div className="flex-1 h-1.5 rounded-full bg-(--base-03) overflow-hidden">
        <div className={`h-full rounded-full transition-all duration-500 ${toneBar[tone]}`} style={{ width: `${barWidthPct(pct)}%` }} />
      </div>
      <span className="text-[11px] font-mono tabular-nums text-(--base-07) w-12 text-right">{pct.toFixed(0)}%</span>
    </div>
  );
}

export default function BandwidthPanel() {
  const [overview, setOverview] = useState<GatewayBandwidthOverview | null>(null);
  const [range, setRange] = useState<BandwidthRange>('1h');
  const [history, setHistory] = useState<BandwidthHistory | null>(null);
  const [selection, setSelection] = useState<Selection | null>(null);
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

  // Sparklines are indexed by the same key the cells carry, so a component that
  // has not reported for the whole window simply finds nothing and says so.
  const compSeries = useMemo(() => {
    const m = new Map<string, BandwidthSeries>();
    for (const s of history?.components ?? []) m.set(seriesKey(s), s);
    return m;
  }, [history]);
  const hostSeries = useMemo(() => {
    const m = new Map<string, BandwidthSeries>();
    for (const s of history?.hosts ?? []) m.set(s.host ?? '', s);
    return m;
  }, [history]);

  // One scale across the whole grid. Per-card scaling would draw a beam relay
  // carrying a tenth of the edge's traffic as an identical line.
  const sparkMax = useMemo(() => {
    let max = 0;
    for (const s of history?.components ?? []) {
      for (const p of s.points) if (p.txBps > max) max = p.txBps;
    }
    return max;
  }, [history]);

  // Default the chart to the busiest host rather than to nothing: an empty
  // chart frame reads as a failure, and the busiest host is the one somebody
  // opening this screen came to look at.
  useEffect(() => {
    if (selection || rows.length === 0) return;
    const busiest = [...rows].sort((a, b) => b.host.txBps - a.host.txBps)[0];
    setSelection({ kind: 'host', key: busiest.host.host });
  }, [rows, selection]);

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

  const selected: BandwidthSeries | null = selection
    ? (selection.kind === 'host' ? hostSeries.get(selection.key) : compSeries.get(selection.key)) ?? null
    : null;
  const selectedLabel = selection
    ? (selection.kind === 'host' ? selection.key : selection.key.replace(':', ' '))
    : '';

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

      {/* One row per host, one column per kind. Every row's cells share a grid
          row, so co-located services line up at the same height by construction
          rather than by matching paddings. Below lg the grid is one column and
          each host's rail is followed by its own services. */}
      <div className="grid gap-3 grid-cols-1 lg:grid-cols-[minmax(180px,240px)_repeat(3,minmax(0,1fr))]">
        <div className="hidden lg:block" aria-hidden />
        {GATEWAY_KINDS.map(k => (
          <div key={k.key} className="hidden lg:block mono-label text-(--base-06) px-1">{k.label}</div>
        ))}

        {rows.map(({ host, cells }) => (
          <div key={host.host} className="contents">
            <HostRail
              host={host}
              active={selection?.kind === 'host' && selection.key === host.host}
              onSelect={() => setSelection({ kind: 'host', key: host.host })}
            />
            {GATEWAY_KINDS.map(k => (
              <div key={k.key} className="flex flex-col gap-2">
                {/* The column header again, for the single-column layout where
                    the header row above is not drawn. */}
                <span className="lg:hidden mono-label text-(--base-06)">{k.label}</span>
                {cells[k.key].length === 0 ? (
                  <div className="flex-1 min-h-16 rounded-lg border border-dashed border-(--base-03) flex items-center justify-center text-[11px] font-mono text-(--base-05)">
                    none on this host
                  </div>
                ) : (
                  cells[k.key].map(c => (
                    <ComponentCell
                      key={seriesKey(c)}
                      comp={c}
                      series={compSeries.get(seriesKey(c))}
                      sparkMax={sparkMax}
                      active={selection?.kind === 'component' && selection.key === seriesKey(c)}
                      onSelect={() => setSelection({ kind: 'component', key: seriesKey(c) })}
                    />
                  ))
                )}
              </div>
            ))}
          </div>
        ))}
      </div>

      <div className="card p-4 flex flex-col gap-3">
        <div className="flex items-center justify-between gap-3 flex-wrap">
          <span className="mono-label">{selectedLabel} throughput</span>
          <RangePicker range={range} onChange={setRange} />
        </div>
        <div className="h-56">
          {selected && selected.points.length > 1 ? (
            <ResponsiveContainer width="100%" height="100%">
              <AreaChart data={selected.points} margin={{ top: 8, right: 8, bottom: 0, left: 4 }}>
                <defs>
                  <linearGradient id="bwTxFill" x1="0" y1="0" x2="0" y2="1">
                    <stop offset="0%" stopColor="var(--accent)" stopOpacity={0.2} />
                    <stop offset="100%" stopColor="var(--accent)" stopOpacity={0} />
                  </linearGradient>
                  <linearGradient id="bwRxFill" x1="0" y1="0" x2="0" y2="1">
                    <stop offset="0%" stopColor="var(--primary)" stopOpacity={0.15} />
                    <stop offset="100%" stopColor="var(--primary)" stopOpacity={0} />
                  </linearGradient>
                </defs>
                <CartesianGrid stroke="var(--base-03)" strokeDasharray="3 3" vertical={false} />
                <XAxis dataKey="ts" tickFormatter={timeFmt} tick={{ fontSize: 10, fill: 'var(--base-05)' }} axisLine={false} tickLine={false} />
                <YAxis tickFormatter={(v: number) => formatBitsPerSec(v)} tick={{ fontSize: 10, fill: 'var(--base-05)' }} axisLine={false} tickLine={false} width={70} />
                <Tooltip
                  contentStyle={tooltipStyle}
                  labelFormatter={(v) => timeFmt(v as number)}
                  formatter={(v, name) => [formatBitsPerSec(Number(v)), name === 'txBps' ? 'tx' : 'rx']}
                  cursor={{ stroke: 'var(--base-05)', strokeWidth: 1, strokeDasharray: '4 4' }}
                />
                <Area type="monotone" dataKey="txBps" stroke="var(--accent)" fill="url(#bwTxFill)" strokeWidth={2} dot={false} isAnimationActive={false} />
                <Area type="monotone" dataKey="rxBps" stroke="var(--primary)" fill="url(#bwRxFill)" strokeWidth={2} dot={false} isAnimationActive={false} />
              </AreaChart>
            </ResponsiveContainer>
          ) : (
            <div className="h-full flex items-center justify-center text-(--base-06) text-sm font-mono">
              Nothing recorded in this window
            </div>
          )}
        </div>
        {/* Said plainly, because it changes how the chart should be read: a
            bucket shows the highest reading it contains, so a spike stays
            visible instead of being averaged into comfort. */}
        {history && history.stepSec > 0 && (
          <p className="text-[11px] text-(--base-05)">
            Peak per {history.stepSec >= 60 ? `${history.stepSec / 60} min` : `${history.stepSec}s`} bucket. History is kept for 24 hours.
          </p>
        )}
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

function HostRail({ host, active, onSelect }: { host: GatewayHostView; active: boolean; onSelect: () => void }) {
  return (
    <button
      onClick={onSelect}
      aria-pressed={active}
      className={`card p-3 flex flex-col gap-2 text-left h-full transition-all hover:border-(--base-04) ${active ? 'ring-1 ring-(--accent)' : ''}`}
    >
      <div className="flex items-center gap-2 flex-wrap">
        <span className={`font-medium truncate ${host.host ? 'text-(--base-09)' : 'text-(--warning-light)'}`}>
          {host.host || 'no host reported'}
        </span>
        {host.capMismatch && (
          <span className="text-[10px] px-1.5 py-0.5 rounded-full bg-(--warning-ghost) text-(--warning-light) border border-(--warning-border)">cap mismatch</span>
        )}
      </div>
      <div className="text-xs font-mono text-(--base-07) flex flex-col gap-0.5">
        <span>tx {formatBitsPerSec(host.txBps)}</span>
        <span>rx {formatBitsPerSec(host.rxBps)}</span>
        <span className="text-(--base-05)">
          {host.capKnown ? `${(host.budgetMbit / 1000).toFixed(1)} Gbps cap` : 'no cap set'}
        </span>
      </div>
      <UtilBar pct={host.utilPct} known={host.capKnown} />
    </button>
  );
}

function ComponentCell({
  comp, series, sparkMax, active, onSelect,
}: {
  comp: GatewayComponentView;
  series?: BandwidthSeries;
  sparkMax: number;
  active: boolean;
  onSelect: () => void;
}) {
  return (
    <button
      onClick={onSelect}
      aria-pressed={active}
      className={`card p-3 flex flex-col gap-2 text-left flex-1 transition-all hover:border-(--base-04) ${active ? 'ring-1 ring-(--accent)' : ''}`}
    >
      <div className="flex items-center justify-between gap-2">
        <span className="text-xs font-mono text-(--base-08) truncate">{comp.id}</span>
        {comp.region && <span className="text-[10px] font-mono text-(--base-05) shrink-0">{comp.region}</span>}
      </div>
      <div className="flex items-baseline gap-2 font-mono tabular-nums">
        <span className="text-sm text-(--base-09)">{formatBitsPerSec(comp.txBps)}</span>
        <span className="text-[11px] text-(--base-06)">tx</span>
        <span className="text-[11px] text-(--base-06) ml-auto">rx {formatBitsPerSec(comp.rxBps)}</span>
      </div>
      <Sparkline
        values={(series?.points ?? []).map(p => p.txBps)}
        max={sparkMax}
        title={`${comp.component} ${comp.id} transmit history`}
      />
      <div className="flex items-center justify-between text-[11px] font-mono text-(--base-06)">
        <span>{comp.capKnown ? `${comp.capMbit} Mbit cap` : 'no cap'}</span>
        <span className={comp.capKnown ? toneText[utilTone(comp.utilPct)] : ''}>
          {comp.capKnown ? `${comp.utilPct.toFixed(0)}%` : '-'}
        </span>
      </div>
    </button>
  );
}

const timeFmt = (ts: number) => {
  const d = new Date(ts * 1000);
  return `${d.getHours().toString().padStart(2, '0')}:${d.getMinutes().toString().padStart(2, '0')}`;
};
