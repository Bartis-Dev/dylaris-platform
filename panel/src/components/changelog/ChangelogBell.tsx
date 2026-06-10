"use client";

import React, { useCallback, useEffect, useMemo, useState } from 'react';
import { Bell, X } from 'lucide-react';
import {
    ChangelogEntry,
    ChangelogResponse,
    ChangelogType,
    getChangelog,
    markChangelogSeen,
} from '@/lib/api/changelog';
import { systemEvents } from '@/lib/systemEvents';

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

// Color tokens per type — verified against globals.css. `fix` maps to primary
// (steel blue) which has both ghost + light variants.
const TYPE_BADGE: Record<ChangelogType, { bg: string; text: string; label: string }> = {
    feature:     { bg: 'bg-(--success-ghost)', text: 'text-(--success-light)', label: 'FEATURE' },
    fix:         { bg: 'bg-(--primary-ghost)', text: 'text-(--primary-light)', label: 'FIX' },
    breaking:    { bg: 'bg-(--error-ghost)',   text: 'text-(--error-light)',   label: 'BREAKING' },
    improvement: { bg: 'bg-(--accent-ghost)',  text: 'text-(--accent-light)',  label: 'IMPROVEMENT' },
    security:    { bg: 'bg-(--warning-ghost)', text: 'text-(--warning-light)', label: 'SECURITY' },
};

type Tab = 'released' | 'coming_soon';

// ---------------------------------------------------------------------------
// Bell component (entry point + state shell)
// ---------------------------------------------------------------------------

export default function ChangelogBell() {
    const [open, setOpen] = useState(false);
    const [data, setData] = useState<ChangelogResponse | null>(null);
    const [activeTab, setActiveTab] = useState<Tab>('released');
    const [activeSlug, setActiveSlug] = useState<string | null>(null);

    const refresh = useCallback(async () => {
        const res = await getChangelog();
        if (res.success && res.data) {
            setData(res.data);
            // First load: default selection is the newest entry of the visible tab.
            setActiveSlug(prev => {
                if (prev) return prev;
                const list = res.data!.released.length ? res.data!.released : res.data!.comingSoon;
                return list[0]?.slug ?? null;
            });
        }
    }, []);

    // Initial load.
    useEffect(() => { refresh(); }, [refresh]);

    // Listen for `changelog.updated` SSE events so the bell repaints without
    // a panel reload when a new entry ships. The publisher side will be wired
    // in a follow-up; we register the listener now so it's a no-op until then.
    useEffect(() => {
        const off = systemEvents.on('changelog.updated', () => { refresh(); });
        return off;
    }, [refresh]);

    const unreadCount = data?.unreadCount ?? 0;
    const released = data?.released ?? [];
    const comingSoon = data?.comingSoon ?? [];
    const hasComingSoon = comingSoon.length > 0;

    // When the user opens a tab whose list is empty, fall back to the other tab.
    useEffect(() => {
        if (!open) return;
        if (activeTab === 'coming_soon' && !hasComingSoon) {
            setActiveTab('released');
        }
    }, [open, activeTab, hasComingSoon]);

    const currentList = activeTab === 'released' ? released : comingSoon;
    const activeEntry: ChangelogEntry | null =
        currentList.find(e => e.slug === activeSlug) ?? currentList[0] ?? null;

    const lastSeenDateStr = data?.lastSeen ?? null;

    const latestReleasedDateStr = useMemo(() => released[0]?.dateStr ?? null, [released]);

    const handleMarkAllRead = useCallback(async () => {
        if (!latestReleasedDateStr) {
            setOpen(false);
            return;
        }
        await markChangelogSeen(latestReleasedDateStr);
        await refresh();
        setOpen(false);
    }, [latestReleasedDateStr, refresh]);

    return (
        <>
            <button
                type="button"
                onClick={() => setOpen(true)}
                title={unreadCount > 0 ? `${unreadCount} new entr${unreadCount === 1 ? 'y' : 'ies'}` : 'Changelog'}
                className="relative flex items-center justify-center w-9 h-9 rounded-md text-(--base-07) hover:bg-(--base-04)/50 hover:text-(--base-09) transition-colors border border-transparent mr-2"
            >
                <Bell size={20} />
                {unreadCount > 0 && (
                    <span className="absolute -top-1 -right-1 min-w-4 h-4 px-1 rounded-full bg-(--accent) text-white text-[10px] font-mono font-bold flex items-center justify-center leading-none animate-pulse">
                        {unreadCount > 9 ? '9+' : unreadCount}
                    </span>
                )}
            </button>

            {open && (
                <ChangelogDrawer
                    released={released}
                    comingSoon={comingSoon}
                    hasComingSoon={hasComingSoon}
                    activeTab={activeTab}
                    setActiveTab={setActiveTab}
                    activeEntry={activeEntry}
                    setActiveSlug={setActiveSlug}
                    lastSeenDateStr={lastSeenDateStr}
                    unreadCount={unreadCount}
                    onClose={() => setOpen(false)}
                    onMarkAllRead={handleMarkAllRead}
                />
            )}
        </>
    );
}

