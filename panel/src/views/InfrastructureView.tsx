"use client";

import { useState, useEffect, useRef } from 'react';
import {
  Server, Globe, Network, RefreshCw, Trash2, AlertTriangle, X,
  Cpu, MemoryStick, ArrowDownToLine, ArrowUpFromLine, Shield, Activity,
  Users, Link2, HardDrive
} from 'lucide-react';
import { getInfrastructureOverview, getNodes, getNodeServers, forceDeleteNode, GatewayGate, GatewayLink, GateStats } from '@/lib/api';

interface NodeInfo {
  id: number;
  name: string;
  token?: string;
  address: string;
  status: string;
  lastSeenAt?: string;
  serverCount?: number;
  privateIps?: string[];
  cpuUsage?: number;
  ramFree?: number;
  ramTotal?: number;
  linkCount?: number;
}

interface ServerInfo {
  id: number;
  uuid: string;
  name: string;
  status: string;
}

interface InfrastructureData {
  gates: GatewayGate[];
  links: GatewayLink[];
  nodes: NodeInfo[];
  routeCount: number;
  onlineLinks: number;
  onlineGates: number;
  totalTunnels: number;
}

type Tab = 'nodes' | 'gates';

function timeAgo(dateStr: string): string {
  const now = Date.now();
  const then = new Date(dateStr).getTime();
  const diff = Math.max(0, now - then);
  const seconds = Math.floor(diff / 1000);
  if (seconds < 60) return `${seconds}s ago`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  const days = Math.floor(hours / 24);
  return `${days}d ago`;
}

function formatBytes(bytes: number): string {
  if (bytes === 0) return '0 B';
  const k = 1024;
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  return `${(bytes / Math.pow(k, i)).toFixed(1)} ${sizes[i]}`;
}

function formatSpeed(bps: number): string {
  return `${formatBytes(bps)}/s`;
}

function ProgressBar({ value, max, color = 'accent' }: { value: number; max: number; color?: 'accent' | 'primary' | 'success' }) {
  const pct = max > 0 ? Math.min(100, (value / max) * 100) : 0;
  const colorClass = color === 'success' ? 'bg-(--success-light)' : color === 'primary' ? 'bg-(--primary)' : 'bg-(--accent)';
  return (
    <div className="h-1.5 rounded-full bg-(--base-03) overflow-hidden">
      <div className={`h-full rounded-full transition-all duration-500 ${colorClass}`} style={{ width: `${pct}%` }} />
    </div>
  );
}

function StatCard({ label, value, sub, icon }: { label: string; value: string | number; sub?: string; icon: React.ReactNode }) {
  return (
    <div className="card p-4 flex flex-col gap-1.5">
      <span className="font-mono text-[10px] uppercase tracking-[0.08em] text-(--base-06)">{label}</span>
      <div className="flex items-baseline gap-1.5">
        <div className="text-(--accent-light) flex items-center" style={{ marginBottom: 1 }}>{icon}</div>
        <span className="font-display text-2xl font-bold text-(--base-09) tabular-nums leading-none">{value}</span>
        {sub && <span className="text-sm font-normal text-(--base-05)">{sub}</span>}
      </div>
    </div>
  );
}

