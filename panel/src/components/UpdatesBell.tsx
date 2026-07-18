"use client";

import React, { useState, useEffect, useRef, useCallback } from 'react';
import { Sparkles } from 'lucide-react';
import { useAppData } from '@/lib/AppDataContext';
import { getUpdates, markUpdatesSeen, UpdateEntry, UpdateServiceBlock } from '@/lib/api/updates';

// ---------------------------------------------------------------------------
// Admin-only "What's new" bell. Shows the platform changelog (always) and the
// gateway changelog (only when gateway routing is enabled), flagging entries
// published since this build with an unseen badge. Regular users never see it.
// The feeds are empty until go-live, so this renders an "up to date" state
// until the append-only feeds start being filled.
// ---------------------------------------------------------------------------

// Per-change-type accent, matching the "Carbon Steel x Plasma Violet" tokens.
const TYPE_STYLES: Record<string, { label: string; color: string }> = {
    feature: { label: 'Feature', color: 'text-(--accent-light)' },
    fix: { label: 'Fix', color: 'text-(--success-light)' },
    change: { label: 'Change', color: 'text-(--info)' },
    security: { label: 'Security', color: 'text-(--error-light)' },
};

function typeStyle(t: string) {
    return TYPE_STYLES[t] || { label: t || 'Update', color: 'text-(--base-07)' };
}

// A titled group of changelog entries for one service.
function ServiceSection({ title, block }: { title: string; block: UpdateServiceBlock | undefined }) {
    const entries: UpdateEntry[] = block?.newEntries ?? [];
    if (entries.length === 0) return null;
    return (
        <div className="border-b border-(--base-03) last:border-b-0">
            <div className="px-4 py-1.5 text-[9px] font-mono uppercase tracking-[0.08em] text-(--base-06) bg-(--base-02) flex items-center justify-between">
                <span>{title}</span>
                {block?.updateAvailable && (
                    <span className="text-(--accent-light) normal-case tracking-normal">update available</span>
                )}
            </div>
            <div className="py-1">
                {entries.map((e, i) => {
                    const s = typeStyle(e.type);
                    return (
                        <div key={`${e.date}-${i}`} className="px-3 py-2 hover:bg-(--base-03) transition-colors">
                            <div className="flex items-start gap-2">
                                <span className={`shrink-0 mt-0.5 font-mono text-[9px] uppercase tracking-[0.06em] ${s.color}`}>
                                    {s.label}
                                </span>
                                <div className="min-w-0 flex-1">
                                    <div className="text-sm text-(--base-08) leading-snug">{e.summary}</div>
                                    {e.date && <div className="text-[10px] font-mono text-(--base-06) mt-0.5">{e.date}</div>}
                                </div>
                            </div>
                        </div>
                    );
                })}
            </div>
        </div>
    );
}

export default function UpdatesBell() {
    const { user } = useAppData();
    const isAdmin = user?.isAdmin ?? false;

    const [open, setOpen] = useState(false);
    const [unseen, setUnseen] = useState(0);
    const [platform, setPlatform] = useState<UpdateServiceBlock | undefined>(undefined);
    const [gateway, setGateway] = useState<UpdateServiceBlock | undefined>(undefined);
    const wrapRef = useRef<HTMLDivElement>(null);

    // Fetch the feed delta. Admin-only endpoint (server enforces); the response
    // is small and server-side cached, so a light poll keeps the badge fresh.
    const refresh = useCallback(async () => {
        if (!isAdmin) return;
        const res = await getUpdates();
        if (res.success) {
            setUnseen(res.unseen || 0);
            setPlatform(res.platform);
            setGateway(res.gateway);
        }
    }, [isAdmin]);

    useEffect(() => {
        if (!isAdmin) return;
        refresh();
        const t = setInterval(refresh, 60000);
        return () => clearInterval(t);
    }, [isAdmin, refresh]);

    // Click-outside closes the dropdown, matching the notifications bell.
    useEffect(() => {
        const handler = (e: MouseEvent) => {
            if (!wrapRef.current?.contains(e.target as Node)) setOpen(false);
        };
        document.addEventListener('click', handler);
        return () => document.removeEventListener('click', handler);
    }, []);

    // Opening acknowledges the current feeds so the badge clears (optimistic).
    const toggle = () => {
        const next = !open;
        setOpen(next);
        if (next && unseen > 0) {
            setUnseen(0);
            markUpdatesSeen().catch(() => {});
        }
    };

    if (!isAdmin) return null;

    const hasUnseen = unseen > 0;
    const hasEntries = (platform?.newEntries?.length ?? 0) > 0 || (gateway?.newEntries?.length ?? 0) > 0;

    return (
        <div ref={wrapRef} className="relative mr-2">
            <button
                type="button"
                onClick={toggle}
                title={hasUnseen ? `${unseen} new update${unseen === 1 ? '' : 's'}` : "What's new"}
                className={`relative flex items-center justify-center w-9 h-9 rounded-md transition-colors border ${
                    open
                        ? 'bg-(--base-03) border-(--base-04) text-(--base-09)'
                        : hasUnseen
                            ? 'bg-(--accent-ghost) text-(--accent-light) border-(--accent-border) hover:bg-(--accent-ghost)/80'
                            : 'text-(--base-07) hover:bg-(--base-04)/50 hover:text-(--base-09) border-transparent'
                }`}
            >
                <Sparkles size={18} />
                {hasUnseen && (
                    <span className="absolute -top-1 -right-1 min-w-[16px] h-[16px] px-1 rounded-full bg-(--accent-light) text-(--base-00) text-[10px] font-mono font-bold flex items-center justify-center leading-none">
                        {unseen > 9 ? '9+' : unseen}
                    </span>
                )}
            </button>

            {open && (
                <div className="dropdown-menu right-0 mt-3 w-96 animate-fade-in origin-top-right">
                    <div className="px-4 py-2 border-b border-(--base-03)">
                        <div className="font-mono text-[10px] uppercase tracking-[0.08em] text-(--base-06)">
                            What&apos;s new
                        </div>
                    </div>

                    {hasEntries ? (
                        <div className="max-h-96 overflow-y-auto">
                            <ServiceSection title="Platform" block={platform} />
                            <ServiceSection title="Gateway" block={gateway} />
                        </div>
                    ) : (
                        <div className="px-4 py-6 text-sm text-(--base-06) text-center">
                            You&apos;re up to date.
                        </div>
                    )}
                </div>
            )}
        </div>
    );
}
