"use client";

import React, { useState, useEffect, useRef, useCallback } from 'react';
import { Sparkles, X } from 'lucide-react';
import { useAppData } from '@/lib/AppDataContext';
import { getUpdates, markUpdatesSeen, UpdateEntry, UpdateServiceBlock } from '@/lib/api/updates';
import { bellState, splitLatest, type UpdateGroup } from '@/lib/updateGroups';

// ---------------------------------------------------------------------------
// Admin-only "What's new". Shows the platform changelog (always) and the
// gateway changelog (only when gateway routing is enabled), flagging entries
// published since this build with an unseen badge. Regular users never see it.
// The feeds are empty until go-live, so this renders an "up to date" state
// until the append-only feeds start being filled.
//
// A modal, not a dropdown: the newest update gets a section of its own at the
// top and the history sits beneath it, which needs more width and vertical room
// than a navbar popover has.
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

// One changelog line. `service` is shown because a single date can carry core,
// panel and node changes and "which part changed" is the first thing an operator
// wants from a changelog.
function EntryRow({ entry }: { entry: UpdateEntry }) {
    const s = typeStyle(entry.type);
    return (
        <div className="flex items-start gap-3 px-4 py-2.5 hover:bg-(--base-03)/60 transition-colors">
            <span className={`shrink-0 mt-0.5 w-16 font-mono text-[9px] uppercase tracking-[0.06em] ${s.color}`}>
                {s.label}
            </span>
            <div className="min-w-0 flex-1">
                <div className="text-sm text-(--base-08) leading-snug">{entry.summary}</div>
                {entry.service && (
                    <div className="text-[10px] font-mono text-(--base-06) mt-0.5">{entry.service}</div>
                )}
            </div>
        </div>
    );
}

// A date's worth of entries. `heading` overrides the date label for the
// top-of-modal "latest" block.
function DateGroup({ group, heading }: { group: UpdateGroup<UpdateEntry>; heading?: string }) {
    return (
        <div>
            <div className="px-4 py-1.5 flex items-baseline justify-between gap-3 bg-(--base-02) border-y border-(--base-03)">
                <span className="font-mono text-[9px] uppercase tracking-[0.08em] text-(--base-06)">
                    {heading ?? (group.date || 'Undated')}
                </span>
                {heading && group.date && (
                    <span className="font-mono text-[10px] text-(--base-07)">{group.date}</span>
                )}
            </div>
            <div className="divide-y divide-(--base-03)/50">
                {group.entries.map((e, i) => <EntryRow key={`${group.date}-${i}`} entry={e} />)}
            </div>
        </div>
    );
}

// One service's slice of the modal: its latest date first, then the history.
function ServiceSection({ title, block }: { title: string; block: UpdateServiceBlock | undefined }) {
    const entries: UpdateEntry[] = block?.newEntries ?? [];
    if (entries.length === 0) return null;
    const { latest, earlier } = splitLatest(entries);
    return (
        <section>
            <div className="px-4 pt-4 pb-2 flex items-center justify-between gap-3">
                <h3 className="text-sm font-display font-semibold text-(--base-09)">{title}</h3>
                {block?.updateAvailable && (
                    <span className="badge badge-accent">update available</span>
                )}
            </div>
            {latest && <DateGroup group={latest} heading="Latest update" />}
            {earlier.length > 0 && (
                <>
                    <div className="px-4 pt-4 pb-1 font-mono text-[9px] uppercase tracking-[0.08em] text-(--base-05)">
                        Earlier
                    </div>
                    {earlier.map(g => <DateGroup key={g.date} group={g} />)}
                </>
            )}
        </section>
    );
}