function NodeCard({ node, onDelete }: { node: NodeInfo; onDelete: (node: NodeInfo) => void }) {
  const isOnline = node.status === 'online';
  const displayName = node.token || node.name;
  const serverCount = node.serverCount ?? 0;
  const linkCount = node.linkCount ?? 0;
  const hasStats = (node.ramTotal ?? 0) > 0;

  return (
    <div className="card p-4 flex flex-col gap-3">
      {/* Header */}
      <div className="flex items-center justify-between gap-2">
        <div className="flex items-center gap-2.5 min-w-0">
          <div className={`w-2 h-2 rounded-full shrink-0 ${isOnline ? 'bg-(--success-light) shadow-[0_0_6px_var(--success-light)]' : 'bg-(--error)'}`} />
          <p className="text-sm font-semibold text-(--base-09) truncate">{displayName}</p>
        </div>
        <div className="flex items-center gap-2 shrink-0">
          <span className={`text-[10px] font-mono uppercase tracking-[0.08em] ${isOnline ? 'text-(--success-light)' : 'text-(--error)'}`}>
            {node.status}
          </span>
          {!isOnline && (
            <button
              onClick={() => onDelete(node)}
              className="p-1 rounded-md hover:bg-(--error)/10 text-(--base-05) hover:text-(--error-light) transition-colors"
              title="Delete node"
            >
              <Trash2 size={13} />
            </button>
          )}
        </div>
      </div>

      {/* Info row */}
      <div className="flex items-center gap-4 pt-2 border-t border-(--base-03)">
        <div className="flex items-center gap-1.5">
          <Server size={11} className="text-(--base-05)" />
          <span className="text-xs font-mono text-(--base-07)">
            <span className="text-(--base-09) font-semibold">{serverCount}</span>
            <span className="text-(--base-06)"> server{serverCount !== 1 ? 's' : ''}</span>
          </span>
        </div>
        <div className="flex items-center gap-1.5">
          <Link2 size={11} className="text-(--base-05)" />
          <span className="text-xs font-mono text-(--base-07)">
            <span className="text-(--base-09) font-semibold">{linkCount}</span>
            <span className="text-(--base-06)"> link{linkCount !== 1 ? 's' : ''}</span>
          </span>
        </div>
      </div>

      {/* CPU */}
      {hasStats && node.cpuUsage !== undefined && (
        <div>
          <div className="flex items-center justify-between mb-1">
            <span className="flex items-center gap-1.5 text-[10px] font-mono uppercase tracking-[0.08em] text-(--base-06)">
              <Cpu size={10} /> CPU
            </span>
            <span className="text-[10px] font-mono text-(--base-07) tabular-nums">{node.cpuUsage.toFixed(1)}%</span>
          </div>
          <ProgressBar value={node.cpuUsage} max={100} color="accent" />
        </div>
      )}

      {/* RAM */}
      {hasStats && node.ramFree !== undefined && node.ramTotal !== undefined && (
        <div>
          <div className="flex items-center justify-between mb-1">
            <span className="flex items-center gap-1.5 text-[10px] font-mono uppercase tracking-[0.08em] text-(--base-06)">
              <MemoryStick size={10} /> RAM
            </span>
            <span className="text-[10px] font-mono text-(--base-07) tabular-nums">
              {formatBytes(node.ramFree)} free / {formatBytes(node.ramTotal)}
            </span>
          </div>
          <ProgressBar value={node.ramTotal - node.ramFree} max={node.ramTotal} color="primary" />
        </div>
      )}

      {/* Last seen */}
      {!isOnline && node.lastSeenAt && (
        <p className="text-[10px] text-(--base-05) font-mono">Last seen {timeAgo(node.lastSeenAt)}</p>
      )}
    </div>
  );
}

