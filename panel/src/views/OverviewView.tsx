"use client";

import { useState, useEffect, useRef } from 'react';
import { AreaChart, Area, XAxis, YAxis, Tooltip, ResponsiveContainer, CartesianGrid } from 'recharts';
import { Server, ServerStats, DiskUsage, getStatsHistory, getDiskUsage } from '@/lib/api';
import { API_URL } from '@/lib/api/core';

import { Cpu, MemoryStick, AlertTriangle, HardDrive, Download, MonitorDown } from 'lucide-react';

function formatBytes(bytes: number): string {
  if (bytes >= 1073741824) return `${(bytes / 1073741824).toFixed(1)} GB`;
  if (bytes >= 1048576) return `${(bytes / 1048576).toFixed(0)} MB`;
  if (bytes >= 1024) return `${(bytes / 1024).toFixed(0)} KB`;
  return `${bytes} B`;
}

function formatTime(ts: number): string {
  const d = new Date(ts * 1000);
  return `${d.getHours().toString().padStart(2, '0')}:${d.getMinutes().toString().padStart(2, '0')}:${d.getSeconds().toString().padStart(2, '0')}`;
}

function formatTimeShort(ts: number): string {
  const d = new Date(ts * 1000);
  return `${d.getHours().toString().padStart(2, '0')}:${d.getMinutes().toString().padStart(2, '0')}`;
}

interface ChartCardProps {
  icon: React.ReactNode;
  label: string;
  currentValue: string;
  accentColor: string;
  children: React.ReactNode;
}

function ChartCard({ icon, label, currentValue, accentColor, children }: ChartCardProps) {
  return (
    <div className="rounded-xl bg-(--base-01) border border-(--base-03) overflow-hidden">
      <div className="flex items-center justify-between px-4 pt-3.5 pb-1">
        <div className="flex items-center gap-2">
          <div className="w-7 h-7 rounded-md bg-(--base-03) flex items-center justify-center">
            {icon}
          </div>
          <span className="input-label text-[11px]">{label}</span>
        </div>
        <span
          className="font-display text-xl font-bold tabular-nums"
          style={{ color: accentColor }}
        >
          {currentValue}
        </span>
      </div>
      <div className="h-44 px-1 pb-2">
        {children}
      </div>
    </div>
  );
}

interface OverviewViewProps {
  server: Server;
}