export default function UpdatesBell() {
    const { user } = useAppData();
    const isAdmin = user?.isAdmin ?? false;

    const [open, setOpen] = useState(false);
    const [unseen, setUnseen] = useState(0);
    const [platform, setPlatform] = useState<UpdateServiceBlock | undefined>(undefined);
    const [gateway, setGateway] = useState<UpdateServiceBlock | undefined>(undefined);
    const closeRef = useRef<HTMLButtonElement>(null);

    // Fetch the feed delta. Admin-only endpoint (server enforces); the response
    // is small and server-side cached (15 min), so a light poll keeps the badge
    // fresh without adding load.
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

    // Escape closes, and focus moves into the dialog on open so it is reachable
    // from the keyboard like the other modals.
    useEffect(() => {
        if (!open) return;
        const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') setOpen(false); };
        document.addEventListener('keydown', onKey);
        closeRef.current?.focus();
        return () => document.removeEventListener('keydown', onKey);
    }, [open]);

    if (!isAdmin) return null;

    const entryCount = (platform?.newEntries?.length ?? 0) + (gateway?.newEntries?.length ?? 0);
    const state = bellState(unseen, entryCount);

    // Opening acknowledges the current feeds so the badge clears (optimistic).
    // The icon then drops from 'new' to 'unread' rather than to grey: there is
    // still something behind it, and rendering it as empty made the button look
    // dead the moment it was used.
    const openModal = () => {
        setOpen(true);
        if (unseen > 0) {
            setUnseen(0);
            markUpdatesSeen().catch(() => {});
        }
    };

    const buttonClass =
        state === 'new'
            ? 'bg-(--accent-ghost) text-(--accent-light) border-(--accent-border) hover:bg-(--accent-ghost)/80'
            : state === 'unread'
                ? 'text-(--accent-light) border-(--accent-border) hover:bg-(--accent-ghost)/40'
                : 'text-(--base-07) hover:bg-(--base-04)/50 hover:text-(--base-09) border-transparent';

    const title =
        state === 'new'
            ? `${unseen} new update${unseen === 1 ? '' : 's'}`
            : state === 'unread'
                ? "What's new"
                : "What's new - you're up to date";

    return (
        <>
            <button
                type="button"
                onClick={openModal}
                title={title}
                aria-label={title}
                className={`relative flex items-center justify-center w-9 h-9 rounded-md transition-colors border mr-2 ${buttonClass}`}
            >
                <Sparkles size={18} />
                {state === 'new' && (
                    <span className="absolute -top-1 -right-1 min-w-[16px] h-[16px] px-1 rounded-full bg-(--accent-light) text-(--base-00) text-[10px] font-mono font-bold flex items-center justify-center leading-none">
                        {unseen > 9 ? '9+' : unseen}
                    </span>
                )}
            </button>

            {open && (
                <div
                    className="modal-overlay animate-fade-in z-50"
                    onClick={e => { if (e.target === e.currentTarget) setOpen(false); }}
                >
                    <div className="modal-panel w-full max-w-lg" role="dialog" aria-modal="true" aria-label="What's new">
                        <div className="flex items-center justify-between px-4 py-3 border-b border-(--base-03)">
                            <div className="flex items-center gap-2">
                                <Sparkles size={15} className="text-(--accent-light)" />
                                <span className="text-sm font-display font-semibold text-(--base-09)">What&apos;s new</span>
                            </div>
                            <button
                                ref={closeRef}
                                type="button"
                                onClick={() => setOpen(false)}
                                className="text-(--base-06) hover:text-(--base-09) p-1 rounded-md hover:bg-(--base-03) transition-colors"
                                aria-label="Close"
                            >
                                <X size={16} />
                            </button>
                        </div>

                        {entryCount > 0 ? (
                            <div className="max-h-[70vh] overflow-y-auto pb-3">
                                <ServiceSection title="Platform" block={platform} />
                                <ServiceSection title="Gateway" block={gateway} />
                            </div>
                        ) : (
                            <div className="px-4 py-10 text-sm text-(--base-06) text-center">
                                You&apos;re up to date.
                            </div>
                        )}
                    </div>
                </div>
            )}
        </>
    );
}
