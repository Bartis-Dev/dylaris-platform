"use client";

import React, { useEffect, useState } from 'react';
import { useParams } from 'next/navigation';
import { AlertTriangle, ExternalLink } from 'lucide-react';
import { listServerTabs, type ServerTab } from '@/lib/api/serverTabs';
import { systemEvents } from '@/lib/systemEvents';
import { Skeleton } from '@/components/Skeleton';

// dynamic renderer for custom tabs. Loads the tab metadata, then
// embeds the configured URL in an iframe (open_in_panel=true) or shows a
// landing card with a popout button (false). Reacts to server_tabs.changed
// SSE so URL edits in another tab refresh here without reload.

export default function ServerCustomTabPage() {
    const params = useParams();
    const serverId = Number(params?.id);
    const tabId = Number(params?.tabId);
    const [tab, setTab] = useState<ServerTab | null | undefined>(undefined);

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
