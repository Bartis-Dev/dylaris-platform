'use client';

import { useEffect, useState } from 'react';
import { CircleAlert, CircleCheck } from 'lucide-react';

/**
 * One toast host for the whole panel.
 *
 * There were 31 of these before, each a local `useState` plus its own copy of
 * the markup, with five different shapes of state and dismiss timeouts ranging
 * from 2800ms to 4500ms. The same action reported differently depending on
 * which screen you triggered it from, and six autosaving controls reported
 * nothing at all because their file had no toast to begin with.
 *
 * Deliberately a module-level singleton driven by one <ToastRoot/>, the same
 * shape as confirmDialog(): a call site changes by an import instead of also
 * having to own state and render a node. Fails SILENT rather than closed - a
 * missing host costs a message, and refusing the action the message describes
 * would be the worse trade.
 */

export interface ToastOptions {
    /** false renders the error treatment. Defaults to true. */
    ok?: boolean;
    /** Milliseconds before it dismisses itself. Defaults to 3500. */
    durationMs?: number;
}

interface ToastItem extends Required<ToastOptions> {
    id: number;
    message: string;
}

let publish: ((t: ToastItem) => void) | null = null;
let nextId = 1;

/**
 * toast shows a message. The second argument is the ok flag rather than an
 * options object because that is the signature the ~31 call sites already used
 * (`showToast(msg, ok = true)`), so migrating them is a rename.
 */
export function toast(message: string, ok: boolean = true, opts: ToastOptions = {}): void {
    if (!publish) {
        // Not an error worth throwing: the message is the least important part
        // of whatever just succeeded.
        console.warn('toast() called with no <ToastRoot/> mounted:', message);
        return;
    }
    publish({
        id: nextId++,
        message,
        ok: opts.ok ?? ok,
        durationMs: opts.durationMs ?? 3500,
    });
}

/** Mount once, above every screen that can report something. */
export function ToastRoot() {
    const [items, setItems] = useState<ToastItem[]>([]);

    useEffect(() => {
        publish = (t: ToastItem) => setItems(prev => [...prev, t]);
        return () => {
            publish = null;
        };
    }, []);

    if (items.length === 0) return null;

    return (
        // polite, not assertive: these announce a completed action, and
        // interrupting a screen reader mid-sentence for "Saved" is worse than
        // waiting for the pause.
        <div className="toast-container" role="status" aria-live="polite">
            {items.map(item => (
                <ToastCard
                    key={item.id}
                    item={item}
                    onDismiss={() => setItems(prev => prev.filter(i => i.id !== item.id))}
                />
            ))}
        </div>
    );
}

function ToastCard({ item, onDismiss }: { item: ToastItem; onDismiss: () => void }) {
    useEffect(() => {
        const t = setTimeout(onDismiss, item.durationMs);
        return () => clearTimeout(t);
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [item.id, item.durationMs]);

    return (
        <button
            type="button"
            onClick={onDismiss}
            className="toast toast-dismissable"
            // The whole card dismisses, so it is a button - but it is not the
            // way to learn what it says, and a reader that lists it as an
            // actionable control alongside the live region reads it twice.
            aria-hidden="true"
            tabIndex={-1}
        >
            <div className={`toast-bar ${item.ok ? 'bg-(--success-light)' : 'bg-(--error-light)'}`} />
            {item.ok ? (
                <CircleCheck size={14} className="text-(--success-light) shrink-0" />
            ) : (
                <CircleAlert size={14} className="text-(--error-light) shrink-0" />
            )}
            <span className="text-sm text-(--base-09) text-left">{item.message}</span>
        </button>
    );
}
