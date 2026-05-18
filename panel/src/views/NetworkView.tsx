"use client";

import { useState, useEffect } from 'react';
import { Server, linkServerToProxy, unlinkServerFromProxy, getProxyEndpoint, ProxyEndpoint } from '@/lib/api';
import { Network, Link, Unlink, Info, Copy, Server as ServerIcon } from 'lucide-react';

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
  const [endpoints, setEndpoints] = useState<ProxyEndpoint[]>([]);
  const [copiedKey, setCopiedKey] = useState<string | null>(null);

  // Refresh endpoints when link state or server changes — the connect command
  // is async, so we poll a few times to catch the IP assignment.
  useEffect(() => {
    let cancelled = false;
    const fetchOnce = async () => {
      try {
        const res = await getProxyEndpoint(server.id);
        if (!cancelled && res.success && res.endpoints) setEndpoints(res.endpoints);
      } catch { /* ignore */ }
    };
    fetchOnce();
    const interval = setInterval(fetchOnce, 5000);
    return () => { cancelled = true; clearInterval(interval); };
  }, [server.id, server.proxyId]);

  const copyToClipboard = (key: string, text: string) => {
    navigator.clipboard.writeText(text);
    setCopiedKey(key);
    setTimeout(() => setCopiedKey(null), 1500);
  };

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
                    <div className="mono-label">Linked Proxy</div>
                  </div>
                  <div className={`badge-dot ${getStatusDot(linkedProxy.status)}`} title={linkedProxy.status}></div>
                </div>
                <button
                  onClick={() => handleUnlink(server.id)}
                  disabled={linkLoading}
                  className="btn btn-ghost btn-sm text-(--error) hover:bg-(--error-ghost)"
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
                    className="btn btn-primary btn-sm"
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
                      className="btn btn-ghost btn-sm text-(--error) hover:bg-(--error-ghost)"
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
                  className="btn btn-primary btn-sm"
                >
                  <Link size={12} />
                  Add
                </button>
              </div>
            )}
          </div>
        )}
      </div>

      {/* Internal Endpoints — only meaningful inside a proxy network. */}
      {((isProxy && endpoints.length > 0) || (isGameServer && server.proxyId && endpoints.length > 0)) && (
        <div className="card p-6">
          <h2 className="modal-title mb-1 flex items-center gap-2">
            <ServerIcon size={18} className="text-(--accent-light)" />
            Internal Endpoints
          </h2>
          <p className="text-sm text-(--base-06) mb-5">
            Private IPs inside the proxy overlay network. Use these in your Bungee/Velocity <code className="font-mono text-xs text-(--base-08)">config.yml</code>.
          </p>
          <div className="space-y-2">
            {endpoints.map(ep => (
              <div key={ep.serverId} className="flex flex-col gap-1 bg-(--base-02) rounded-md px-3 py-2 border border-(--base-03)">
                <div className="flex items-center justify-between">
                  <span className="text-sm font-medium text-(--base-09)">{ep.serverName}</span>
                  {!ep.ip && <span className="mono-label text-(--warning-light)">waiting for connect…</span>}
                </div>
                {ep.ip && (
                  <div className="flex items-center gap-2 flex-wrap">
                    <span className="mono-label">IP</span>
                    <code className="font-mono text-xs text-(--base-08) bg-(--base-03) px-1.5 py-0.5 rounded">{ep.ip}</code>
                    <button onClick={() => copyToClipboard(`ip-${ep.serverId}`, ep.ip)} className="text-(--base-06) hover:text-(--base-09) transition-colors" title="Copy IP">
                      <Copy size={11} />
                    </button>
                    {copiedKey === `ip-${ep.serverId}` && <span className="mono-label text-(--success-light)">copied</span>}
                    <span className="mono-label ml-3">Hostname</span>
                    <code className="font-mono text-xs text-(--base-08) bg-(--base-03) px-1.5 py-0.5 rounded">{ep.hostname}</code>
                    <button onClick={() => copyToClipboard(`host-${ep.serverId}`, ep.hostname)} className="text-(--base-06) hover:text-(--base-09) transition-colors" title="Copy hostname">
                      <Copy size={11} />
                    </button>
                  </div>
                )}
              </div>
            ))}
          </div>
        </div>
      )}

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
          <p>Domain routes are managed in the <span className="font-medium text-(--base-09)">Setup</span> tab.</p>
        </div>
      </div>
    </div>
  );
}
