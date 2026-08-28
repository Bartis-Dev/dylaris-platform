"use client";

import { useState, useEffect, useRef, useCallback } from 'react';
import { Sparkles, X, AlertTriangle, RefreshCw } from 'lucide-react';
import { useAppData } from '@/lib/AppDataContext';
import {
    getUpdates, markUpdatesSeen,
    type Release, type UpdateComponent, type UpdateRequirement,
} from '@/lib/api/updates';
import {
    groupInstances, serviceLabel, anythingOutdated, bellState, categories,
    formatDeadline, type VersionGroup,
} from '@/lib/updateGroups';

// ---------------------------------------------------------------------------
// The updates view. Two questions, in this order:
//
//   1. Which of MY components are behind, and by how much
//   2. What changed
//
// The first is why anyone opens this, so it sits at the top. It used to be
// absent entirely: the old view listed changelog entries and left the reader to
// work out whether any of it applied to their deployment.
//
// Everyone sees it, not just admins. An admin runs the platform and sees the
// platform notes plus every component; a BYON customer sees the customer notes
// plus their own nodes, and used to see nothing at all - despite being the one
// reader with hardware to update.
// ---------------------------------------------------------------------------

// How often the view refreshes itself while open or mounted. Six hours, because
// releases are a human-paced event and Core caches the notes for fifteen minutes
// anyway; the button next to it exists for the case where six hours is too long.
const REFRESH_MS = 6 * 60 * 60 * 1000;

const CATEGORY_STYLES: Record<string, { color: string; dot: string }> = {
    breaking: { color: 'text-(--error-light)', dot: 'bg-(--error)' },
    security: { color: 'text-(--error-light)', dot: 'bg-(--error-light)' },
    features: { color: 'text-(--accent-light)', dot: 'bg-(--accent)' },
    fixes: { color: 'text-(--success-light)', dot: 'bg-(--success-light)' },
};

// One version row inside a component: "2/3 on 2026.08.28".
//
// The count is written as a fraction rather than a bare number because "1 on
// 2026.08.20" does not say whether that is all of them or one straggler, and
// the straggler is the whole reason to look.
function VersionRow({ group }: { group: VersionGroup }) {
    const unknown = group.version === '';
    return (
        <div className="flex items-baseline gap-2 text-[12px] leading-relaxed">
            <span className="font-mono text-(--base-06) tabular-nums shrink-0">
                {group.count}/{group.total}
            </span>
            <span
                className={`font-mono ${
                    group.outdated ? 'text-(--warning-light)' : unknown ? 'text-(--base-05)' : 'text-(--base-08)'
                }`}
                title={group.labels.join(', ')}
            >
                {unknown ? 'not reporting' : group.version}
            </span>
            {group.outdated && (
                <span className="text-[10px] font-mono uppercase tracking-[0.06em] text-(--warning-light)">
                    update available
                </span>
            )}
        </div>
    );
}

// One component: what it should be on, and what its instances actually are.
function ComponentRow({ component }: { component: UpdateComponent }) {
    const groups = groupInstances(component.instances);
    return (
        <div className="flex items-start gap-4 px-4 py-2.5 border-b border-(--base-03)/60 last:border-b-0">
            <div className="w-28 shrink-0">
                <div className="text-sm text-(--base-09)">{serviceLabel(component.service)}</div>
                <div className="text-[10px] font-mono text-(--base-05)">
                    {component.latest ? `latest ${component.latest}` : 'no releases'}
                </div>
            </div>
            <div className="min-w-0 flex-1 space-y-0.5">
                {groups.length === 0 ? (
                    // A component the releases name but nothing reports. Shown
                    // rather than hidden: a component the operator never updates
                    // and is never told about is exactly the case this exists for.
                    <div className="text-[12px] text-(--base-05)">Nothing reporting this component.</div>
                ) : (
                    groups.map(g => <VersionRow key={g.version || 'unknown'} group={g} />)
                )}
            </div>
        </div>
    );
}

