"use client";

import { useState, useEffect, useCallback } from 'react';
import { Server, linkServerToProxy, unlinkServerFromProxy, getServerRoutes, createServerRoute, deleteServerRoute, GatewayRoute } from '@/lib/api';
import { Network, Link, Unlink, Info, Globe, Plus, Trash2, AlertCircle } from 'lucide-react';

function getStatusDot(status: string) {
  switch (status) {
    case 'online': return 'bg-(--success-light)';
    case 'stopped': case 'offline': return 'bg-(--error)';
    default: return 'bg-(--warning) animate-pulse';
  }
}

interface NetworkViewProps {
  server: Server;
  allServers: Server[];
  onServerSelect?: (id: number) => void;
  onRefreshServers?: () => void;
}

export default function NetworkView({ server, allServers, onServerSelect, onRefreshServers }: NetworkViewProps) {
  const [selectedId, setSelectedId] = useState('');
  const [linkLoading, setLinkLoading] = useState(false);

  // Routes state
  const [routes, setRoutes] = useState<GatewayRoute[]>([]);
  const [routesLoading, setRoutesLoading] = useState(false);
  const [newDomain, setNewDomain] = useState('');
  const [newPort, setNewPort] = useState(25565);
  const [routeError, setRouteError] = useState('');
  const [routeCreating, setRouteCreating] = useState(false);
  const [confirmDeleteRoute, setConfirmDeleteRoute] = useState<number | null>(null);

  const isProxy = server.serverType === 'proxy';
  const isGameServer = !isProxy;

  const linkedProxy = isGameServer && server.proxyId
    ? allServers.find(s => s.id === server.proxyId)
    : null;

  const linkedChildren = isProxy
    ? allServers.filter(s => s.proxyId === server.id && s.serverType !== 'proxy')
    : [];

  const availableProxies = isGameServer
    ? allServers.filter(s => s.serverType === 'proxy' && s.id !== server.id)
    : [];

  const availableGameServers = isProxy
    ? allServers.filter(s => s.serverType !== 'proxy' && !s.proxyId && s.id !== server.id)
    : [];

  const handleLink = async (serverId: number, proxyId: number) => {
    setLinkLoading(true);
    try {
      await linkServerToProxy(serverId, proxyId);
      onRefreshServers?.();
    } catch { /* ignore */ }
    setLinkLoading(false);
    setSelectedId('');
  };

  const handleUnlink = async (serverId: number) => {
    setLinkLoading(true);
    try {
      await unlinkServerFromProxy(serverId);
      onRefreshServers?.();
    } catch { /* ignore */ }
    setLinkLoading(false);
  };

  // Routes
  const loadRoutes = useCallback(async () => {
    setRoutesLoading(true);
    try {
      const res = await getServerRoutes(server.id);
      if (res.routes) setRoutes(res.routes);
      else if (Array.isArray(res)) setRoutes(res);
      else setRoutes([]);
    } catch { setRoutes([]); }
    setRoutesLoading(false);
  }, [server.id]);

  useEffect(() => {
    loadRoutes();
  }, [loadRoutes]);

  const handleCreateRoute = async () => {
    setRouteError('');
    const domain = newDomain.trim().toLowerCase();
    if (!domain) { setRouteError('Domain is required'); return; }
    if (!/^(\*\.)?[a-z0-9]([a-z0-9.-]*[a-z0-9])?$/.test(domain)) {
      setRouteError('Invalid domain format');
      return;
    }
    setRouteCreating(true);
    try {
      const res = await createServerRoute(server.id, { domain, targetPort: newPort });
      if (res.error) {
        setRouteError(res.error);
      } else {
        setNewDomain('');
        setNewPort(25565);
        loadRoutes();
      }
    } catch {
      setRouteError('Failed to create route');
    }
    setRouteCreating(false);
  };

  const handleDeleteRoute = async (routeId: number) => {
    try {
      await deleteServerRoute(server.id, routeId);
      setRoutes(prev => prev.filter(r => r.ID !== routeId));
    } catch { /* ignore */ }
    setConfirmDeleteRoute(null);
  };

  return (
    <div className="max-w-3xl mx-auto space-y-6">
      {/* Proxy Linking Section */}
      <div className="card p-6">
        <h2 className="modal-title mb-1 flex items-center gap-2">
          <Network size={20} className="text-(--accent-light)" />
          {isProxy ? 'Linked Servers' : 'Proxy Linking'}
        </h2>
        <p className="text-sm text-(--base-06) mb-5">
          {isProxy
            ? 'Manage game servers linked to this proxy. Linked servers appear as sub-containers in the sidebar.'
            : 'Link this server to a proxy to join a network. Linked servers are managed through the proxy.'}
        </p>

        {/* Game Server: linked proxy or link dropdown */}
        {isGameServer && (
          <div className="space-y-3">
            {linkedProxy ? (
              <div className="flex items-center justify-between bg-(--base-02) rounded-md px-4 py-3 border border-(--base-03)">
                <div className="flex items-center gap-3">
                  <Network size={16} className="text-(--accent-light)" />
                  <div>
                    <button
                      onClick={() => onServerSelect?.(linkedProxy.id)}
                      className="text-sm font-medium text-(--base-09) hover:text-(--accent-light) transition-colors"
                    >
                      {linkedProxy.name}
                    </button>
                    <div className="font-mono text-[10px] text-(--base-06) uppercase tracking-[0.08em]">Linked Proxy</div>
                  </div>
                  <div className={`badge-dot ${getStatusDot(linkedProxy.status)}`} title={linkedProxy.status}></div>
                </div>
                <button
                  onClick={() => handleUnlink(server.id)}
                  disabled={linkLoading}
                  className="btn btn-ghost text-xs px-3 py-1.5 text-(--error) hover:bg-(--error-ghost)"
                >
                  <Unlink size={12} />
                  Unlink
                </button>
              </div>
            ) : (
              <>
                <div className="flex items-center gap-2">
                  <select
                    value={selectedId}
                    onChange={e => setSelectedId(e.target.value)}
                    className="input-field flex-1 text-sm"
                  >
                    <option value="">Select a proxy...</option>
                    {availableProxies.map(p => (
                      <option key={p.id} value={String(p.id)}>{p.name}</option>
                    ))}
                  </select>
                  <button
                    onClick={() => selectedId && handleLink(server.id, Number(selectedId))}
                    disabled={!selectedId || linkLoading}
                    className="btn btn-primary text-xs px-4 py-2.5"
                  >
                    <Link size={12} />
                    Link
                  </button>
                </div>
                {availableProxies.length === 0 && (
                  <p className="text-xs text-(--base-06)">No proxy servers available. Create a proxy server first.</p>
                )}
              </>
            )}
          </div>
        )}

        {/* Proxy Server: linked children */}
        {isProxy && (
          <div className="space-y-3">
            {linkedChildren.length > 0 ? (
              <div className="space-y-2">
                {linkedChildren.map(child => (
                  <div key={child.id} className="flex items-center justify-between bg-(--base-02) rounded-md px-4 py-2.5 border border-(--base-03)">
                    <div className="flex items-center gap-3">
                      <div className={`badge-dot ${getStatusDot(child.status)}`} title={child.status}></div>
                      <button
                        onClick={() => onServerSelect?.(child.id)}
                        className="text-sm font-medium text-(--base-09) hover:text-(--accent-light) transition-colors"
                      >
                        {child.name}
                      </button>
                      {child.activeSubServer && (
                        <span className="text-[10px] bg-(--base-03) px-1.5 py-0.5 rounded text-(--base-06) font-mono">
                          {child.activeSubServer}
                        </span>
                      )}
                    </div>
                    <button
                      onClick={() => handleUnlink(child.id)}
                      disabled={linkLoading}
                      className="btn btn-ghost text-xs px-2 py-1 text-(--error) hover:bg-(--error-ghost)"
                    >
                      <Unlink size={12} />
                    </button>
                  </div>
                ))}
              </div>
            ) : (
              <p className="text-sm text-(--base-06) italic">No servers linked to this proxy yet.</p>
            )}

            {availableGameServers.length > 0 && (
              <div className="flex items-center gap-2 pt-2 border-t border-(--base-03)">
                <select
                  value={selectedId}
                  onChange={e => setSelectedId(e.target.value)}
                  className="input-field flex-1 text-sm"
                >
                  <option value="">Add a server...</option>
                  {availableGameServers.map(s => (
                    <option key={s.id} value={String(s.id)}>{s.name}</option>
                  ))}
                </select>
                <button
                  onClick={() => selectedId && handleLink(Number(selectedId), server.id)}
                  disabled={!selectedId || linkLoading}
                  className="btn btn-primary text-xs px-4 py-2.5"
                >
                  <Link size={12} />
                  Add
                </button>
              </div>
            )}
          </div>
        )}
      </div>

      {/* Routes / Domains Section */}
      <div className="card p-6">
          <h2 className="modal-title mb-1 flex items-center gap-2">
            <Globe size={20} className="text-(--accent-light)" />
            Routes / Domains
          </h2>
          <p className="text-sm text-(--base-06) mb-5">
            Map custom domains to this server through the gateway. Routes are synced to gates automatically.
          </p>

          {/* Existing routes */}
          {routesLoading ? (
            <p className="text-sm text-(--base-07)">Loading routes...</p>
          ) : routes.length > 0 ? (
            <div className="space-y-2 mb-4">
              {routes.map(route => (
                <div key={route.ID} className="flex items-center justify-between bg-(--base-02) rounded-md px-4 py-2.5 border border-(--base-03)">
                  <div className="flex items-center gap-3">
                    <Globe size={14} className="text-(--accent-light) shrink-0" />
                    <div>
                      <div className="text-sm font-medium text-(--base-09)">{route.domain}</div>
                      <div className="font-mono text-[10px] text-(--base-06) uppercase tracking-[0.08em]">
                        Port {route.target_port}
                        {route.link_name && <> &middot; Link: {route.link_name}</>}
                      </div>
                    </div>
                  </div>
                  {confirmDeleteRoute === route.ID ? (
                    <div className="flex gap-2">
                      <button onClick={() => handleDeleteRoute(route.ID)} className="btn btn-danger px-3 py-1 text-xs">Delete</button>
                      <button onClick={() => setConfirmDeleteRoute(null)} className="btn btn-secondary px-3 py-1 text-xs">Cancel</button>
                    </div>
                  ) : (
                    <button
                      onClick={() => setConfirmDeleteRoute(route.ID)}
                      className="text-(--error-light) hover:text-(--error) transition-colors"
                      title="Delete route"
                    >
                      <Trash2 size={16} />
                    </button>
                  )}
                </div>
              ))}
            </div>
          ) : (
            <p className="text-sm text-(--base-06) italic mb-4">No routes configured for this server.</p>
          )}

          {/* Add route form */}
          <div className="bg-(--base-03) rounded-lg border border-(--base-04) p-4">
            <div className="font-mono text-[10px] uppercase tracking-[0.08em] text-(--base-06) mb-3">Add Route</div>
            <div className="flex gap-2 items-end">
              <div className="flex-1">
                <label className="input-label mb-1.5 block">Domain</label>
                <input
                  type="text"
                  value={newDomain}
                  onChange={e => { setNewDomain(e.target.value); setRouteError(''); }}
                  onKeyDown={e => e.key === 'Enter' && handleCreateRoute()}
                  placeholder="play.example.com"
                  className="input-field w-full text-sm"
                />
              </div>
              <div className="w-36">
                <label className="input-label mb-1.5 block">Port</label>
                <select
                  value={newPort}
                  onChange={e => setNewPort(Number(e.target.value))}
                  className="input-field w-full text-sm"
                >
                  <option value={25565}>MC (25565)</option>
                  <option value={80}>HTTP (80)</option>
                  <option value={443}>HTTPS (443)</option>
                </select>
              </div>
              <button
                onClick={handleCreateRoute}
                disabled={routeCreating || !newDomain.trim()}
                className="btn btn-primary text-xs px-4 py-2.5 disabled:opacity-50"
              >
                <Plus size={14} />
                Add
              </button>
            </div>
            {routeError && (
              <div className="flex items-center gap-2 mt-2 text-sm text-(--error-light)">
                <AlertCircle size={14} />
                {routeError}
              </div>
            )}
          </div>
      </div>

      {/* Info Card */}
      <div className="flex items-start gap-3 bg-(--base-03) border border-(--base-04) rounded-xl px-4 py-3">
        <Info size={16} className="text-(--base-06) mt-0.5 shrink-0" />
        <div className="text-sm text-(--base-07) space-y-1">
          {isProxy ? (
            <>
              <p>Linked servers appear nested under this proxy in the sidebar.</p>
              <p>Use the <span className="font-medium text-(--base-09)">Members</span> tab to manage permissions. Enable <span className="font-medium text-(--base-09)">Inherit</span> to automatically grant access to all linked servers.</p>
            </>
          ) : (
            <>
              <p>Linking to a proxy groups this server in the sidebar and enables network features.</p>
              <p>If the proxy owner grants you <span className="font-medium text-(--base-09)">Inherit</span> permissions, you'll automatically get access to all linked servers.</p>
            </>
          )}
          <p>Routes map custom domains to this server through the gateway infrastructure. The domain resolves to the gate, which tunnels traffic through a link to your server.</p>
        </div>
      </div>
    </div>
  );
}
