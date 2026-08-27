"use client";

import { useCallback, useEffect, useMemo, useState } from 'react';
import { AlertTriangle, ChevronRight, LayoutDashboard } from 'lucide-react';
import { listServerTabs, mintTabProxyAuth, type ServerTab } from '@/lib/api/serverTabs';
import { tabContentSrc } from '@/lib/tabProxy';
import { useAppData } from '@/lib/AppDataContext';
import { DynamicIcon } from '@/lib/icons';
import { Skeleton } from '@/components/Skeleton';
import { systemEvents } from '@/lib/systemEvents';

// Every proxied custom tab the viewer may see, full width, grouped by server.
//
// The per-server tab page still exists and still works; this is where the same
// content gets the whole window instead of the strip left over beside the server
// navigation, which is what most of these are actually for - a map, a dashboard,
// a status board.
//
// The server list comes from AppDataContext, which is the SAME list the Servers
// page renders and therefore the same authorization Core already applied. That
// is deliberate: a second surface onto the same data with a query of its own is
// how this repository has produced the same defect three times (SFTP, players,
// the tab share plane). There is no server query in this file.

// Matches the in-dashboard tab page: the ticket is short-lived (~5min
// server-side), so re-mint comfortably inside that window while a tab is open.
const PROXY_AUTH_REFRESH_MS = 4 * 60 * 1000;

type TabsByServer = Record<number, ServerTab[]>;