// A mandatory update. Loud, and louder once the deadline has passed, because at
// that point the component is being refused rather than warned.
function RequirementBanner({ req }: { req: UpdateRequirement }) {
    return (
        <div className={`mx-4 mt-4 px-3 py-2.5 rounded-md border ${
            req.passed
                ? 'bg-(--error-ghost) border-(--error)/50'
                : 'bg-(--warning-ghost) border-(--warning)/40'
        }`}>
            <div className="flex items-start gap-2.5">
                <AlertTriangle
                    size={15}
                    className={`shrink-0 mt-0.5 ${req.passed ? 'text-(--error-light)' : 'text-(--warning-light)'}`}
                />
                <div className="min-w-0 text-[12px] leading-relaxed">
                    <div className={`font-medium ${req.passed ? 'text-(--error-light)' : 'text-(--warning-light)'}`}>
                        {req.passed
                            ? `${serviceLabel(req.service)} must be updated to ${req.minVersion} - the deadline has passed`
                            : `${serviceLabel(req.service)} must be updated to ${req.minVersion} by ${formatDeadline(req.deadline)}`}
                    </div>
                    {req.note && <div className="text-(--base-08) mt-0.5">{req.note}</div>}
                </div>
            </div>
        </div>
    );
}

function ReleaseBlock({ release }: { release: Release }) {
    return (
        <section className="px-4 pt-4">
            <div className="flex items-center gap-2 pb-1">
                <h3 className="text-sm font-display font-semibold text-(--base-09) font-mono">{release.version}</h3>
                {release.required && (
                    <span className="badge badge-error">mandatory</span>
                )}
            </div>
            {categories(release).map(cat => {
                const style = CATEGORY_STYLES[cat.key];
                return (
                    <div key={cat.key} className="mt-2">
                        <div className={`font-mono text-[9px] uppercase tracking-[0.08em] ${style.color}`}>
                            {cat.label}
                        </div>
                        {cat.entries.length === 0 ? (
                            // Written out rather than omitted: an absent heading
                            // reads as "nobody filled this in", and for a Security
                            // section those are very different statements.
                            <div className="text-[12px] text-(--base-05) mt-0.5">Nothing this time.</div>
                        ) : (
                            <ul className="mt-0.5 space-y-1">
                                {cat.entries.map((e, i) => (
                                    <li key={i} className="flex items-start gap-2.5">
                                        <span className={`shrink-0 mt-[7px] w-1.5 h-1.5 rounded-full ${style.dot}`} aria-hidden="true" />
                                        <div className="min-w-0 text-[12px] text-(--base-08) leading-relaxed">
                                            {e.text}
                                            {(e.services?.length ?? 0) > 0 && (
                                                <span className="ml-1.5 font-mono text-[10px] text-(--base-05)">
                                                    {e.services!.map(serviceLabel).join(', ')}
                                                </span>
                                            )}
                                        </div>
                                    </li>
                                ))}
                            </ul>
                        )}
                    </div>
                );
            })}
        </section>
    );
}

