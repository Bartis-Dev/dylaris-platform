"use client";

import React, { useEffect, useState , useRef} from 'react';
import Link from 'next/link';
import { LifeBuoy, Loader2, Trash2, ArrowRight } from 'lucide-react';
import { getTicketSettings, saveTicketSettings, TicketSettings } from '@/lib/api/tickets';
import { SkeletonHeader, SkeletonCard } from '@/components/Skeleton';
import { toast } from '@/components/ui/Toast';
import { useUnsavedChanges } from '@/components/settings/UnsavedChanges';

const defaultSettings: TicketSettings = {
    crossTeamVisibility: true,
    watchersDefaultCanReply: false,
    allowUsersToAddWatchers: true,
    auditRetentionDays: 0, // matches the server default: no horizon until one is saved
    maxFileSizeMb: 10,
    maxTicketSizeMb: 50,
    maxUserSizeMb: 500,
    autoCloseEnabled: false,
    autoCloseDaysAfterResolved: 7,
    deletionEnabled: false,
};

export default function TicketSettingsTab() {
    const [s, setS] = useState<TicketSettings>(defaultSettings);
    // See MaintenanceTab: this form tracked nothing, so leaving the page
    // dropped every edit without a word.
    const snapshotRef = useRef<TicketSettings | null>(null);
    const [loading, setLoading] = useState(true);
    const [saving, setSaving] = useState(false);
    // Without this a failed load renders defaultSettings as though it were the
    // stored config and Save writes those defaults over the real ones. See
    // BeamTab for the same reasoning; the tabs that snapshot into a ref get this
    // for free because a null snapshot keeps `dirty` false.
    const [loadFailed, setLoadFailed] = useState(false);

    useEffect(() => {
        getTicketSettings().then(res => {
            if (res.success && res.settings) { setS(res.settings); snapshotRef.current = res.settings; }
            else setLoadFailed(true);
            setLoading(false);
        });
    }, []);

    const flash = (msg: string, ok = true) => toast(msg, ok);

    const handleSave = async () => {
        setSaving(true);
        const res = await saveTicketSettings(s);
        setSaving(false);
        if (res.success) {
            const stored = res.settings ?? s;
            setS(stored);
            snapshotRef.current = stored;
            flash('Saved.');
        } else {
            flash(res.message || 'Save failed.', false);
        }
    };

    const dirty =
        snapshotRef.current !== null &&
        JSON.stringify(s) !== JSON.stringify(snapshotRef.current);

    const handleDiscard = () => {
        if (snapshotRef.current) setS(snapshotRef.current);
    };

    useUnsavedChanges({ dirty, save: handleSave, discard: handleDiscard, saving });

    if (loading) {
        return (
            <div className="space-y-6 max-w-3xl">
                <SkeletonHeader />
                <SkeletonCard height="h-[480px]" />
            </div>
        );
    }

    return (
        <div className="space-y-6 max-w-3xl">
            <header>
                <h2 className="text-lg font-display flex items-center gap-2">
                    <LifeBuoy size={18} className="text-(--accent-light)" /> Ticket Settings
                </h2>
                <p className="text-sm text-(--base-06) mt-1">
                    Visibility scope for support teams, watcher (CC) policy, and how long ticket audit history is kept.
                </p>
            </header>

            <section className="card p-5 border border-(--base-03) space-y-4">
                <Toggle
                    label="Cross-team visibility"
                    description="When on, every supporter sees every ticket. When off, supporters only see tickets assigned to them, unassigned tickets (for triage), or tickets matching their support_team."
                    value={s.crossTeamVisibility}
                    onChange={v => setS({ ...s, crossTeamVisibility: v })}
                />
                <Toggle
                    label="Allow users to add watchers"
                    description="Lets the ticket creator CC other users on their own ticket. Watchers see all non-internal messages."
                    value={s.allowUsersToAddWatchers}
                    onChange={v => setS({ ...s, allowUsersToAddWatchers: v })}
                />
                <Toggle
                    label="Default new watchers to reply-allowed"
                    description="When on, newly added watchers can post replies (non-internal). When off, watchers are read-only by default — admins/support can still flip individual watchers on a per-ticket basis."
                    value={s.watchersDefaultCanReply}
                    onChange={v => setS({ ...s, watchersDefaultCanReply: v })}
                />
                <div className="flex items-center gap-3 pt-2 border-t border-(--base-03)">
                    <label className="input-label mb-0 shrink-0">Audit retention</label>
                    <input
                        type="number"
                        min={0}
                        max={3650}
                        value={s.auditRetentionDays}
                        onChange={e => setS({ ...s, auditRetentionDays: Number(e.target.value) || 0 })}
                        className="input-field w-24"
                    />
                    <span className="text-sm text-(--base-07)">days</span>
                    <p className="text-xs text-(--base-06) leading-tight">
                        0 = keep forever. Range 0-3650. A positive value arms a daily sweep that deletes
                        ticket audit history past that age; 180 is a reasonable horizon.
                    </p>
                </div>

                {/* Attachment quotas. 0 = unlimited per axis. */}
                <div className="pt-4 border-t border-(--base-03) space-y-3">
                    <h4 className="mono-label flex items-center gap-1.5">Attachment quotas (MB)</h4>
                    <p className="text-xs text-(--base-06)">Set 0 on any axis to disable that limit. File quota gates a single upload; ticket quota caps the total per ticket; user quota caps the total a single user has stored across all their tickets.</p>
                    <div className="grid grid-cols-3 gap-3">
                        <div className="flex flex-col gap-[5px]">
                            <label className="input-label">Per file</label>
                            <input type="number" min={0} max={1024} value={s.maxFileSizeMb}
                                onChange={e => setS({ ...s, maxFileSizeMb: Number(e.target.value) || 0 })}
                                className="input-field w-full" />
                        </div>
                        <div className="flex flex-col gap-[5px]">
                            <label className="input-label">Per ticket</label>
                            <input type="number" min={0} max={10240} value={s.maxTicketSizeMb}
                                onChange={e => setS({ ...s, maxTicketSizeMb: Number(e.target.value) || 0 })}
                                className="input-field w-full" />
                        </div>
                        <div className="flex flex-col gap-[5px]">
                            <label className="input-label">Per user</label>
                            <input type="number" min={0} max={102400} value={s.maxUserSizeMb}
                                onChange={e => setS({ ...s, maxUserSizeMb: Number(e.target.value) || 0 })}
                                className="input-field w-full" />
                        </div>
                    </div>
                </div>

                {/* Auto-close */}
                <div className="pt-4 border-t border-(--base-03) space-y-3">
                    <Toggle
                        label="Auto-close resolved tickets"
                        description="Daily background job. When on, resolved tickets get closed after the configured idle period — keeps the inbox clean without manual sweeping."
                        value={s.autoCloseEnabled}
                        onChange={v => setS({ ...s, autoCloseEnabled: v })}
                    />
                    <div className="flex items-center gap-3">
                        <label className="input-label mb-0 shrink-0">Idle days before close</label>
                        <input type="number" min={1} max={365} value={s.autoCloseDaysAfterResolved}
                            onChange={e => setS({ ...s, autoCloseDaysAfterResolved: Number(e.target.value) || 0 })}
                            className="input-field w-24" disabled={!s.autoCloseEnabled} />
                        <span className="text-sm text-(--base-07)">days</span>
                    </div>
                </div>

                {/* Ticket deletion gate. Off by default — when on, admins get
                    a Delete button on the ticket detail page and every delete
                    is stamped in the audit log. */}
                <div className="pt-4 border-t border-(--base-03) space-y-3">
                    <Toggle
                        label="Allow ticket deletion"
                        description="When enabled, admins can permanently delete tickets. The audit entry is preserved."
                        value={s.deletionEnabled}
                        onChange={v => setS({ ...s, deletionEnabled: v })}
                    />
                    <Link
                        href="/settings/tickets/deletion-log"
                        className="text-xs text-(--accent-light) hover:underline inline-flex items-center gap-1"
                    >
                        <Trash2 size={11} /> View deletion log
                        <ArrowRight size={11} />
                    </Link>
                </div>

                {loadFailed && (
                    <div className="alert alert-error text-xs" role="alert">
                        The current ticket settings could not be loaded, so the values above are defaults
                        rather than what is stored. Saving is disabled until a reload succeeds.
                    </div>
                )}

                <div className="flex justify-end pt-1">
                    <button
                        type="button"
                        onClick={handleSave}
                        disabled={saving || loadFailed}
                        className="btn btn-primary inline-flex items-center gap-2 disabled:opacity-40 disabled:cursor-not-allowed"
                    >
                        {saving && <Loader2 size={14} className="animate-spin" />}
                        Save ticket settings
                    </button>
                </div>

            </section>

            <p className="text-xs text-(--base-06) italic">
                Categories are managed under <strong>Settings → Ticket Categories</strong>. Enabling the Tickets module
                itself is done under <strong>Modules</strong>.
            </p>
        </div>
    );
}

function Toggle({ label, description, value, onChange }: { label: string; description: string; value: boolean; onChange: (v: boolean) => void }) {
    return (
        <label className="flex items-start justify-between gap-4 cursor-pointer">
            <div>
                <p className="text-sm font-medium">{label}</p>
                <p className="text-xs text-(--base-06) leading-snug mt-0.5">{description}</p>
            </div>
            <input
                type="checkbox"
                checked={value}
                onChange={e => onChange(e.target.checked)}
                className="checkbox shrink-0 mt-1"
            />
        </label>
    );
}