export default function OverviewView({ server }: OverviewViewProps) {
  const [mode, setMode] = useState<'live' | 'history'>('live');
  const [liveData, setLiveData] = useState<ServerStats[]>([]);
  const [historyData, setHistoryData] = useState<ServerStats[]>([]);
  const [historyRange, setHistoryRange] = useState('24h');
  const [diskUsage, setDiskUsage] = useState<DiskUsage | null>(null);
  const [beamDownloadError, setBeamDownloadError] = useState('');
  const esRef = useRef<EventSource | null>(null);

  useEffect(() => {
    setLiveData([]);

    const token = localStorage.getItem('token') || localStorage.getItem('authToken') || '';
    const url = `${API_URL}/servers/${server.id}/stats/stream?token=${encodeURIComponent(token)}`;
    const es = new EventSource(url);
    esRef.current = es;

    es.onmessage = (e) => {
      try {
        const data = JSON.parse(e.data) as ServerStats;
        setLiveData(prev => [...prev.slice(-59), data]);
      } catch { /* ignore */ }
    };

    return () => { es.close(); };
  }, [server.id]);

  useEffect(() => {
    if (mode !== 'history') return;
    getStatsHistory(server.id, historyRange).then((res: { points?: ServerStats[] }) => {
      setHistoryData(res.points || []);
    }).catch(() => setHistoryData([]));
  }, [mode, historyRange, server.id]);

  useEffect(() => {
    getDiskUsage(server.id).then((data: DiskUsage) => {
      if (data && typeof data.total === 'number') setDiskUsage(data);
    }).catch(() => {});

    const interval = setInterval(() => {
      getDiskUsage(server.id).then((data: DiskUsage) => {
        if (data && typeof data.total === 'number') setDiskUsage(data);
      }).catch(() => {});
    }, 15000);
    return () => clearInterval(interval);
  }, [server.id]);

  const downloadBeam = async (platform: string) => {
    const filename = platform === 'windows' ? 'DylarisBeam.exe' : 'DylarisBeam';
    setBeamDownloadError('');
    try {
      const res = await fetch(`${API_URL}/tools/beam?platform=${platform}`);
      if (!res.ok) {
        setBeamDownloadError('Binary not available yet — build Beam first.');
        return;
      }
      const contentType = res.headers.get('Content-Type') ?? '';
      if (contentType.includes('application/json')) {
        setBeamDownloadError('Binary not available yet — build Beam first.');
        return;
      }
      const blob = await res.blob();
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = filename;
      document.body.appendChild(a);
      a.click();
      document.body.removeChild(a);
      URL.revokeObjectURL(url);
    } catch {
      setBeamDownloadError('Download failed.');
    }
  };

  const chartData = mode === 'live' ? liveData : historyData;
  const timeFormatter = mode === 'live' ? formatTime : formatTimeShort;

  const isOffline = server.status === 'stopped' || server.status === 'offline' || server.status === 'pending_setup';

  const latestCpu = chartData.length > 0 ? (chartData[chartData.length - 1].cpu ?? 0) : 0;
  const latestRamMb = chartData.length > 0 ? (chartData[chartData.length - 1].memUsed ?? 0) : 0;

  const tooltipStyle = {
    backgroundColor: 'var(--base-02)',
    border: '1px solid var(--base-04)',
    borderRadius: 'var(--radius-md)',
    fontSize: '12px',
    color: 'var(--base-09)',
    boxShadow: '0 8px 24px rgba(0,0,0,0.4)',
    padding: '8px 12px',
  };

  const emptyState = (
    <div className="h-full flex items-center justify-center text-(--base-06) text-sm font-mono">
      {isOffline ? 'Server offline' : 'Waiting for data...'}
    </div>
  );

  return (
    <div className="h-full flex flex-col gap-4 overflow-y-auto">
      {/* Header Controls */}
      <div className="flex items-center gap-3">
        <div className="flex bg-(--base-03) rounded-md p-0.5">
          <button
            onClick={() => setMode('live')}
            className={`btn text-sm px-3 py-1.5 border-0 rounded-sm ${
              mode === 'live' ? 'bg-(--accent) text-white' : 'bg-transparent text-(--base-07) hover:text-(--base-09)'
            }`}
          >
            Live
          </button>
          <button
            onClick={() => setMode('history')}
            className={`btn text-sm px-3 py-1.5 border-0 rounded-sm ${
              mode === 'history' ? 'bg-(--accent) text-white' : 'bg-transparent text-(--base-07) hover:text-(--base-09)'
            }`}
          >
            History
          </button>
        </div>
        {mode === 'history' && (
          <div className="flex gap-1">
            {['1h', '6h', '12h', '24h'].map(r => (
              <button
                key={r}
                onClick={() => setHistoryRange(r)}
                className={`px-2.5 py-1 rounded-sm text-xs font-medium transition-colors ${
                  historyRange === r ? 'bg-(--base-04) text-(--base-09)' : 'text-(--base-07) hover:text-(--base-09)'
                }`}
              >
                {r}
              </button>
            ))}
          </div>
        )}
        <span className="text-[10px] text-(--base-06) ml-auto font-mono tracking-wide">
          {mode === 'live' ? `${liveData.length} pts` : `${historyData.length} pts`}
        </span>
      </div>

      {/* Charts Grid */}
      <div className="grid grid-cols-1 lg:grid-cols-2 gap-3">
        {/* CPU Chart */}
        <ChartCard
          icon={<Cpu size={14} className="text-(--base-06)" />}
          label="CPU"
          currentValue={chartData.length > 0 ? `${latestCpu.toFixed(1)}%` : '—'}
          accentColor="var(--primary-light)"
        >
          {chartData.length > 1 ? (
            <ResponsiveContainer width="100%" height="100%">
              <AreaChart data={chartData} margin={{ top: 8, right: 8, bottom: 0, left: -12 }}>
                <defs>
                  <linearGradient id="cpuFill" x1="0" y1="0" x2="0" y2="1">
                    <stop offset="0%" stopColor="var(--primary)" stopOpacity={0.2} />
                    <stop offset="100%" stopColor="var(--primary)" stopOpacity={0} />
                  </linearGradient>
                </defs>
                <CartesianGrid stroke="var(--base-03)" strokeDasharray="3 3" vertical={false} />
                <XAxis dataKey="ts" tickFormatter={timeFormatter} tick={{ fontSize: 10, fill: 'var(--base-05)' }} axisLine={false} tickLine={false} />
                <YAxis domain={[0, 100]} tick={{ fontSize: 10, fill: 'var(--base-05)' }} axisLine={false} tickLine={false} unit="%" />
                <Tooltip
                  contentStyle={tooltipStyle}
                  labelFormatter={(v) => formatTime(v as number)}
                  formatter={(v) => [`${Number(v).toFixed(1)}%`, 'CPU']}
                  cursor={{ stroke: 'var(--base-05)', strokeWidth: 1, strokeDasharray: '4 4' }}
                />
                <Area type="monotone" dataKey="cpu" stroke="var(--primary)" fill="url(#cpuFill)" strokeWidth={2} dot={false} isAnimationActive={false} />
              </AreaChart>
            </ResponsiveContainer>
          ) : emptyState}
        </ChartCard>

        {/* RAM Chart */}
        <ChartCard
          icon={<MemoryStick size={14} className="text-(--base-06)" />}
          label="RAM"
          currentValue={chartData.length > 0 ? (latestRamMb >= 1024 ? `${(latestRamMb / 1024).toFixed(1)} GB` : `${Math.round(latestRamMb)} MB`) : '—'}
          accentColor="var(--accent-light)"
        >
          {chartData.length > 1 ? (
            <ResponsiveContainer width="100%" height="100%">
              <AreaChart data={chartData} margin={{ top: 8, right: 8, bottom: 0, left: -12 }}>
                <defs>
                  <linearGradient id="ramFill" x1="0" y1="0" x2="0" y2="1">
                    <stop offset="0%" stopColor="var(--accent)" stopOpacity={0.2} />
                    <stop offset="100%" stopColor="var(--accent)" stopOpacity={0} />
                  </linearGradient>
                </defs>
                <CartesianGrid stroke="var(--base-03)" strokeDasharray="3 3" vertical={false} />
                <XAxis dataKey="ts" tickFormatter={timeFormatter} tick={{ fontSize: 10, fill: 'var(--base-05)' }} axisLine={false} tickLine={false} />
                <YAxis domain={[0, server.memory]} tick={{ fontSize: 10, fill: 'var(--base-05)' }} axisLine={false} tickLine={false} tickFormatter={(v: number) => v >= 1024 ? `${(v / 1024).toFixed(1)}G` : `${v}M`} />
                <Tooltip
                  contentStyle={tooltipStyle}
                  labelFormatter={(v) => formatTime(v as number)}
                  formatter={(v) => { const n = Number(v); return [`${n >= 1024 ? (n / 1024).toFixed(1) + ' GB' : n + ' MB'}`, 'RAM']; }}
                  cursor={{ stroke: 'var(--base-05)', strokeWidth: 1, strokeDasharray: '4 4' }}
                />
                <Area type="monotone" dataKey="memUsed" stroke="var(--accent)" fill="url(#ramFill)" strokeWidth={2} dot={false} isAnimationActive={false} />
              </AreaChart>
            </ResponsiveContainer>
          ) : emptyState}
        </ChartCard>
      </div>

      {/* Disk Limit Warning */}
      {diskUsage?.warning && diskUsage.limit > 0 && (
        <div className={`border rounded-xl p-4 flex items-center gap-3 ${
          diskUsage.warning === 'full'
            ? 'bg-(--error-ghost) border-(--error-border)'
            : diskUsage.warning === '90'
              ? 'bg-(--error-ghost) border-(--error-border)'
              : 'bg-(--warning-ghost) border-(--warning-border)'
        }`}>
          <AlertTriangle size={20} className={
            diskUsage.warning === '80' ? 'text-(--warning)' : 'text-(--error-light)'
          } />
          <div>
            <p className={`text-sm font-semibold ${
              diskUsage.warning === '80' ? 'text-(--warning)' : 'text-(--error-light)'
            }`}>
              {diskUsage.warning === 'full'
                ? 'Storage limit reached'
                : diskUsage.warning === '90'
                  ? 'Storage limit critical'
                  : 'Storage running low'}
            </p>
            <p className="text-xs text-(--base-07)">
              {Math.round((diskUsage.total / diskUsage.limit) * 100)}% used — {formatBytes(diskUsage.total)} of {formatBytes(diskUsage.limit)}
            </p>
          </div>
        </div>
      )}

      {/* Disk Usage Section — iPhone-style stacked bar */}
      {diskUsage && (() => {
        const segmentColors = [
          'var(--color-primary)',
          'var(--color-accent)',
          'var(--color-success)',
          'var(--color-warning)',
          'var(--color-primary-light)',
          'var(--color-accent-light)',
          'var(--color-success-light)',
          'var(--color-error-light)',
        ];
        const sorted = diskUsage.subServers
          ? Object.entries(diskUsage.subServers).sort(([, a], [, b]) => b - a)
          : [];
        const capacity = diskUsage.limit > 0 ? diskUsage.limit : diskUsage.total;
        const freeBytes = diskUsage.limit > 0 ? Math.max(diskUsage.limit - diskUsage.total, 0) : 0;

        return (
          <div className="card p-5">
            <div className="flex items-center justify-between mb-4">
              <h3 className="input-label flex items-center gap-1.5">
                <HardDrive size={16} className="text-(--base-07)" />
                Disk Usage
              </h3>
              <span className="font-mono text-sm text-(--base-09)">
                {formatBytes(diskUsage.total)}
                {diskUsage.limit > 0 && <span className="text-(--base-06)"> / {formatBytes(diskUsage.limit)}</span>}
              </span>
            </div>

            {/* Stacked bar */}
            <div className="w-full h-4 bg-(--base-04) rounded-sm overflow-hidden flex">
              {sorted.map(([name, bytes], i) => {
                const pct = capacity > 0 ? (bytes / capacity) * 100 : 0;
                if (pct < 0.3) return null;
                return (
                  <div
                    key={name}
                    title={`${name}: ${formatBytes(bytes)}`}
                    style={{
                      width: `${pct}%`,
                      backgroundColor: segmentColors[i % segmentColors.length],
                      minWidth: pct > 0 ? '2px' : 0,
                    }}
                    className="h-full transition-all duration-300"
                  />
                );
              })}
            </div>

            {/* Legend */}
            <div className="flex flex-wrap gap-x-5 gap-y-2 mt-3">
              {sorted.map(([name, bytes], i) => (
                <div key={name} className="flex items-center gap-2 text-sm">
                  <span
                    className="w-2.5 h-2.5 rounded-[3px] shrink-0"
                    style={{ backgroundColor: segmentColors[i % segmentColors.length] }}
                  />
                  <span className="text-(--base-08)">{name}</span>
                  <span className="font-mono text-xs text-(--base-06)">{formatBytes(bytes)}</span>
                </div>
              ))}
              {diskUsage.limit > 0 && freeBytes > 0 && (
                <div className="flex items-center gap-2 text-sm">
                  <span className="w-2.5 h-2.5 rounded-[3px] shrink-0 bg-(--base-04)" />
                  <span className="text-(--base-06)">Free</span>
                  <span className="font-mono text-xs text-(--base-06)">{formatBytes(freeBytes)}</span>
                </div>
              )}
            </div>
          </div>
        );
      })()}

      {/* Beam Desktop App */}
      <div className="card p-5">
        <div className="flex items-center gap-4">
          <div className="w-10 h-10 rounded-md bg-(--accent-ghost) border border-(--accent-border) flex items-center justify-center shrink-0">
            <MonitorDown size={20} className="text-(--accent-light)" />
          </div>
          <div className="flex-1 min-w-0">
            <h3 className="input-label flex items-center gap-1.5 mb-1">
              Beam Desktop App
              <span className="text-[9px] font-mono uppercase tracking-[0.08em] text-(--accent-light) bg-(--accent-ghost) border border-(--accent-border) px-1.5 py-0.5 rounded-sm">Beta</span>
            </h3>
            <p className="text-xs text-(--base-06)">
              Transfer large files, bulk upload/download server data directly — faster than SFTP.
            </p>
          </div>
          <div className="flex flex-col items-end gap-1.5 shrink-0">
            <div className="flex items-center gap-2">
              {(['windows', 'linux', 'macos-arm'] as const).map(platform => (
                <button
                  key={platform}
                  onClick={() => downloadBeam(platform)}
                  className="btn btn-secondary px-3 py-2 text-xs flex items-center gap-1.5"
                >
                  <Download size={12} />
                  {platform === 'windows' ? 'Windows' : platform === 'linux' ? 'Linux' : 'macOS'}
                </button>
              ))}
            </div>
            {beamDownloadError && (
              <span className="text-[11px] text-(--error-light) font-mono">{beamDownloadError}</span>
            )}
          </div>
        </div>
      </div>

    </div>
  );
}
