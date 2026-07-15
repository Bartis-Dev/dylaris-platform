"use client";

import React, { useEffect, useState } from 'react';
import { useParams } from 'next/navigation';
import { AlertTriangle, ExternalLink, Link2 } from 'lucide-react';
import { listServerTabs, mintTabProxyAuth, type ServerTab } from '@/lib/api/serverTabs';
import { systemEvents } from '@/lib/systemEvents';
import { tabDashboardProxySrc } from '@/lib/tabProxy';
import { useAppData } from '@/lib/AppDataContext';
import { Skeleton } from '@/components/Skeleton';

// dynamic renderer for custom tabs. Loads the tab metadata, then either:
//  - direct: embeds the configured URL in an iframe (open_in_panel=true) or
//    shows a landing card with a popout button (false).
//  - proxied, surface tab|both: mints the dyl_tabproxy cookie (see
//    core/handlers/tab_proxy.go MintProxyAuth) and then embeds Core's
//    same-origin proxy endpoint - the iframe src never carries a token.
//  - proxied, surface page: not embeddable here, points at the share link.
// Reacts to server_tabs.changed SSE so edits in another tab refresh here
// without reload.

// The dyl_tabproxy ticket cookie Core mints is short-lived (~5min,
// tabProxyTicketTTL server-side); re-mint comfortably inside that window so
// the cookie never expires out from under an in-flight sub-request while the
// tab stays open. The iframe src itself does not need to change on refresh -
// keeping the cookie fresh is enough for its ongoing sub-requests.
const PROXY_AUTH_REFRESH_MS = 4 * 60 * 1000;

type ProxyAuthState = 'pending' | 'ready' | 'error';

