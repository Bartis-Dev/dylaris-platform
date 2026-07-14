"use client";

import React, { useEffect, useState } from 'react';
import { useParams, useRouter } from 'next/navigation';
import { AlertTriangle, Lock } from 'lucide-react';
import { API_URL } from '@/lib/api/core';
import { mintPublicTabProxyAuth } from '@/lib/api/serverTabs';
import { tabProxyPageSrc } from '@/lib/tabProxy';

// Standalone, Dylaris-branded page for a proxied custom tab, reachable by an
// unguessable share link. Lives OUTSIDE the (authed) group so a public link
// loads without the panel's client-side auth redirect (see (authed)/layout.tsx).
//
// Auth is preflight-first (WS5 Task 14): the page must never mint a
// session-authed ticket before knowing whether this visitor even needs one -
// minting eagerly would 401 an anonymous visitor of a PUBLIC link, which
// never needs a cookie at all.
//
//   1. GET {API_URL}/tabproxy/{token}/ with credentials:'include' (send any
//      existing dyl_tabproxy cookie; this root-router endpoint never checks a
//      Bearer - see core/handlers/tab_proxy.go Public). Only the status is
//      read - the body is cancelled immediately so a 2xx preflight never
//      downloads the full proxied page twice (once here, once in the iframe).
//        2xx -> ready (public link served anon, or a private link that
//               already carries a valid, unexpired cookie).
//        401 -> private link, no/stale cookie: mint one via the
//               session-authed .../auth endpoint (MintPublicProxyAuth). That
//               mint's own outcome decides what happens next:
//                 204  -> ready; re-mint every ~4 min (the ticket TTL is
//                         ~5 min) for as long as this page stays mounted.
//                 401  -> no/invalid panel session: stash postLoginRedirect
//                         and bounce to /login (mirrors the pattern used by
//                         (authed)/layout.tsx and the /login page itself).
//                 403  -> logged in, but no access to this tab's server.
//                 other -> treat the link as invalid.
//        403 -> logged in (existing cookie) but wrong scope - no access.
//        410 -> the share link has expired.
//        404/other -> not a valid link.
//
// The iframe src is always token-less (tabProxyPageSrc(token) alone, per
// Task 11) - no session credential is ever carried in a URL.

type State = 'checking' | 'ready' | 'notfound' | 'expired' | 'forbidden';

// Mirrors the in-dashboard tab page's own refresh cadence
// ((authed)/servers/[id]/t/[tabId]/page.tsx PROXY_AUTH_REFRESH_MS): the
// dyl_tabproxy ticket cookie is short-lived (~5min server-side, see
// tabProxyTicketTTL), so re-mint comfortably inside that window rather than
// waiting for it to lapse mid-session.
const PROXY_AUTH_REFRESH_MS = 4 * 60 * 1000;

export default function StandaloneTabProxyPage() {
    const params = useParams();
    const router = useRouter();
    const token = String(params?.token || '');
    const [state, setState] = useState<State>('checking');

    useEffect(() => {
        let cancelled = false;
        let refreshInterval: ReturnType<typeof setInterval> | undefined;

        // authorize applies the same 401/403/other classification whether it
        // runs right after the initial preflight's 401 or from the periodic
        // refresh tick, so a session that lapses mid-view degrades exactly
        // the same way a first visit without one would.
        const authorize = async () => {
            const mint = await mintPublicTabProxyAuth(token);
            if (cancelled) return;
            if (mint.success) {
                setState('ready');
                return;
            }
            if (mint.status === 401) {
                sessionStorage.setItem('postLoginRedirect', `/c/${token}`);
                router.push('/login');
                return;
            }
            if (mint.status === 403) { setState('forbidden'); return; }
            setState('notfound');
        };

        (async () => {
            try {
                const res = await fetch(`${API_URL}/tabproxy/${encodeURIComponent(token)}/`, {
                    method: 'GET',
                    credentials: 'include',
                });
                // Preflight only needs the status - never let the browser pull
                // down the (potentially large) proxied body just to check it.
                res.body?.cancel()?.catch(() => {});
                if (cancelled) return;

                if (res.ok) { setState('ready'); return; }
                if (res.status === 410) { setState('expired'); return; }
                if (res.status === 403) { setState('forbidden'); return; }
                if (res.status === 401) {
                    await authorize();
                    if (cancelled) return;
                    // Only the FIRST mint gets here (a failed one already
                    // redirected or showed an error card - nothing left to
                    // keep fresh), so the interval is armed exactly once.
                    refreshInterval = setInterval(authorize, PROXY_AUTH_REFRESH_MS);
                    return;
                }
                setState('notfound');
            } catch {
                if (!cancelled) setState('notfound');
            }
        })();

        return () => {
            cancelled = true;
            if (refreshInterval) clearInterval(refreshInterval);
        };
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [token, router]);

    return (
        <div className="flex flex-col h-screen bg-(--base-00) text-(--base-09) font-body overflow-hidden">
            <header className="shrink-0 h-12 bg-(--base-01) border-b border-(--base-03) flex items-center px-4">
                <div className="px-3 py-0.5 rounded-md bg-(--accent-dim) border border-(--accent-border) inline-flex items-center">
                    <span className="text-xl font-logo tracking-widest select-none">
                        <span className="text-(--accent-light)">D</span>
                        <span className="text-(--base-09)">ylaris</span>
                    </span>
                </div>
                <span className="ml-3 text-xs text-(--base-06)">running through your Dylaris panel</span>
            </header>
            <main className="flex-1 overflow-hidden bg-(--base-01)">
                {state === 'checking' && (
                    <div className="h-full flex items-center justify-center text-(--base-06) text-sm">Loading...</div>
                )}
                {state === 'notfound' && (
                    <div className="h-full flex items-center justify-center p-6">
                        <div className="card p-6 max-w-md text-center">
                            <AlertTriangle size={20} className="text-(--warning-light) mx-auto mb-2" />
                            <p className="text-sm text-(--base-07)">This link is not valid.</p>
                        </div>
                    </div>
                )}
                {state === 'expired' && (
                    <div className="h-full flex items-center justify-center p-6">
                        <div className="card p-6 max-w-md text-center">
                            <AlertTriangle size={20} className="text-(--warning-light) mx-auto mb-2" />
                            <p className="text-sm text-(--base-07)">This share link has expired.</p>
                        </div>
                    </div>
                )}
                {state === 'forbidden' && (
                    <div className="h-full flex items-center justify-center p-6">
                        <div className="card p-6 max-w-md text-center">
                            <Lock size={20} className="text-(--warning-light) mx-auto mb-2" />
                            <p className="text-sm text-(--base-07)">You do not have access to this link.</p>
                        </div>
                    </div>
                )}
                {state === 'ready' && (
                    <iframe
                        src={tabProxyPageSrc(token)}
                        title="Dylaris tab"
                        className="w-full h-full border-0"
                        referrerPolicy="no-referrer"
                        sandbox="allow-scripts allow-same-origin allow-forms allow-popups allow-downloads"
                    />
                )}
            </main>
        </div>
    );
}
