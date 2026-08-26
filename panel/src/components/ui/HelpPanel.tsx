'use client';

import React, { useState } from 'react';
import { HelpCircle, X } from 'lucide-react';

/**
 * The help panel that slides out beside a modal.
 *
 * A field-level popover works where a form is a list of self-evident fields
 * with one hard one. It stops working when the whole dialog is hard - an S3
 * connection is six fields, and the right answer for four of them depends on
 * which provider you are pointing at. That is a page of explanation, not a
 * tooltip, and it belongs next to the form rather than on top of it.
 *
 * LAYOUT: put the dialog and this panel in a `.modal-with-help` row inside the
 * overlay, and give the dialog `w-full lg:max-w-lg`:
 *
 *   <div className="modal-overlay"><div className="modal-with-help">
 *     <div className="modal-panel w-full lg:max-w-lg ...">...</div>
 *     <HelpPanel ... />
 *   </div></div>
 *
 * That class is what keeps the pair centred as a unit. A plain flex row leaves
 * the dialog at its left edge, so the dialog hangs left of centre while the
 * help is closed and moves when it opens.
 */

export interface HelpEntry {
    /** The field this explains, worded exactly as its label. */
    field: string;
    body: React.ReactNode;
}

/** The trigger, meant for a modal header. */
export function HelpPanelButton({
    open,
    onToggle,
    label = 'Show help',
}: {
    open: boolean;
    onToggle: () => void;
    label?: string;
}) {
    return (
        <button
            type="button"
            onClick={onToggle}
            aria-expanded={open}
            aria-label={label}
            className={`btn btn-ghost btn-sm inline-flex items-center gap-1.5 ${
                open ? 'text-(--accent-light)' : ''
            }`}
        >
            <HelpCircle size={14} />
            Help
        </button>
    );
}

export default function HelpPanel({
    open,
    onClose,
    title,
    entries,
    footer,
}: {
    open: boolean;
    onClose: () => void;
    title: string;
    entries: HelpEntry[];
    /** Anything that is about the dialog as a whole rather than one field. */
    footer?: React.ReactNode;
}) {
    if (!open) return null;
    return (
        <aside className="help-panel" aria-label={title}>
            <div className="flex items-center justify-between gap-3 px-4 py-3 border-b border-(--base-03) shrink-0">
                <h3 className="mono-label">{title}</h3>
                <button
                    type="button"
                    onClick={onClose}
                    aria-label="Close help"
                    className="btn btn-icon btn-sm btn-ghost"
                >
                    <X size={14} />
                </button>
            </div>
            <div className="overflow-y-auto px-4 py-4 space-y-4">
                {entries.map(e => (
                    <div key={e.field}>
                        <div className="text-xs font-medium text-(--base-09) mb-1">{e.field}</div>
                        <div className="text-xs text-(--base-06) leading-relaxed">{e.body}</div>
                    </div>
                ))}
                {footer && (
                    <div className="pt-3 border-t border-(--base-03) text-xs text-(--base-06) leading-relaxed">
                        {footer}
                    </div>
                )}
            </div>
        </aside>
    );
}

/**
 * useHelpPanel is the whole state a dialog needs to carry one: whether it is
 * open, and the two props for the pair of components.
 */
export function useHelpPanel(initial = false) {
    const [open, setOpen] = useState(initial);
    return {
        open,
        toggle: () => setOpen(o => !o),
        close: () => setOpen(false),
    };
}