export default function CustomTabsPage() {
    const { servers, coreInfo } = useAppData();
    const [tabsByServer, setTabsByServer] = useState<TabsByServer>({});
    const [loading, setLoading] = useState(true);
    const [selected, setSelected] = useState<ServerTab | null>(null);
    const [authState, setAuthState] = useState<'pending' | 'ready' | 'error'>('pending');
    const [authError, setAuthError] = useState<string | null>(null);

    // One request per server, in parallel. A tab list is a small read and the
    // sidebar has to show which servers have tabs before anything is clicked;
    // loading on expand instead would leave every group looking identical until
    // opened. If a fleet ever makes this heavy, the fix is a single
    // list-my-tabs endpoint, not lazier groups.
    const load = useCallback(async () => {
        if (servers.length === 0) {
            setTabsByServer({});
            setLoading(false);
            return;
        }
        const lists = await Promise.all(servers.map(s => listServerTabs(s.id).catch(() => [] as ServerTab[])));
        const next: TabsByServer = {};
        servers.forEach((s, i) => {
            const proxied = lists[i].filter(t => t.enabled && t.mode === 'proxied' && t.surface !== 'page');
            if (proxied.length > 0) next[s.id] = proxied;
        });
        setTabsByServer(next);
        setLoading(false);
    }, [servers]);

    useEffect(() => { load(); }, [load]);

    useEffect(() => {
        const unsub = systemEvents.on('server_tabs.changed', () => { load(); });
        return () => { unsub(); };
    }, [load]);

    // Mint the ticket for the selected tab before its frame is allowed to load,
    // then keep it fresh. A failing background refresh must never tear down a
    // frame that is already working, so only the first mint may show the error.
    useEffect(() => {
        const origin = selected?.proxyOrigin || '';
        if (!origin) return;
        let cancelled = false;
        setAuthState('pending');
        setAuthError(null);

        const mint = async (isInitial: boolean) => {
            const res = await mintTabProxyAuth(origin);
            if (cancelled) return;
            if (res.success) { setAuthState('ready'); return; }
            if (!isInitial) return;
            setAuthState('error');
            setAuthError(res.message || 'Failed to authorize this tab.');
        };
        mint(true);
        const interval = setInterval(() => mint(false), PROXY_AUTH_REFRESH_MS);
        return () => { cancelled = true; clearInterval(interval); };
    }, [selected?.proxyOrigin]);

    const withTabs = useMemo(
        () => servers.filter(s => (tabsByServer[s.id]?.length ?? 0) > 0),
        [servers, tabsByServer],
    );

    if (!coreInfo?.tabProxyAvailable) {
        return (
            <main className="flex-1 flex items-center justify-center p-6">
                <div className="card p-6 max-w-md text-center space-y-2">
                    <AlertTriangle size={20} className="text-(--warning-light) mx-auto" />
                    <p className="text-sm text-(--base-07)">
                        Proxied tabs are not available on this instance. An admin has to give Core a
                        proxy host before a tab can be served.
                    </p>
                </div>
            </main>
        );
    }

    return (
        <div className="flex-1 flex min-h-0">
            <nav aria-label="Custom tabs" className="w-64 shrink-0 border-r border-(--base-03) bg-(--base-01) overflow-y-auto">
                <h1 className="px-4 pt-4 pb-2 text-xs font-medium uppercase tracking-wide text-(--base-06)">
                    Custom tabs
                </h1>
                {loading && (
                    <div className="px-4 space-y-2">
                        <Skeleton className="h-4 w-32 rounded" />
                        <Skeleton className="h-4 w-24 rounded" />
                        <Skeleton className="h-4 w-28 rounded" />
                    </div>
                )}
                {!loading && withTabs.length === 0 && (
                    <p className="px-4 py-3 text-xs text-(--base-06)">
                        None of your servers has a proxied tab yet. Add one under a server&apos;s
                        Config &rarr; Tabs.
                    </p>
                )}
                {!loading && withTabs.map(server => (
                    <section key={server.id} className="pb-2">
                        <h2 className="px-4 py-1.5 text-xs font-medium text-(--base-07) truncate" title={server.name}>
                            {server.name}
                        </h2>
                        <ul>
                            {(tabsByServer[server.id] ?? []).map(tab => {
                                const active = selected?.id === tab.id;
                                return (
                                    <li key={tab.id}>
                                        <button
                                            type="button"
                                            onClick={() => setSelected(tab)}
                                            aria-current={active ? 'page' : undefined}
                                            className={`w-full flex items-center gap-2 px-4 py-1.5 text-sm text-left transition-colors
                                                focus-visible:outline-hidden focus-visible:shadow-(--focus-ring)
                                                ${active
                                                    ? 'bg-(--accent-dim) text-(--base-09) border-l-2 border-(--accent-light)'
                                                    : 'text-(--base-07) border-l-2 border-transparent hover:bg-(--base-02) hover:text-(--base-09)'}`}
                                        >
                                            <DynamicIcon name={tab.icon || 'layout-dashboard'} size={13} className="shrink-0" />
                                            <span className="truncate">{tab.name}</span>
                                            {active && <ChevronRight size={12} className="ml-auto shrink-0" />}
                                        </button>
                                    </li>
                                );
                            })}
                        </ul>
                    </section>
                ))}
            </nav>

            <main className="flex-1 min-w-0 bg-(--base-01)">
                {!selected && (
                    <div className="h-full flex items-center justify-center p-6">
                        <div className="text-center space-y-2">
                            <LayoutDashboard size={22} className="text-(--base-05) mx-auto" />
                            <p className="text-sm text-(--base-06)">Pick a tab to open it here.</p>
                        </div>
                    </div>
                )}
                {selected && authState === 'pending' && (
                    <div className="h-full p-4"><Skeleton className="w-full h-full rounded" /></div>
                )}
                {selected && authState === 'error' && (
                    <div className="h-full flex items-center justify-center p-6">
                        <div className="card p-6 max-w-md text-center space-y-2">
                            <AlertTriangle size={20} className="text-(--warning-light) mx-auto" />
                            <p className="text-sm text-(--base-07)">{authError}</p>
                        </div>
                    </div>
                )}
                {selected && authState === 'ready' && tabContentSrc(selected.proxyOrigin) && (
                    <iframe
                        // Keyed on the tab so switching swaps the document
                        // instead of navigating inside the previous container.
                        key={selected.id}
                        src={tabContentSrc(selected.proxyOrigin) as string}
                        title={selected.name}
                        className="w-full h-full border-0"
                        referrerPolicy="no-referrer"
                        sandbox="allow-scripts allow-same-origin allow-forms allow-popups allow-downloads"
                    />
                )}
            </main>
        </div>
    );
}
