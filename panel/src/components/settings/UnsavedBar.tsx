'use client';

import { Loader2 } from 'lucide-react';
import { useUnsavedChangesState } from '@/components/settings/UnsavedChanges';

/**
 * The one save bar, for the whole panel.
 *
 * It used to live inside the settings layout, which is why the rule it enforces
 * only applied there: everything outside Settings kept its own Save button, or
 * saved on every keystroke, or did not track changes at all and dropped them on
 * navigation. A shared rule that covers a quarter of the screens is not a rule.
 *
 * Fixed to the viewport rather than to a column, so a page does not have to be
 * laid out a particular way to take part. It renders nothing while nothing is
 * dirty, which is nearly always.
 */
export default function UnsavedBar() {
    const registration = useUnsavedChangesState();
    if (!registration?.dirty) return null;

    const { saving, save, discard } = registration;

    return (
        <div
            role="region"
            aria-label="Unsaved changes"
            className="unsaved-bar fixed bottom-0 left-0 right-0 z-50 flex items-center justify-between gap-4 border-t border-(--base-03) bg-(--base-02) px-5 py-3 shadow-[0_-8px_24px_rgba(0,0,0,0.35)] animate-fade-in"
        >
            <span className="mono-label text-[11px]">Unsaved changes</span>
            <div className="flex items-center gap-2">
                <button
                    type="button"
                    onClick={() => discard()}
                    className="btn btn-secondary text-xs py-1.5 px-4"
                    disabled={saving}
                >
                    Discard
                </button>
                <button
                    type="button"
                    onClick={() => { void save(); }}
                    disabled={saving}
                    className="btn btn-primary text-xs py-1.5 px-4 disabled:opacity-40 inline-flex items-center gap-1.5"
                >
                    {saving && <Loader2 size={12} className="animate-spin" />}
                    Save
                </button>
            </div>
        </div>
    );
}