function GateCard({ gate }: { gate: GatewayGate }) {
  const isOnline = gate.status === 'online';
  const stats = gate.stats;
  const rxBytes = (stats as any)?.rx_bytes as number | undefined;
  const txBytes = (stats as any)?.tx_bytes as number | undefined;
  const hasTotal = (rxBytes ?? 0) > 0 || (txBytes ?? 0) > 0;

  return (
    <div className="card p-4 flex flex-col gap-3">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2.5 min-w-0">
          <div className={`w-2 h-2 rounded-full shrink-0 ${isOnline ? 'bg-(--success-light) shadow-[0_0_6px_var(--success-light)]' : 'bg-(--error)'}`} />
          <div className="min-w-0">
            <p className="text-sm font-semibold text-(--base-09) truncate">{gate.name}</p>
            <p className="text-[10px] font-mono text-(--base-05) truncate">{gate.ip}:{gate.service_port}</p>
          </div>
        </div>
        <span className={`text-[10px] font-mono uppercase tracking-[0.08em] shrink-0 ${isOnline ? 'text-(--success-light)' : 'text-(--error)'}`}>
          {gate.status}
        </span>
      </div>

      {isOnline && stats ? (
        <div className="flex flex-col gap-2.5 pt-1 border-t border-(--base-03)">
          <div>
            <div className="flex items-center justify-between mb-1">
              <span className="flex items-center gap-1.5 text-[10px] font-mono uppercase tracking-[0.08em] text-(--base-06)">
                <Cpu size={10} /> CPU
              </span>
              <span className="text-[10px] font-mono text-(--base-07) tabular-nums">{stats.cpu.toFixed(1)}%</span>
            </div>
            <ProgressBar value={stats.cpu} max={100} color="accent" />
          </div>
          <div>
            <div className="flex items-center justify-between mb-1">
              <span className="flex items-center gap-1.5 text-[10px] font-mono uppercase tracking-[0.08em] text-(--base-06)">
                <MemoryStick size={10} /> RAM
              </span>
              <span className="text-[10px] font-mono text-(--base-07) tabular-nums">
                {formatBytes(stats.ram_used)} / {formatBytes(stats.ram_total)}
              </span>
            </div>
            <ProgressBar value={stats.ram_pct} max={100} color="primary" />
          </div>
          <div className="flex items-center gap-5 pt-0.5">
            <div className="flex items-center gap-1.5">
              <Users size={11} className="text-(--accent-light)" />
              <span className="text-[11px] font-mono text-(--base-07)">
                <span className="text-(--base-09) font-semibold tabular-nums">{stats.active_mc_streams}</span> players
              </span>
            </div>
            <div className="flex items-center gap-1.5">
              <Activity size={11} className="text-(--base-06)" />
              <span className="text-[11px] font-mono text-(--base-07)">
                <span className="text-(--base-09) font-semibold tabular-nums">{stats.active_tunnels}</span> tunnels
              </span>
            </div>
          </div>
          <div className="flex items-center gap-4 flex-wrap">
            <div className="flex items-center gap-1.5">
              <ArrowDownToLine size={10} className="text-(--success-light)" />
              <span className="text-[10px] font-mono text-(--base-07) tabular-nums">{formatSpeed(stats.rx_speed)}</span>
            </div>
            <div className="flex items-center gap-1.5">
              <ArrowUpFromLine size={10} className="text-(--accent-light)" />
              <span className="text-[10px] font-mono text-(--base-07) tabular-nums">{formatSpeed(stats.tx_speed)}</span>
            </div>
            {hasTotal && (
              <div className="flex items-center gap-1 ml-auto">
                <HardDrive size={10} className="text-(--base-05)" />
                <span className="text-[10px] font-mono text-(--base-06) tabular-nums">
                  {formatBytes((rxBytes ?? 0) + (txBytes ?? 0))} total
                </span>
              </div>
            )}
          </div>
          {stats.xdp_enabled && (
            <div className="flex items-center gap-2 flex-wrap">
              <span className="inline-flex items-center gap-1 px-1.5 py-0.5 rounded bg-(--accent)/10 text-[10px] font-mono uppercase tracking-[0.08em] text-(--accent-light)">
                <Shield size={9} /> XDP
              </span>
              <span className="text-[10px] font-mono text-(--base-06) tabular-nums">
                {stats.xdp_dropped_blocked + stats.xdp_dropped_ratelimit} dropped
              </span>
              {stats.xdp_blocked_ips > 0 && (
                <span className="text-[10px] font-mono text-(--error-light) tabular-nums">
                  {stats.xdp_blocked_ips} blocked IPs
                </span>
              )}
            </div>
          )}
        </div>
      ) : (
        <div className="pt-1 border-t border-(--base-03)">
          <p className="text-[10px] font-mono text-(--base-05) italic">No stats available</p>
        </div>
      )}
    </div>
  );
}

