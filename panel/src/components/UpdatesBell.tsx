"use client";

import React, { useState, useEffect, useRef, useCallback } from 'react';
import { Sparkles, X, AlertTriangle } from 'lucide-react';
import { useAppData } from '@/lib/AppDataContext';
import { getUpdates, markUpdatesSeen, UpdateEntry, UpdateServiceBlock, PerServiceBlock } from '@/lib/api/updates';
import {
    bellState, splitLatestByService, serviceLabel, breakingCount, typeCounts,
    type ServiceGroup,
} from '@/lib/updateGroups';

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
//
// `breaking` is the odd one out and deliberately loud: every other type
// describes what changed, this one says the update does not finish applying
// itself and the operator has to do something. It gets a border and a callout,
// not just a colour, so it survives being skimmed.
const TYPE_STYLES: Record<string, { label: string; color: string; dot: string }> = {
    breaking: { label: 'Breaking', color: 'text-(--error-light)', dot: 'bg-(--error)' },
    security: { label: 'Security', color: 'text-(--error-light)', dot: 'bg-(--error-light)' },
    feature: { label: 'Feature', color: 'text-(--accent-light)', dot: 'bg-(--accent)' },
    fix: { label: 'Fix', color: 'text-(--success-light)', dot: 'bg-(--success-light)' },
    change: { label: 'Change', color: 'text-(--info)', dot: 'bg-(--info)' },
};

function typeStyle(t: string) {
    const key = (t || '').trim().toLowerCase();
    return TYPE_STYLES[key] || { label: t || 'Update', color: 'text-(--base-07)', dot: 'bg-(--base-05)' };
}

function isBreaking(entry: UpdateEntry) {
    return (entry.type || '').trim().toLowerCase() === 'breaking';
}

// One changelog line, as a bullet. `service` is shown because a single date can
// carry core, panel and node changes and "which part changed" is the first thing
// an operator wants from a changelog.
function EntryRow({ entry }: { entry: UpdateEntry }) {
    const s = typeStyle(entry.type);
    const breaking = isBreaking(entry);
    return (
        <li className={`flex items-start gap-3 px-4 py-2 ${
            breaking ? 'bg-(--error-ghost)/40 border-l-2 border-(--error)' : ''
        }`}>
            <span className={`shrink-0 mt-[7px] w-1.5 h-1.5 rounded-full ${s.dot}`} aria-hidden="true" />
            <div className="min-w-0 flex-1">
                <div className="text-sm text-(--base-08) leading-relaxed">
                    <span className={`font-mono text-[10px] uppercase tracking-[0.06em] mr-2 ${s.color}`}>
                        {s.label}
                    </span>
                    {entry.summary}
                </div>
                {/* The service is the group heading now, so the only per-line
                    metadata left is when it landed - quiet, and titled so the
                    full value is there on hover. */}
                <div className="flex items-center gap-2 mt-0.5">
                    {entry.date && (
                        <span className="text-[10px] font-mono text-(--base-05)" title={`Published ${entry.date}`}>
                            {entry.date}
                        </span>
                    )}
                    {breaking && (
                        <span className="text-[10px] font-mono text-(--error-light)">action required</span>
                    )}
                </div>
            </div>
        </li>
    );
}

// One component's entries under its own heading. This is the unit the modal
// groups by: the feed is one line per change, so a single day arrives
// interleaved across core, panel and node, and listing it in arrival order gave
// "Core, Panel, Core, Panel". Someone reading a changelog wants everything one
// component did, once.
function ServiceBlockGroup({ group }: { group: ServiceGroup<UpdateEntry> }) {
    return (
        <div>
            <div className="px-4 py-1.5 flex items-baseline justify-between gap-3 bg-(--base-02) border-y border-(--base-03)">
                <span className="font-mono text-[10px] uppercase tracking-[0.08em] text-(--base-07)">
                    {serviceLabel(group.service)}
                </span>
                <span className="font-mono text-[9px] text-(--base-05)">
                    {group.entries.length} change{group.entries.length === 1 ? '' : 's'}
                </span>
            </div>
            <ul className="divide-y divide-(--base-03)/50">
                {group.entries.map((e, i) => <EntryRow key={`${group.service}-${i}`} entry={e} />)}
            </ul>
        </div>
    );
}

