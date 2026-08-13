"use client";

import { useEffect, useState, useCallback } from 'react';
import { AreaChart, Area, XAxis, YAxis, Tooltip, ResponsiveContainer, CartesianGrid } from 'recharts';
import { AlertTriangle } from 'lucide-react';
import {
  getGatewayBandwidthOverview,
  getGatewayBandwidthHistory,
  getGatewayRebalance,
  setWarpRebalanceMode,
  type RebalanceView,
} from '@/lib/api';
import {
  formatBitsPerSec,
  utilTone,
  barWidthPct,
  groupComponentsByHost,
  summarizeAlerts,
  summarizeDecision,
  type GatewayBandwidthOverview,
  type BandwidthHistoryPoint,
  type UtilTone,
} from '@/lib/bandwidth';

const tooltipStyle = {
  backgroundColor: 'var(--base-02)',
  border: '1px solid var(--base-04)',
  borderRadius: 'var(--radius-md)',
  fontSize: '12px',
  color: 'var(--base-09)',
  boxShadow: 'var(--shadow-md)',
  padding: '8px 12px',
};

const toneBar: Record<UtilTone, string> = {
  ok: 'bg-(--success-light)',
  warn: 'bg-(--warning-light)',
  crit: 'bg-(--error)',
};

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
  const [selectedHost, setSelectedHost] = useState<string | null>(null);
  const [range, setRange] = useState('1h');
  const [history, setHistory] = useState<BandwidthHistoryPoint[]>([]);
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

  useEffect(() => {
    if (!selectedHost) {
      setHistory([]);
      return;
    }
    let cancelled = false;
    getGatewayBandwidthHistory(range, selectedHost)
      .then((res) => {
        if (!cancelled) setHistory(res.points ?? []);
      })
      .catch(() => {
        if (!cancelled) setHistory([]);
      });
    return () => {
      cancelled = true;
    };
  }, [selectedHost, range]);

  if (!overview) {
    return <div className="card p-8 text-center text-(--base-06) text-sm">Loading bandwidth telemetry...</div>;
  }

  const hosts = overview.hosts;
  const byHost = groupComponentsByHost(overview.components);
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

  if (hosts.length === 0 && overview.components.length === 0) {
    return (
      <div className="flex flex-col gap-4">
        {rebalancerSection}
        <div className="card p-8 text-center text-(--base-06) text-sm">
          No gateway components reporting. Edges, beam and warp leaders publish bandwidth once BANDWIDTH_MBIT is set and traffic flows.
        </div>
      </div>
    );
  }

  const timeFmt = (ts: number) => {
    const d = new Date(ts * 1000);
    return `${d.getHours().toString().padStart(2, '0')}:${d.getMinutes().toString().padStart(2, '0')}`;
  };

  return (
    <div className="flex flex-col gap-4">
      {rebalancerSection}

      {/* Alert banner */}
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

      {/* Host cards */}
      <div className="grid grid-cols-1 lg:grid-cols-2 gap-3">
        {hosts.map((h) => {
          const comps = byHost.get(h.host) ?? [];
          const active = selectedHost === h.host;
          return (
            <div key={h.host} className={`card p-4 flex flex-col gap-3 ${active ? 'ring-1 ring-(--accent)' : ''}`}>
              <div className="flex items-center justify-between">
                <div className="flex items-center gap-2">
                  <span className="font-medium text-(--base-09)">{h.host}</span>
                  {h.capMismatch && (
                    <span className="text-[10px] px-1.5 py-0.5 rounded-full bg-(--warning-ghost) text-(--warning-light) border border-(--warning-border)">cap mismatch</span>
                  )}
                </div>
                <span className="text-xs font-mono text-(--base-06)">{h.components} comp</span>
              </div>
              <div className="flex items-center justify-between text-xs font-mono text-(--base-07)">
                <span>tx {formatBitsPerSec(h.txBps)}</span>
                <span>rx {formatBitsPerSec(h.rxBps)}</span>
                <span>{h.capKnown ? `${(h.budgetMbit / 1000).toFixed(1)} Gbps cap` : 'no cap'}</span>
              </div>
              <UtilBar pct={h.utilPct} known={h.capKnown} />
              <div className="flex flex-col gap-1 border-t border-(--base-03) pt-2">
                {comps.map((c) => (
                  <div key={`${c.component}:${c.id}`} className="flex items-center justify-between text-xs">
                    <div className="flex items-center gap-2">
                      <span className={`w-1.5 h-1.5 rounded-full ${c.alive ? 'bg-(--success-light)' : 'bg-(--error)'}`} />
                      <span className="text-(--base-08)">{c.component}</span>
                      <span className="text-(--base-06) font-mono">{c.id}</span>
                      {c.region && <span className="text-(--base-05) font-mono">{c.region}</span>}
                    </div>
                    <div className="flex items-center gap-3 font-mono text-(--base-07)">
                      <span>tx {formatBitsPerSec(c.txBps)}</span>
                      <span className="w-10 text-right">{c.capKnown ? `${c.utilPct.toFixed(0)}%` : '-'}</span>
                    </div>
                  </div>
                ))}
              </div>
              <button onClick={() => setSelectedHost(active ? null : h.host)} className="btn btn-secondary btn-sm self-start">
                {active ? 'Hide history' : 'Show history'}
              </button>
            </div>
          );
        })}
      </div>

      {/* History chart */}
      {selectedHost && (
        <div className="card p-4 flex flex-col gap-3">
          <div className="flex items-center justify-between">
            <span className="mono-label">{selectedHost} throughput</span>
            <div className="flex gap-1">
              {['1h', '6h', '12h', '24h'].map((r) => (
                <button
                  key={r}
                  onClick={() => setRange(r)}
                  className={`px-2.5 py-1 rounded-sm text-xs font-medium transition-colors ${range === r ? 'bg-(--base-04) text-(--base-09)' : 'text-(--base-07) hover:text-(--base-09)'}`}
                >
                  {r}
                </button>
              ))}
            </div>
          </div>
          <div className="h-56">
            {history.length > 1 ? (
              <ResponsiveContainer width="100%" height="100%">
                <AreaChart data={history} margin={{ top: 8, right: 8, bottom: 0, left: 4 }}>
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
              <div className="h-full flex items-center justify-center text-(--base-06) text-sm font-mono">Waiting for data...</div>
            )}
          </div>
        </div>
      )}
    </div>
  );
}
