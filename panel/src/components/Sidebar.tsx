"use client";

import React, { useState, useMemo } from 'react';
import { useParams } from 'next/navigation';
import { Server } from '../lib/api';
import { ShieldCheck, Search, X, ChevronDown, ChevronRight, PlusCircle, Network } from 'lucide-react';
import { useAppData } from '@/lib/AppDataContext';
import GuardedLink from '@/components/GuardedLink';

interface SidebarProps {
  onNewServer?: () => void;
}

interface ProxyGroup {
  proxy: Server;
  children: Server[];
}

function getStatusDot(status: string) {
  switch (status) {
    case 'online':
      return 'bg-(--success-light)';
    case 'stopped':
    case 'offline':
      return 'bg-(--error)';
    case 'disk_full':
      return 'bg-(--error) animate-pulse';
    default:
      return 'bg-(--warning) animate-pulse';
  }
}

function buildProxyHierarchy(servers: Server[]) {
  const proxyMap = new Map<number, ProxyGroup>();
  const standaloneServers: Server[] = [];

  for (const s of servers) {
    if (s.serverType === 'proxy') {
      proxyMap.set(s.id, { proxy: s, children: [] });
    }
  }

  for (const s of servers) {
    if (s.serverType === 'proxy') continue;
    if (s.proxyId && proxyMap.has(s.proxyId)) {
      proxyMap.get(s.proxyId)!.children.push(s);
    } else {
      standaloneServers.push(s);
    }
  }

  return { proxyGroups: Array.from(proxyMap.values()), standaloneServers };
}