export default function InfrastructureView() {
  const [data, setData] = useState<InfrastructureData | null>(null);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [tab, setTab] = useState<Tab>('nodes');
  const intervalRef = useRef<ReturnType<typeof setInterval> | null>(null);

  const [deleteModal, setDeleteModal] = useState<{ node: NodeInfo; servers: ServerInfo[] } | null>(null);
  const [confirmName, setConfirmName] = useState('');
  const [deleting, setDeleting] = useState(false);
  const [toast, setToast] = useState<string | null>(null);

  async function fetchData(isManual = false) {
    if (isManual) setRefreshing(true);
    try {
      const res = await getInfrastructureOverview();
      if (res && res.success !== false) {
        let nodes = res.nodes || [];
        if (nodes.length === 0) {
          try {
            const nodesRes = await getNodes();
            if (Array.isArray(nodesRes)) nodes = nodesRes;
            else if (nodesRes?.nodes) nodes = nodesRes.nodes;
          } catch { /* ignore */ }
        }
        setData({
          gates: res.gates || [],
          links: res.links || [],
          nodes,
          routeCount: res.routeCount ?? 0,
          onlineLinks: res.onlineLinks ?? 0,
          onlineGates: res.onlineGates ?? 0,
          totalTunnels: res.totalTunnels ?? 0,
        });
      }
    } catch { /* keep previous data */ } finally {
      setLoading(false);
      if (isManual) setRefreshing(false);
    }
  }

  useEffect(() => {
    fetchData();
    intervalRef.current = setInterval(() => fetchData(), 10000);
    return () => { if (intervalRef.current) clearInterval(intervalRef.current); };
  }, []);

  useEffect(() => {
    if (toast) {
      const t = setTimeout(() => setToast(null), 3000);
      return () => clearTimeout(t);
    }
  }, [toast]);

  async function openDeleteModal(node: NodeInfo) {
    try {
      const res = await getNodeServers(node.id);
      setDeleteModal({ node, servers: res?.servers || [] });
      setConfirmName('');
    } catch { setToast('Failed to load node servers'); }
  }

  async function handleForceDelete() {
    if (!deleteModal) return;
    setDeleting(true);
    try {
      const res = await forceDeleteNode(deleteModal.node.id);
      if (res?.success) {
        setToast(`Node "${deleteModal.node.name}" deleted`);
        setDeleteModal(null);
        fetchData(true);
      } else { setToast('Delete failed'); }
    } catch { setToast('Delete failed'); } finally { setDeleting(false); }
  }

  const gates = data?.gates ?? [];
  const links = data?.links ?? [];
  const nodes = data?.nodes ?? [];
  const routeCount = data?.routeCount ?? 0;
  const onlineGates = data?.onlineGates ?? 0;
  const onlineNodes = nodes.filter(n => n.status === 'online').length;
  const totalPlayers = gates.reduce((sum, g) => sum + (g.stats?.active_mc_streams ?? 0), 0);

  const gatewayDeployed = gates.length > 0 || links.length > 0;

  // If gates tab is active but gateway is no longer deployed, reset to nodes
  useEffect(() => {
    if (tab === 'gates' && !gatewayDeployed) setTab('nodes');
  }, [tab, gatewayDeployed]);

  if (loading) {
    return (
      <div className="h-full flex items-center justify-center">
        <RefreshCw size={24} className="text-(--base-06) animate-spin" />
      </div>
    );
  }

  const TABS: { id: Tab; label: string; count: number }[] = [
    { id: 'nodes', label: 'Nodes', count: nodes.length },
    ...(gatewayDeployed ? [{ id: 'gates' as Tab, label: 'Gates', count: gates.length }] : []),
  ];

  return (
    <div className="h-full flex flex-col gap-4 overflow-y-auto">

      {/* Header */}
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-3">
          <div className="w-9 h-9 rounded-md bg-(--base-03) flex items-center justify-center">
            <Server size={18} className="text-(--accent-light)" />
          </div>
          <h1 className="font-display text-xl font-bold text-(--base-09)">Infrastructure</h1>
        </div>
        <button
          onClick={() => fetchData(true)}
          disabled={refreshing}
          className="btn px-3 py-1.5 rounded-md bg-(--base-03) border border-(--base-04) text-(--base-07) hover:text-(--base-09) transition-colors flex items-center gap-1.5 text-sm"
        >
          <RefreshCw size={14} className={refreshing ? 'animate-spin' : ''} />
          Refresh
        </button>
      </div>

      {/* Summary Row */}
      <div className={`grid gap-3 ${gatewayDeployed ? 'grid-cols-2 md:grid-cols-4' : 'grid-cols-2'}`}>
        <StatCard label="Nodes" value={nodes.length} icon={<Network size={16} />} />
        <StatCard label="Online" value={onlineNodes} sub={`/ ${nodes.length}`} icon={<Activity size={16} />} />
        {gatewayDeployed && <StatCard label="Gates" value={gates.length} icon={<Server size={16} />} />}
        {gatewayDeployed && <StatCard label="Routes" value={routeCount} icon={<Globe size={16} />} />}
      </div>

      {/* Tabs */}
      <div className="flex items-center gap-0.5 bg-(--base-02) border border-(--base-03) rounded-lg p-1 w-fit">
        {TABS.map(t => (
          <button
            key={t.id}
            onClick={() => setTab(t.id)}
            className={`flex items-center gap-2 px-4 py-1.5 rounded-md text-sm font-medium transition-all ${
              tab === t.id ? 'bg-(--accent) text-white shadow-sm' : 'text-(--base-07) hover:text-(--base-09)'
            }`}
          >
            {t.label}
            <span className={`text-[10px] font-mono tabular-nums px-1.5 py-0.5 rounded-full ${
              tab === t.id ? 'bg-white/20 text-white' : 'bg-(--base-03) text-(--base-06)'
            }`}>
              {t.count}
            </span>
          </button>
        ))}
      </div>

      {/* Tab: Nodes */}
      {tab === 'nodes' && (
        nodes.length === 0 ? (
          <div className="card p-8 text-center text-(--base-06) text-sm">No nodes registered</div>
        ) : (
          <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-3">
            {nodes.map(node => (
              <NodeCard key={node.id} node={node} onDelete={openDeleteModal} />
            ))}
          </div>
        )
      )}

      {/* Tab: Gates */}
      {tab === 'gates' && (
        gates.length === 0 ? (
          <div className="card p-8 text-center text-(--base-06) text-sm">
            No gates registered — gates auto-discover via Redis
          </div>
        ) : (
          <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-3">
            {gates.map(gate => (
              <GateCard key={gate.gate_id} gate={gate} />
            ))}
          </div>
        )
      )}

      {/* Force-Delete Modal */}
      {deleteModal && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60">
          <div className="card w-full max-w-md p-6 flex flex-col gap-4">
            <div className="flex items-center justify-between">
              <div className="flex items-center gap-2">
                <AlertTriangle size={18} className="text-(--error)" />
                <h2 className="font-display text-lg font-bold text-(--base-09)">Force Delete Node</h2>
              </div>
              <button onClick={() => setDeleteModal(null)} className="p-1 text-(--base-06) hover:text-(--base-09)">
                <X size={16} />
              </button>
            </div>
            <div className="rounded-md bg-(--error)/10 border border-(--error)/30 p-3">
              <p className="text-sm text-(--error)">
                This will permanently delete node <strong>&quot;{deleteModal.node.name}&quot;</strong>
                {deleteModal.servers.length > 0 && (
                  <> and all <strong>{deleteModal.servers.length}</strong> server{deleteModal.servers.length !== 1 ? 's' : ''} on it</>
                )}. This cannot be undone.
              </p>
            </div>
            {deleteModal.servers.length > 0 && (
              <div>
                <p className="font-mono text-[10px] uppercase tracking-[0.08em] text-(--base-06) mb-2">Servers on this node</p>
                <div className="rounded-md border border-(--base-03) divide-y divide-(--base-03) max-h-40 overflow-y-auto">
                  {deleteModal.servers.map(srv => (
                    <div key={srv.id} className="px-3 py-2 flex items-center justify-between">
                      <div>
                        <p className="text-sm text-(--base-09)">{srv.name}</p>
                        <p className="text-[10px] font-mono text-(--base-05)">{srv.uuid}</p>
                      </div>
                      <span className="text-[10px] font-mono uppercase text-(--base-06)">{srv.status}</span>
                    </div>
                  ))}
                </div>
              </div>
            )}
            <div>
              <label className="font-mono text-[10px] uppercase tracking-[0.08em] text-(--base-06) block mb-1.5">
                Type &quot;{deleteModal.node.name}&quot; to confirm
              </label>
              <input
                type="text"
                value={confirmName}
                onChange={e => setConfirmName(e.target.value)}
                placeholder={deleteModal.node.name}
                className="w-full px-3 py-2 rounded-md bg-(--base-02) border border-(--base-04) text-(--base-09) text-sm focus:border-(--error) focus:shadow-[0_0_0_3px_rgba(220,38,38,0.15)] outline-none transition-all"
              />
            </div>
            <div className="flex justify-end gap-2">
              <button onClick={() => setDeleteModal(null)} className="px-4 py-2 rounded-md text-sm text-(--base-07) hover:text-(--base-09) transition-colors">
                Cancel
              </button>
              <button
                onClick={handleForceDelete}
                disabled={confirmName !== deleteModal.node.name || deleting}
                className="px-4 py-2 rounded-md text-sm font-semibold bg-(--error) text-white hover:opacity-90 transition-opacity disabled:opacity-40 disabled:cursor-not-allowed"
              >
                {deleting ? 'Deleting...' : 'Force Delete'}
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Toast */}
      {toast && (
        <div className="fixed bottom-6 right-6 z-50 px-4 py-2.5 rounded-md bg-(--base-02) border border-(--base-04) text-sm text-(--base-09) shadow-lg">
          {toast}
        </div>
      )}
    </div>
  );
}