export default function UpdatesBell() {
    const { user } = useAppData();

    const [open, setOpen] = useState(false);
    const [loading, setLoading] = useState(false);
    const [components, setComponents] = useState<UpdateComponent[]>([]);
    const [releases, setReleases] = useState<Release[]>([]);
    const [required, setRequired] = useState<UpdateRequirement[]>([]);
    const [latest, setLatest] = useState<string | undefined>();
    const [seen, setSeen] = useState<string | undefined>();
    const closeRef = useRef<HTMLButtonElement>(null);

    const refresh = useCallback(async () => {
        setLoading(true);
        const res = await getUpdates();
        setLoading(false);
        if (!res.success) return;
        setComponents(res.components ?? []);
        setReleases(res.releases ?? []);
        setRequired(res.required ?? []);
        setLatest(res.latest);
        setSeen(res.seen);
    }, []);

    useEffect(() => {
        if (!user) return;
        refresh();
        const t = setInterval(refresh, REFRESH_MS);
        return () => clearInterval(t);
    }, [user, refresh]);

    useEffect(() => {
        if (!open) return;
        const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') setOpen(false); };
        document.addEventListener('keydown', onKey);
        closeRef.current?.focus();
        return () => document.removeEventListener('keydown', onKey);
    }, [open]);

    if (!user) return null;

    const outdated = anythingOutdated(components);
    const state = bellState({ outdated, required: required.length > 0, latest, seen });

    // Opening acknowledges the newest release so the "unseen" badge clears. It
    // deliberately does NOT clear the attention state: acknowledging that a
    // release exists is not the same as having updated, and a button that goes
    // quiet when you look at it stops reporting the thing it is for.
    const openModal = () => {
        setOpen(true);
        if (latest && latest !== seen) {
            setSeen(latest);
            markUpdatesSeen().catch(() => {});
        }
    };

    const buttonClass =
        state === 'attention'
            ? 'bg-(--warning-ghost) text-(--warning-light) border-(--warning)/40 hover:bg-(--warning-ghost)/80'
            : state === 'unseen'
                ? 'bg-(--accent-ghost) text-(--accent-light) border-(--accent-border) hover:bg-(--accent-ghost)/80'
                : 'text-(--base-07) hover:bg-(--base-04)/50 hover:text-(--base-09) border-transparent';

    const title =
        state === 'attention'
            ? 'Updates - something you run is behind'
            : state === 'unseen'
                ? 'Updates - a new release was published'
                : 'Updates - you are up to date';

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
                {state !== 'idle' && (
                    <span className={`absolute -top-1 -right-1 w-[10px] h-[10px] rounded-full border-2 border-(--base-01) ${
                        state === 'attention' ? 'bg-(--warning)' : 'bg-(--accent-light)'
                    }`} />
                )}
            </button>

            {open && (
                <div
                    className="modal-overlay animate-fade-in z-50"
                    onClick={e => { if (e.target === e.currentTarget) setOpen(false); }}
                >
                    <div className="modal-panel w-full max-w-3xl" role="dialog" aria-modal="true" aria-label="Updates">
                        <div className="flex items-center justify-between px-4 py-3 border-b border-(--base-03)">
                            <div className="flex items-center gap-2">
                                <Sparkles size={15} className="text-(--accent-light)" />
                                <span className="text-sm font-display font-semibold text-(--base-09)">Updates</span>
                                {latest && (
                                    <span className="font-mono text-[10px] text-(--base-05)">latest {latest}</span>
                                )}
                            </div>
                            <div className="flex items-center gap-1">
                                <button
                                    type="button"
                                    onClick={refresh}
                                    disabled={loading}
                                    className="text-(--base-06) hover:text-(--base-09) p-1 rounded-md hover:bg-(--base-03) transition-colors disabled:opacity-50"
                                    aria-label="Check again"
                                    title="Check again"
                                >
                                    <RefreshCw size={15} className={loading ? 'animate-spin' : ''} />
                                </button>
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
                        </div>

                        <div className="max-h-[75vh] overflow-y-auto pb-4">
                            {required.map(req => <RequirementBanner key={req.service} req={req} />)}

                            {components.length > 0 && (
                                <div className="mt-4 mx-4 rounded-md border border-(--base-03) bg-(--base-02)">
                                    {components.map(c => <ComponentRow key={c.service} component={c} />)}
                                </div>
                            )}

                            {releases.length === 0 ? (
                                <div className="px-4 py-10 text-sm text-(--base-06) text-center">
                                    No releases published yet.
                                </div>
                            ) : (
                                releases.map(r => <ReleaseBlock key={r.version} release={r} />)
                            )}
                        </div>
                    </div>
                </div>
            )}
        </>
    );
}
