"use client";

import { useEffect, useState } from 'react';
import { useParams } from 'next/navigation';
import { AlertTriangle } from 'lucide-react';
import { mintTabProxyAuth, resolveShareLink } from '@/lib/api/serverTabs';
import { tabContentSrc } from '@/lib/tabProxy';

// Standalone, Dylaris-branded page for a shared custom tab. Lives OUTSIDE the
// (authed) group so a public link loads without the panel's auth redirect.
//
// This page and the tab it frames are DIFFERENT ORIGINS on purpose. The page is
// ours; the frame is a tenant's container. Same-origin would let that container
// reach parent.document and rewrite the header below - swapping the link back to
// the panel for a phishing target, say - which is a smaller hole than reading
// the session token but the same kind of hole.
//
// The flow is deliberately short:
//
//   1. resolve the token to its content host, and whether a ticket is needed
//   2. if one is, try to mint it; a visitor who is signed in gets a working
//      frame, one who is not gets Core's own "open it from the panel" card
//      INSIDE the frame
//   3. render the frame either way
//
// Step 2 does not branch on the outcome, and that is the point. This page
// cannot tell "not signed in" from "signed in but holds no ticket for this tab
// yet" - it can read neither the panel's localStorage nor a cookie that was
// never set - so it does not try. Core answers on the content host, where both
// are knowable.

// The ticket is short-lived (~5min server-side); re-mint comfortably inside
// that window so it never lapses under a frame that is still open.
const PROXY_AUTH_REFRESH_MS = 4 * 60 * 1000;

type State = 'checking' | 'ready' | 'notfound' | 'expired';

export default function StandaloneTabProxyPage() {
    const params = useParams();
    const token = String(params?.token || '');
    const [state, setState] = useState<State>('checking');
    const [contentOrigin, setContentOrigin] = useState('');

    useEffect(() => {
        let cancelled = false;
        let refresh: ReturnType<typeof setInterval> | undefined;

        (async () => {
            const res = await resolveShareLink(token);
            if (cancelled) return;
            if (!res.success || !res.data?.contentOrigin) {
                setState(res.status === 410 ? 'expired' : 'notfound');
                return;
            }
            setContentOrigin(res.data.contentOrigin);

            if (res.data.requiresAuth) {
                // Best effort, and unconditional afterwards. A signed-in
                // visitor gets content; anyone else gets Core's card in the
                // frame, which is the same answer this page would have had to
                // render anyway, only from the side that can actually tell.
                await mintTabProxyAuth(res.data.contentOrigin);
                if (cancelled) return;
                refresh = setInterval(() => { mintTabProxyAuth(res.data!.contentOrigin); }, PROXY_AUTH_REFRESH_MS);
            }
            setState('ready');
        })();

        return () => {
            cancelled = true;
            if (refresh) clearInterval(refresh);
        };
    }, [token]);

    const iframeSrc = tabContentSrc(contentOrigin);

    return (
        <div className="flex flex-col h-screen bg-(--base-00) text-(--base-09) font-body overflow-hidden">
            <header className="shrink-0 h-12 bg-(--base-01) border-b border-(--base-03) flex items-center px-4">
                <a
                    href="/"
                    className="px-3 py-0.5 rounded-md bg-(--accent-dim) border border-(--accent-border) inline-flex items-center transition-colors hover:border-(--accent-light) focus-visible:outline-hidden focus-visible:shadow-(--focus-ring)"
                >
                    <span className="text-xl font-logo tracking-widest select-none">
                        <span className="text-(--accent-light)">D</span>
                        <span className="text-(--base-09)">ylaris</span>
                    </span>
                </a>
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
                {state === 'ready' && iframeSrc && (
                    <iframe
                        src={iframeSrc}
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