// ---------------------------------------------------------------------------
// Drawer (separated so the bell stays focused on state)
// ---------------------------------------------------------------------------

interface DrawerProps {
    released: ChangelogEntry[];
    comingSoon: ChangelogEntry[];
    hasComingSoon: boolean;
    activeTab: Tab;
    setActiveTab: (t: Tab) => void;
    activeEntry: ChangelogEntry | null;
    setActiveSlug: (s: string | null) => void;
    lastSeenDateStr: string | null;
    unreadCount: number;
    onClose: () => void;
    onMarkAllRead: () => void;
}

function ChangelogDrawer({
    released,
    comingSoon,
    hasComingSoon,
    activeTab,
    setActiveTab,
    activeEntry,
    setActiveSlug,
    lastSeenDateStr,
    unreadCount,
    onClose,
    onMarkAllRead,
}: DrawerProps) {
    const list = activeTab === 'released' ? released : comingSoon;

    // Trap Escape to close — matches dropdown-style affordances elsewhere.
    useEffect(() => {
        const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') onClose(); };
        document.addEventListener('keydown', onKey);
        return () => document.removeEventListener('keydown', onKey);
    }, [onClose]);

    return (
        <>
            {/* Backdrop */}
            <div
                className="fixed inset-0 bg-black/40 z-40 animate-fade-in"
                onClick={onClose}
            />

            {/* Panel */}
            <div
                className="fixed top-0 right-0 h-screen w-[460px] max-w-[90vw] bg-(--base-01) border-l border-(--base-03) shadow-2xl z-50 flex flex-col"
                role="dialog"
                aria-label="Changelog"
            >
                {/* Header */}
                <div className="h-14 shrink-0 px-4 flex items-center justify-between border-b border-(--base-03)">
                    <div className="flex items-center gap-2">
                        <span className="font-display text-base font-semibold text-(--base-09)">Changelog</span>
                        {unreadCount > 0 && (
                            <span className="px-1.5 h-4 rounded-full bg-(--accent) text-white text-[10px] font-mono font-bold flex items-center justify-center leading-none">
                                {unreadCount > 9 ? '9+' : unreadCount}
                            </span>
                        )}
                    </div>
                    <button
                        type="button"
                        onClick={onClose}
                        className="p-1.5 rounded-md text-(--base-07) hover:bg-(--base-04)/50 hover:text-(--base-09) transition-colors"
                        aria-label="Close changelog"
                    >
                        <X size={18} />
                    </button>
                </div>

                {/* Tabs */}
                <div className="h-10 shrink-0 px-4 flex items-center gap-1 border-b border-(--base-03)">
                    <TabPill
                        active={activeTab === 'released'}
                        onClick={() => { setActiveTab('released'); setActiveSlug(released[0]?.slug ?? null); }}
                    >
                        Released
                    </TabPill>
                    {hasComingSoon && (
                        <TabPill
                            active={activeTab === 'coming_soon'}
                            onClick={() => { setActiveTab('coming_soon'); setActiveSlug(comingSoon[0]?.slug ?? null); }}
                        >
                            Coming soon
                        </TabPill>
                    )}
                </div>

                {/* Body — sidebar + entry detail */}
                <div className="flex-1 flex min-h-0">
                    {/* Sidebar list */}
                    <div className="w-[180px] shrink-0 border-r border-(--base-03) overflow-y-auto">
                        {list.length === 0 ? (
                            <div className="px-3 py-6 text-xs text-(--base-06) text-center">
                                Nothing here yet.
                            </div>
                        ) : (
                            <ul className="py-1">
                                {list.map(entry => {
                                    const isActive = activeEntry?.slug === entry.slug;
                                    const unread = activeTab === 'released' && isEntryUnread(entry.dateStr, lastSeenDateStr);
                                    return (
                                        <li key={entry.slug}>
                                            <button
                                                type="button"
                                                onClick={() => setActiveSlug(entry.slug)}
                                                className={`w-full text-left px-3 py-2 border-l-2 transition-colors ${
                                                    isActive
                                                        ? 'bg-(--accent-ghost) border-(--accent)'
                                                        : 'border-transparent hover:bg-(--base-04)/40'
                                                }`}
                                            >
                                                <div className="font-mono text-[10px] uppercase tracking-wider text-(--base-06)">
                                                    {entry.dateStr}
                                                </div>
                                                <div className="flex items-start gap-1.5 mt-0.5">
                                                    {unread && (
                                                        <span className="mt-1.5 w-1.5 h-1.5 rounded-full bg-(--accent-light) shrink-0" />
                                                    )}
                                                    <span className={`text-sm line-clamp-2 leading-snug ${
                                                        unread ? 'font-semibold text-(--base-09)' : 'text-(--base-08)'
                                                    }`}>
                                                        {entry.title}
                                                    </span>
                                                </div>
                                            </button>
                                        </li>
                                    );
                                })}
                            </ul>
                        )}
                    </div>

                    {/* Detail pane */}
                    <div className="flex-1 overflow-y-auto p-6">
                        {activeEntry ? (
                            <EntryDetail entry={activeEntry} />
                        ) : (
                            <div className="text-sm text-(--base-06) text-center mt-12">
                                Select an entry from the list.
                            </div>
                        )}
                    </div>
                </div>

                {/* Footer */}
                <div className="shrink-0 border-t border-(--base-03) p-3">
                    <button
                        type="button"
                        onClick={onMarkAllRead}
                        disabled={released.length === 0}
                        className="w-full py-2 rounded-md border border-(--base-04) text-sm text-(--base-08) hover:bg-(--base-04)/50 hover:text-(--base-09) transition-colors font-medium disabled:opacity-50 disabled:cursor-not-allowed"
                    >
                        Mark all as read
                    </button>
                </div>
            </div>
        </>
    );
}