export default function Sidebar({ onNewServer }: SidebarProps) {
  const { servers, user: currentUser, proxiesEnabled } = useAppData();
  const params = useParams();
  const activeServerId = params?.id ? Number(params.id) : null;

  const [isAdminMode, setIsAdminMode] = useState(false);
  const [showSearch, setShowSearch] = useState(false);
  const [searchQuery, setSearchQuery] = useState('');
  const [activeTab, setActiveTab] = useState<'mine' | 'invited'>('mine');
  const [collapsedUsers, setCollapsedUsers] = useState<Set<string>>(new Set());
  const [collapsedProxies, setCollapsedProxies] = useState<Set<number>>(new Set());

  const isAdmin = currentUser?.isAdmin ?? false;

  const filteredServers = useMemo(() => {
    if (!searchQuery.trim()) return servers;
    const q = searchQuery.toLowerCase();

    const directMatches = new Set<number>();
    const neededProxyIds = new Set<number>();

    for (const s of servers) {
      const matches = s.name.toLowerCase().includes(q) ||
        String(s.id).includes(q) ||
        (s.ownerName || s.owner || '').toLowerCase().includes(q);

      if (matches) {
        directMatches.add(s.id);
        if (s.proxyId) neededProxyIds.add(s.proxyId);
        if (s.serverType === 'proxy') neededProxyIds.add(s.id);
      }
    }

    return servers.filter(s => directMatches.has(s.id) || neededProxyIds.has(s.id));
  }, [servers, searchQuery]);

  // Backend returns ALL servers when the caller is an admin, tagging
  // foreign-owned ones with role='admin' so the admin view can list
  // them. Without the explicit exclusion here those rows would land in
  // "my servers" -- so an admin sees every server on the platform in
  // the default sidebar without ever toggling admin-mode. Exclude
  // role='admin' from both lanes; it only belongs in the dedicated
  // admin-mode view (renderAdminList).
  const myServers = useMemo(() =>
    filteredServers.filter(s => s.role !== 'invited' && s.role !== 'inherited' && s.role !== 'admin'),
    [filteredServers]
  );
  const invitedServers = useMemo(() =>
    filteredServers.filter(s => s.role === 'invited' || s.role === 'inherited'),
    [filteredServers]
  );

  const toggleUserCollapse = (owner: string) => {
    setCollapsedUsers(prev => {
      const next = new Set(prev);
      if (next.has(owner)) next.delete(owner);
      else next.add(owner);
      return next;
    });
  };

  const toggleProxyCollapse = (proxyId: number, e: React.MouseEvent) => {
    e.stopPropagation();
    e.preventDefault();
    setCollapsedProxies(prev => {
      const next = new Set(prev);
      if (next.has(proxyId)) next.delete(proxyId);
      else next.add(proxyId);
      return next;
    });
  };

  const renderServerItem = (server: Server, isChild = false) => {
    const isActive = activeServerId === server.id;
    const isProxy = server.serverType === 'proxy';
    const roleLabel = server.role === 'inherited' ? 'Inherited' : server.role === 'invited' ? 'Invited' : isProxy ? 'Proxy' : 'Owner';
    return (
      <GuardedLink
        key={server.id}
        href={`/servers/${server.id}`}
        className={`w-full flex items-center justify-between gap-2.5 ${isChild ? 'py-[7px] px-2.5' : 'py-[9px] px-3'} rounded-md transition-all group ${
          isActive
            ? 'bg-(--base-04) border border-(--accent-border)'
            : 'bg-(--base-03) border border-(--base-03) hover:bg-(--base-04) hover:border-(--base-04)'
        }`}
      >
        <div className="min-w-0 flex-1">
          <div className={`font-medium truncate text-sm transition-colors ${isActive ? 'text-(--base-09)' : 'text-(--base-07) group-hover:text-(--base-09)'}`}>
            {server.name}
          </div>
          <div className="mono-label mt-0.5">
            {roleLabel}
          </div>
        </div>
        <div className={`badge-dot ${getStatusDot(server.status)}`} title={server.status}></div>
      </GuardedLink>
    );
  };

  const renderProxyGroup = (group: ProxyGroup) => {
    const isCollapsed = collapsedProxies.has(group.proxy.id);
    const isProxyActive = activeServerId === group.proxy.id;

    return (
      <div key={`proxy-${group.proxy.id}`} className="mb-1.5">
        <GuardedLink
          href={`/servers/${group.proxy.id}`}
          className={`w-full flex items-center gap-2 py-[9px] px-3 rounded-md transition-all cursor-pointer group border-l-2 ${
            isProxyActive
              ? 'bg-(--base-04) border-l-(--accent) border border-(--accent-border)'
              : 'bg-(--base-02) border-l-(--accent)/40 border border-(--base-03) hover:bg-(--base-03) hover:border-(--base-04)'
          }`}
        >
          <Network size={15} className="text-(--accent-light) shrink-0" />
          <div className="min-w-0 flex-1">
            <div className={`font-medium truncate text-sm transition-colors ${isProxyActive ? 'text-(--base-09)' : 'text-(--base-07) group-hover:text-(--base-09)'}`}>
              {group.proxy.name}
            </div>
            <div className="mono-label mt-0.5">
              Proxy
            </div>
          </div>
          <span className="text-[10px] font-mono text-(--base-05) mr-1">({group.children.length})</span>
          <div className={`badge-dot ${getStatusDot(group.proxy.status)}`} title={group.proxy.status}></div>
          <button
            type="button"
            onClick={(e) => toggleProxyCollapse(group.proxy.id, e)}
            className="p-0.5 rounded-md hover:bg-(--base-04) transition-colors"
          >
            {isCollapsed
              ? <ChevronRight size={14} className="text-(--base-06)" />
              : <ChevronDown size={14} className="text-(--base-06)" />
            }
          </button>
        </GuardedLink>

        {!isCollapsed && group.children.length > 0 && (
          <div className="ml-3 mt-1 pl-3 border-l border-(--base-04) space-y-1">
            {group.children.map(child => renderServerItem(child, true))}
          </div>
        )}
      </div>
    );
  };

  const renderServerList = (serverList: Server[]) => {
    const { proxyGroups, standaloneServers } = proxiesEnabled
      ? buildProxyHierarchy(serverList)
      : { proxyGroups: [], standaloneServers: serverList };
    const hasProxies = proxyGroups.length > 0;

    if (proxyGroups.length === 0 && standaloneServers.length === 0) {
      return <div className="text-sm text-(--base-06) italic">No servers found.</div>;
    }

    return (
      <>
        {proxyGroups.map(renderProxyGroup)}
        {hasProxies && standaloneServers.length > 0 && (
          <div className="mono-label text-(--base-05) px-1 pt-2 pb-1">
            Standalone
          </div>
        )}
        <div className="space-y-1">
          {standaloneServers.map(s => renderServerItem(s))}
        </div>
      </>
    );
  };

  const renderAdminList = () => {
    const groups: Record<string, Server[]> = {};
    for (const s of filteredServers) {
      const owner = s.ownerName || s.owner || `User #${s.ownerId}`;
      if (!groups[owner]) groups[owner] = [];
      groups[owner].push(s);
    }

    if (Object.keys(groups).length === 0) {
      return <div className="text-sm text-(--base-06) italic">No servers found.</div>;
    }

    return Object.entries(groups).map(([owner, ownerServers]) => (
      <div key={owner} className="mb-2">
        <button
          onClick={() => toggleUserCollapse(owner)}
          className="flex items-center gap-1 w-full input-label py-1.5 px-1 hover:text-(--base-08) transition-colors"
        >
          <ChevronDown size={14} className="transition-transform" style={{ transform: collapsedUsers.has(owner) ? 'rotate(-90deg)' : 'rotate(0deg)' }} />
          {owner}
          <span className="text-(--base-05) ml-auto">{ownerServers.length}</span>
        </button>
        {!collapsedUsers.has(owner) && (
          <div className="ml-1">
            {renderServerList(ownerServers)}
          </div>
        )}
      </div>
    ));
  };

  return (
    <aside className="w-72 bg-(--base-01) border-r border-(--base-03) flex flex-col h-full shrink-0 z-20">
      <div className="shrink-0 px-4 pt-4 pb-2 border-b border-(--base-03)">
        <div className="flex items-center justify-between mb-2">
          {isAdmin ? (
            <button
              onClick={() => setIsAdminMode(!isAdminMode)}
              className={`btn text-xs px-2.5 py-1 ${isAdminMode ? 'btn-primary' : 'btn-secondary'}`}
            >
              <ShieldCheck size={14} />
              Admin
            </button>
          ) : (
            <span className="input-label">Servers</span>
          )}
          <button
            onClick={() => { setShowSearch(!showSearch); if (showSearch) setSearchQuery(''); }}
            className={`p-1.5 rounded-md transition-colors ${showSearch ? 'bg-(--accent-ghost) text-(--accent-light)' : 'text-(--base-06) hover:bg-(--base-03) hover:text-(--base-09)'}`}
          >
            {showSearch ? <X size={18} /> : <Search size={18} />}
          </button>
        </div>

        {showSearch && (
          <input
            type="text"
            placeholder="Search servers..."
            value={searchQuery}
            onChange={e => setSearchQuery(e.target.value)}
            autoFocus
            className="input-field w-full mb-1 text-sm placeholder:text-(--base-06)"
          />
        )}

        {!isAdminMode && (
          <div className="flex mt-1 bg-(--base-03)/50 rounded-md p-0.5">
            <button
              onClick={() => setActiveTab('mine')}
              className={`flex-1 text-xs font-medium py-1.5 rounded-sm transition-colors ${
                activeTab === 'mine'
                  ? 'bg-(--base-02) text-(--base-09) shadow-sm'
                  : 'text-(--base-07) hover:text-(--base-09)'
              }`}
            >
              My Servers
            </button>
            <button
              onClick={() => setActiveTab('invited')}
              className={`flex-1 text-xs font-medium py-1.5 rounded-sm transition-colors ${
                activeTab === 'invited'
                  ? 'bg-(--base-02) text-(--base-09) shadow-sm'
                  : 'text-(--base-07) hover:text-(--base-09)'
              }`}
            >
              Invited {invitedServers.length > 0 && <span className="ml-1 text-(--accent-light)">({invitedServers.length})</span>}
            </button>
          </div>
        )}
      </div>

      <div className="flex-1 overflow-y-auto px-3 pt-3 pb-2">
        {isAdminMode ? renderAdminList() : (
          <>
            {activeTab === 'mine' && renderServerList(myServers)}
            {activeTab === 'invited' && (
              invitedServers.length === 0
                ? <div className="text-sm text-(--base-06) italic">No invited servers.</div>
                : renderServerList(invitedServers)
            )}
          </>
        )}
      </div>

      <div className="shrink-0 border-t border-(--base-03)">
        <button
          onClick={onNewServer}
          className="btn w-full px-4 py-3 text-sm text-(--accent-light) bg-transparent border-0 hover:bg-(--base-03) justify-start"
        >
          <PlusCircle size={16} />
          New Container
        </button>
      </div>
    </aside>
  );
}