// The header of the newest date: what it contains by type, and - when anything
// in it is breaking - a callout the operator has to read before updating. This
// is the "last update" block the modal leads with.
function LatestHeader({ entries, date }: { entries: UpdateEntry[]; date: string }) {
    const counts = typeCounts(entries);
    const breaking = breakingCount(entries);
    return (
        <div className="px-4 pt-4 pb-3 border-b border-(--base-03)">
            <div className="flex items-baseline justify-between gap-3 flex-wrap">
                <h4 className="text-base font-display font-semibold text-(--base-09)">Latest update</h4>
                <span className="font-mono text-xs text-(--base-07)">{date || 'Undated'}</span>
            </div>
            <div className="flex items-center gap-2 flex-wrap mt-2">
                {counts.map(c => {
                    const s = typeStyle(c.type);
                    return (
                        <span
                            key={c.type}
                            className="inline-flex items-center gap-1.5 px-2 py-0.5 rounded-md bg-(--base-03) border border-(--base-04) text-[11px]"
                        >
                            <span className={`w-1.5 h-1.5 rounded-full ${s.dot}`} aria-hidden="true" />
                            <span className="text-(--base-08)">
                                {c.count} {s.label.toLowerCase()}{c.count === 1 ? '' : 's'}
                            </span>
                        </span>
                    );
                })}
            </div>
            {breaking > 0 && (
                <div className="mt-3 flex items-start gap-2 px-3 py-2 rounded-md bg-(--error-ghost) border border-(--error)/40">
                    <AlertTriangle size={14} className="shrink-0 mt-0.5 text-(--error-light)" />
                    <p className="text-xs text-(--base-08) leading-snug">
                        <span className="font-medium text-(--error-light)">
                            {breaking} breaking change{breaking === 1 ? '' : 's'} in this update.
                        </span>
                        {' '}Applying it needs action from you. The entries marked below say what; read them
                        before you deploy.
                    </p>
                </div>
            )}
        </div>
    );
}

// Per-component standing, when the components reported one. A component that
// never did is shown as unknown rather than as up to date: assuming it moved
// with Core is the exact mistake this replaces.
function ServiceStanding({ blocks }: { blocks: PerServiceBlock[] }) {
    if (blocks.length === 0) return null;
    return (
        <div className="px-4 py-3 border-b border-(--base-03) flex flex-wrap gap-2">
            {blocks.map(b => {
                const behind = b.behind > 0;
                return (
                    <span
                        key={b.service}
                        title={b.baselineKnown
                            ? `${serviceLabel(b.service)} was built at feed entry ${b.installedCount}`
                            : `${serviceLabel(b.service)} does not report which build it runs; measured against Core instead`}
                        className={`inline-flex items-center gap-1.5 px-2 py-0.5 rounded-md border text-[11px] ${
                            behind
                                ? 'bg-(--warning-ghost) border-(--warning)/40 text-(--warning-light)'
                                : 'bg-(--base-03) border-(--base-04) text-(--base-07)'
                        }`}
                    >
                        <span className="font-medium">{serviceLabel(b.service)}</span>
                        <span>
                            {behind ? `${b.behind} pending` : 'up to date'}
                        </span>
                        {!b.baselineKnown && <span className="text-(--base-05)">(assumed)</span>}
                    </span>
                );
            })}
        </div>
    );
}

// One FEED's slice of the modal: the newest date grouped by component, then the
// whole history grouped by component.
function ServiceSection({ title, block }: { title: string; block: UpdateServiceBlock | undefined }) {
    const entries: UpdateEntry[] = block?.newEntries ?? [];
    if (entries.length === 0) return null;
    const { latestDate, latest, earlier } = splitLatestByService(entries);
    return (
        <section>
            <div className="px-4 pt-4 pb-2 flex items-center justify-between gap-3">
                <h3 className="text-sm font-display font-semibold text-(--base-09)">{title}</h3>
                {block?.updateAvailable && (
                    <span className="badge badge-accent">update available</span>
                )}
            </div>
            <ServiceStanding blocks={block?.perService ?? []} />
            {latest.length > 0 && (
                <>
                    <LatestHeader entries={latest.flatMap(g => g.entries)} date={latestDate} />
                    {latest.map(g => <ServiceBlockGroup key={`latest-${g.service}`} group={g} />)}
                </>
            )}
            {earlier.length > 0 && (
                <>
                    <div className="px-4 pt-4 pb-1 font-mono text-[9px] uppercase tracking-[0.08em] text-(--base-05)">
                        Earlier updates
                    </div>
                    {earlier.map(g => <ServiceBlockGroup key={`earlier-${g.service}`} group={g} />)}
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
                    <div className="modal-panel w-full max-w-3xl" role="dialog" aria-modal="true" aria-label="What's new">
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
                            <div className="max-h-[75vh] overflow-y-auto pb-4">
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
