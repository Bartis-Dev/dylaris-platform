"use client";

import React, { useEffect, useState } from 'react';
import { LifeBuoy, Loader2, CircleCheck, CircleAlert } from 'lucide-react';
import { getTicketSettings, saveTicketSettings, TicketSettings } from '@/lib/api/tickets';

const defaultSettings: TicketSettings = {
    crossTeamVisibility: true,
    watchersDefaultCanReply: false,
    allowUsersToAddWatchers: true,
    auditRetentionDays: 180,
    maxFileSizeMb: 10,
    maxTicketSizeMb: 50,
    maxUserSizeMb: 500,
    autoCloseEnabled: false,
    autoCloseDaysAfterResolved: 7,
};

export default function TicketSettingsTab() {
    const [s, setS] = useState<TicketSettings>(defaultSettings);
    const [loading, setLoading] = useState(true);
    const [saving, setSaving] = useState(false);
    const [toast, setToast] = useState<{ msg: string; ok: boolean } | null>(null);

    useEffect(() => {
        getTicketSettings().then(res => {
            if (res.success && res.settings) setS(res.settings);
            setLoading(false);
        });
    }, []);

    const flash = (msg: string, ok = true) => {
        setToast({ msg, ok });
        setTimeout(() => setToast(null), 2800);
    };

    const handleSave = async () => {
        setSaving(true);
        const res = await saveTicketSettings(s);
        setSaving(false);
        if (res.success) {
            if (res.settings) setS(res.settings);
            flash('Saved.');
        } else {
            flash(res.message || 'Save failed.', false);
        }
    };

    if (loading) {
        return (
            <div className="space-y-6 max-w-3xl">
                <h2 className="text-lg font-display flex items-center gap-2">
                    <LifeBuoy size={18} className="text-(--accent-light)" /> Ticket Settings
                </h2>
                <p className="text-sm text-(--base-06)">Loading…</p>
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
                    <p className="text-xs text-(--base-06) leading-tight">0 = unlimited. Range 0–3650.</p>
                </div>

                {/* Phase 3 — attachment quotas. 0 = unlimited per axis. */}
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

                {/* Phase 3 — auto-close */}
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

                <div className="flex justify-end pt-1">
                    <button
                        type="button"
                        onClick={handleSave}
                        disabled={saving}
                        className="btn btn-primary inline-flex items-center gap-2 disabled:opacity-40"
                    >
                        {saving && <Loader2 size={14} className="animate-spin" />}
                        Save ticket settings
                    </button>
                </div>

                {toast && (
                    <p className={`text-xs ${toast.ok ? 'text-(--success-light)' : 'text-(--error-light)'} flex items-center gap-1`}>
                        {toast.ok ? <CircleCheck size={12} /> : <CircleAlert size={12} />}
                        {toast.msg}
                    </p>
                )}
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
                className="w-4 h-4 accent-(--accent) shrink-0 mt-1"
            />
        </label>
    );
}