export default function ServerCustomTabPage() {
    const params = useParams();
    const serverId = Number(params?.id);
    const tabId = Number(params?.tabId);
    // spec B5: when origin-isolation is active, coreInfo.tabProxyOrigin is the
    // dedicated proxy origin; the builder then emits an absolute src on it. When
    // inactive it is '' and the builder falls back to the relative same-origin
    // path. This page is inside AppDataProvider ((authed)/layout.tsx).
    const { coreInfo } = useAppData();
    const [tab, setTab] = useState<ServerTab | null | undefined>(undefined);
    const [proxyAuth, setProxyAuth] = useState<ProxyAuthState>('pending');
    const [proxyAuthError, setProxyAuthError] = useState<string | null>(null);

    const refresh = async () => {
        const list = await listServerTabs(serverId);
        setTab(list.find(t => t.id === tabId) || null);
    };

    useEffect(() => { refresh(); /* eslint-disable-next-line react-hooks/exhaustive-deps */ }, [serverId, tabId]);

    useEffect(() => {
        const unsub = systemEvents.on('server_tabs.changed', (evt) => {
            const sid = (evt.payload as any)?.serverId;
            if (sid === undefined || sid === serverId) refresh();
        });
        return () => { unsub(); /* eslint-disable-next-line react-hooks/exhaustive-deps */ };
    }, [serverId]);

    // Embeddable-proxied tab: mint the dyl_tabproxy cookie before the iframe
    // is allowed to load, then keep it fresh on an interval for as long as
    // this page stays mounted. Every other branch (not-found/disabled/
    // page-only/direct) leaves isEmbeddedProxy false, so no proxy-auth call
    // is ever made for a tab that isn't actually going to be proxy-embedded.
    const isEmbeddedProxy = !!tab && tab.enabled && tab.mode === 'proxied' && tab.surface !== 'page';
    useEffect(() => {
        if (!isEmbeddedProxy) return;
        let cancelled = false;
        setProxyAuth('pending');
        setProxyAuthError(null);

        // isInitial distinguishes the first mint (before the iframe has ever
        // rendered) from the periodic background refresh. Only the initial
        // mint may flip the UI into the error card - a background refresh
        // that fails (transient network blip, brief backend hiccup) must
        // never tear down an iframe that is already loaded and working, so
        // it just leaves the state alone and lets the next interval tick retry.
        const mint = async (isInitial: boolean) => {
            const res = await mintTabProxyAuth(serverId, tabId);
            if (cancelled) return;
            if (res.success) {
                setProxyAuth('ready');
                return;
            }
            if (!isInitial) return;
            setProxyAuth('error');
            setProxyAuthError(res.message || 'Failed to authorize this tab.');
        };

        mint(true);
        const interval = setInterval(() => mint(false), PROXY_AUTH_REFRESH_MS);
        return () => { cancelled = true; clearInterval(interval); };
    }, [isEmbeddedProxy, serverId, tabId]);

    if (tab === undefined) {
        return (
            <main className="flex-1 overflow-hidden bg-(--base-01) p-4">
                <Skeleton className="w-full h-full rounded" />
            </main>
        );
    }
    if (tab === null) {
        return (
            <main className="flex-1 flex items-center justify-center p-6">
                <div className="card p-6 max-w-md text-center">
                    <AlertTriangle size={20} className="text-(--warning-light) mx-auto mb-2" />
                    <p className="text-sm text-(--base-07)">Tab not found. It may have been deleted.</p>
                </div>
            </main>
        );
    }
    if (!tab.enabled) {
        return (
            <main className="flex-1 flex items-center justify-center p-6">
                <div className="card p-6 max-w-md text-center">
                    <p className="text-sm text-(--base-07)">This tab is disabled.</p>
                </div>
            </main>
        );
    }

    if (tab.mode === 'proxied') {
        if (tab.surface === 'page') {
            // Published as a standalone page only - not embeddable here.
            return (
                <main className="flex-1 flex items-center justify-center p-6">
                    <div className="card p-6 max-w-md text-center space-y-2">
                        <Link2 size={20} className="text-(--accent-light) mx-auto" />
                        <p className="text-sm text-(--base-07)">
                            {tab.name} is published as a standalone page. Open it via its share link.
                        </p>
                    </div>
                </main>
            );
        }

        if (proxyAuth === 'pending') {
            return (
                <main className="flex-1 overflow-hidden bg-(--base-01) p-4">
                    <Skeleton className="w-full h-full rounded" />
                </main>
            );
        }
        if (proxyAuth === 'error') {
            return (
                <main className="flex-1 flex items-center justify-center p-6">
                    <div className="card p-6 max-w-md text-center">
                        <AlertTriangle size={20} className="text-(--warning-light) mx-auto mb-2" />
                        <p className="text-sm text-(--base-07)">{proxyAuthError}</p>
                    </div>
                </main>
            );
        }

        // proxyAuth === 'ready': the dyl_tabproxy cookie is minted and
        // path-scoped to exactly this proxy prefix, so the src below never
        // needs (and never gets) a token of its own.
        return (
            <main className="flex-1 overflow-hidden bg-(--base-01)">
                <iframe
                    src={tabDashboardProxySrc(serverId, tabId, coreInfo?.tabProxyOrigin || undefined)}
                    title={tab.name}
                    className="w-full h-full border-0"
                    referrerPolicy="no-referrer"
                    sandbox="allow-scripts allow-same-origin allow-forms allow-popups allow-downloads"
                />
            </main>
        );
    }

    if (!tab.openInPanel) {
        // Popout-style — surface the link instead of embedding it. Some
        // sites refuse to load in iframes via X-Frame-Options; this gives
        // the operator a clean way to declare that without ugly error pages.
        return (
            <main className="flex-1 flex items-center justify-center p-6">
                <div className="card p-6 max-w-md text-center space-y-3">
                    <p className="text-sm text-(--base-07)">{tab.name} opens in a new window.</p>
                    <a
                        href={tab.url}
                        target="_blank"
                        rel="noopener noreferrer"
                        className="btn btn-primary inline-flex"
                    >
                        <ExternalLink size={13} />
                        Open {tab.name}
                    </a>
                </div>
            </main>
        );
    }

    return (
        <main className="flex-1 overflow-hidden bg-(--base-01)">
            {/* sandbox kept permissive so JS-heavy minimap viewers function;
                referrer-policy keeps URLs from leaking the panel surface */}
            <iframe
                src={tab.url}
                title={tab.name}
                className="w-full h-full border-0"
                referrerPolicy="no-referrer"
                sandbox="allow-scripts allow-same-origin allow-forms allow-popups allow-downloads"
            />
        </main>
    );
}