// ---------------------------------------------------------------------------
// Subcomponents
// ---------------------------------------------------------------------------

function TabPill({ active, onClick, children }: { active: boolean; onClick: () => void; children: React.ReactNode }) {
    return (
        <button
            type="button"
            onClick={onClick}
            className={`px-3 py-1 rounded-md text-sm transition-colors border ${
                active
                    ? 'bg-(--accent-ghost) text-(--accent-light) border-(--accent-border)'
                    : 'text-(--base-07) hover:bg-(--base-04)/50 hover:text-(--base-09) border-transparent'
            }`}
        >
            {children}
        </button>
    );
}

function EntryDetail({ entry }: { entry: ChangelogEntry }) {
    const badge = TYPE_BADGE[entry.type];
    return (
        <article>
            <div className="flex items-center gap-2 flex-wrap">
                <span className="px-2 py-0.5 rounded-full font-mono text-[10px] uppercase tracking-wider bg-(--base-04) text-(--base-07)">
                    {entry.dateStr}
                </span>
                {badge && (
                    <span className={`px-2 py-0.5 rounded-full font-mono text-[10px] uppercase tracking-wider ${badge.bg} ${badge.text}`}>
                        {badge.label}
                    </span>
                )}
                {entry.audience === 'admin' && (
                    <span className="px-2 py-0.5 rounded-full font-mono text-[10px] uppercase tracking-wider bg-(--accent-dim) text-(--accent-light)">
                        ADMIN ONLY
                    </span>
                )}
            </div>
            <h2 className="font-display text-xl mt-3 text-(--base-09) leading-tight">{entry.title}</h2>
            <div className="mt-4 text-sm text-(--base-08) leading-relaxed">
                <MarkdownBody source={entry.body} />
            </div>
        </article>
    );
}

// ---------------------------------------------------------------------------
// Tiny markdown renderer
//
// Supports just enough to make current entries readable: paragraphs (split
// on blank lines) and **bold**. Lines starting with `- ` are rendered as a
// list because the starter content uses bullet-ish phrasing in places.
// Anything more advanced ships in a follow-up.
// ---------------------------------------------------------------------------

function MarkdownBody({ source }: { source: string }) {
    const blocks = useMemo(() => splitBlocks(source), [source]);
    return (
        <div className="space-y-3">
            {blocks.map((block, i) => {
                if (block.type === 'list') {
                    return (
                        <ul key={i} className="list-disc pl-5 space-y-1">
                            {block.items.map((item, j) => (
                                <li key={j}>{renderInline(item)}</li>
                            ))}
                        </ul>
                    );
                }
                return (
                    <p key={i} className="whitespace-pre-wrap">
                        {renderInline(block.text)}
                    </p>
                );
            })}
        </div>
    );
}

type Block = { type: 'p'; text: string } | { type: 'list'; items: string[] };

function splitBlocks(source: string): Block[] {
    const normalized = source.replace(/\r\n/g, '\n').trim();
    if (!normalized) return [];
    const chunks = normalized.split(/\n\s*\n+/);
    const out: Block[] = [];
    for (const chunk of chunks) {
        const lines = chunk.split('\n');
        const isList = lines.every(l => l.trim().startsWith('- '));
        if (isList && lines.length > 0) {
            out.push({ type: 'list', items: lines.map(l => l.replace(/^\s*-\s+/, '')) });
        } else {
            out.push({ type: 'p', text: chunk });
        }
    }
    return out;
}

function renderInline(text: string): React.ReactNode[] {
    // Split on **bold** while preserving the delimiters. Use a non-greedy
    // match so `**foo** and **bar**` produces two bold spans, not one big one.
    const parts = text.split(/(\*\*[^*]+\*\*)/g);
    return parts.map((part, i) => {
        if (part.startsWith('**') && part.endsWith('**') && part.length >= 4) {
            return <strong key={i} className="text-(--base-09) font-semibold">{part.slice(2, -2)}</strong>;
        }
        return <React.Fragment key={i}>{part}</React.Fragment>;
    });
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function isEntryUnread(entryDateStr: string, lastSeenDateStr: string | null): boolean {
    if (!lastSeenDateStr) return true;
    // Both are YYYY-MM-DD strings — lexicographic compare is identical to
    // date compare, no Date() construction needed.
    return entryDateStr > lastSeenDateStr;
}
