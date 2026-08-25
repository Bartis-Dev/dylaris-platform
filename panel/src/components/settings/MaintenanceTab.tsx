"use client";

import React, { useEffect, useState } from 'react';
import { Wrench, Loader2, CircleCheck, CircleAlert } from 'lucide-react';
import { getMaintenance, saveMaintenance, MaintenanceState } from '@/lib/api';
import { SkeletonHeader, SkeletonCard } from '@/components/Skeleton';
import { toLocalInput, fromLocalInput } from '@/lib/localDateTime';

const BLOCK_LEVELS: { value: MaintenanceState['blockLevel']; label: string; help: string }[] = [
    { value: 'off',          label: 'Off',                help: 'Feature off entirely. Banner hidden, no blocking.' },
    { value: 'banner_only',  label: 'Banner only',        help: 'Show the banner site-wide. Don\'t block any traffic.' },
    { value: 'block_writes', label: 'Block writes',       help: 'Non-admins can still read (GET) but cannot create/update/delete.' },
    { value: 'block_all',    label: 'Block everything',   help: 'Non-admins are fully locked out (admins always pass).' },
];

const defaultState: MaintenanceState = {
    active: false,
    title: 'Maintenance in progress',
    message: 'We\'re performing scheduled maintenance and will be back shortly.',
    expectedEnd: '',
    blockLevel: 'banner_only',
};

export default function MaintenanceTab() {
    const [state, setState] = useState<MaintenanceState>(defaultState);
    const [loading, setLoading] = useState(true);
    const [saving, setSaving] = useState(false);
    const [toast, setToast] = useState<{ msg: string; ok: boolean } | null>(null);
    // A failed load used to leave defaultState on screen looking exactly like a
    // stored config - maintenance OFF at banner_only - with Save enabled. This
    // is the one screen where writing that back is destructive: the DB migration
    // deliberately holds block_all for its whole run, and saving the default
    // would lift it and let users write to a database being copied. Same
    // mechanism BeamTab uses, and the same gate ConfigEditorModal uses.
    const [loadFailed, setLoadFailed] = useState(false);

    useEffect(() => {
        getMaintenance().then(res => {
            if (res.success && res.state) setState(res.state);
            else setLoadFailed(true);
            setLoading(false);
        });
    }, []);

    const flash = (msg: string, ok = true) => {
        setToast({ msg, ok });
        setTimeout(() => setToast(null), 2800);
    };

    const handleSave = async () => {
        setSaving(true);
        const res = await saveMaintenance(state);
        setSaving(false);
        if (res.success) {
            if (res.state) setState(res.state);
            flash('Saved.');
        } else {
            flash(res.message || 'Save failed.', false);
        }
    };

    if (loading) {
        return (
            <div className="space-y-6 max-w-3xl">
                <SkeletonHeader />
                <SkeletonCard height="h-[28rem]" />
            </div>
        );
    }

    return (
        <div className="space-y-6 max-w-3xl">
            <header>
                <h2 className="text-lg font-display flex items-center gap-2">
                    <Wrench size={18} className="text-(--accent-light)" /> Maintenance Mode
                </h2>
                <p className="text-sm text-(--base-06) mt-1">
                    Show a system-wide banner to logged-in users. Optionally block writes or all
                    non-admin traffic while you work. Admins are never blocked — otherwise you couldn&apos;t
                    turn this off again from the panel.
                </p>
            </header>

            <section className="card p-5 border border-(--base-03) space-y-4">
                <label className="flex items-start justify-between gap-4 cursor-pointer">
                    <div>
                        <p className="text-sm font-medium">Active</p>
                        <p className="text-xs text-(--base-06) leading-snug mt-0.5">
                            When on, the banner is shown and the configured block level applies.
                        </p>
                    </div>
                    <input
                        type="checkbox"
                        checked={state.active}
                        onChange={e => setState({ ...state, active: e.target.checked })}
                        className="w-4 h-4 accent-(--accent) shrink-0 mt-1"
                    />
                </label>

                <div className="flex flex-col gap-[5px]">
                    <label className="input-label">Title</label>
                    <input
                        type="text"
                        value={state.title}
                        onChange={e => setState({ ...state, title: e.target.value })}
                        maxLength={120}
                        className="input-field w-full"
                    />
                </div>

                <div className="flex flex-col gap-[5px]">
                    <label className="input-label">Message</label>
                    <textarea
                        value={state.message}
                        onChange={e => setState({ ...state, message: e.target.value })}
                        rows={3}
                        maxLength={500}
                        className="input-field w-full"
                    />
                    <p className="text-xs text-(--base-06)">Plain text. Markdown rendering can be added later if needed.</p>
                </div>

                <div className="flex flex-col gap-[5px]">
                    <label className="input-label">Expected end (optional)</label>
                    <input
                        type="datetime-local"
                        value={state.expectedEnd ? toLocalInput(state.expectedEnd) : ''}
                        onChange={e => setState({ ...state, expectedEnd: e.target.value ? fromLocalInput(e.target.value) : '' })}
                        style={{ colorScheme: 'dark' }}
                        className="input-field datetime-field w-full"
                    />
                    <p className="text-xs text-(--base-06)">Used in the banner as a countdown + as a Retry-After hint on 503 responses.</p>
                </div>

                <div className="flex flex-col gap-[5px]">
                    <label className="input-label">Block level</label>
                    <div className="grid grid-cols-1 gap-2">
                        {BLOCK_LEVELS.map(opt => (
                            <label
                                key={opt.value}
                                className={`flex items-start gap-3 p-3 rounded-md border cursor-pointer transition-colors ${
                                    state.blockLevel === opt.value
                                        ? 'border-(--accent-border) bg-(--accent-ghost)'
                                        : 'border-(--base-04) hover:bg-(--base-03)'
                                }`}
                            >
                                <input
                                    type="radio"
                                    name="blockLevel"
                                    value={opt.value}
                                    checked={state.blockLevel === opt.value}
                                    onChange={() => setState({ ...state, blockLevel: opt.value })}
                                    className="mt-0.5 accent-(--accent)"
                                />
                                <div>
                                    <p className="text-sm font-medium">{opt.label}</p>
                                    <p className="text-xs text-(--base-06)">{opt.help}</p>
                                </div>
                            </label>
                        ))}
                    </div>
                </div>

                {loadFailed && (
                    <div className="alert alert-error text-xs" role="alert">
                        The current maintenance settings could not be loaded, so the values above are
                        defaults rather than what is stored. Saving is disabled until a reload succeeds -
                        writing these back could lift a maintenance mode that is holding right now.
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
                        Save maintenance settings
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
                The maintenance banner is mounted globally in the authenticated layout — it polls{' '}
                <span className="font-mono">/api/maintenance</span> every 30 seconds while you&apos;re signed in.
            </p>
        </div>
    );
}
