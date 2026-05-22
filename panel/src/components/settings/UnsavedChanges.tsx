"use client";

import React, {
    createContext,
    useContext,
    useState,
    useEffect,
    useRef,
} from 'react';
import { Loader2 } from 'lucide-react';

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

export interface UnsavedChangesRegistration {
    dirty: boolean;
    save: () => Promise<void> | void;
    discard: () => void;
    saving: boolean;
}

// The value stored in the context: either a live registration or null (clean).
type UnsavedChangesContextValue = UnsavedChangesRegistration | null;

// The setter exposed internally so the layout and hook can both talk to the
// same piece of state.
interface UnsavedChangesCtx {
    registration: UnsavedChangesContextValue;
    setRegistration: (reg: UnsavedChangesContextValue) => void;
}

// ---------------------------------------------------------------------------
// Context
// ---------------------------------------------------------------------------

const UnsavedChangesContext = createContext<UnsavedChangesCtx>({
    registration: null,
    setRegistration: () => {},
});

// ---------------------------------------------------------------------------
// Provider
// ---------------------------------------------------------------------------

export function UnsavedChangesProvider({ children }: { children: React.ReactNode }) {
    const [registration, setRegistration] = useState<UnsavedChangesContextValue>(null);

    // beforeunload — warn the user when they refresh / close the tab / type
    // a new URL while a form on the page is dirty. The custom Save/Discard
    // dialog only fires for in-app navigation (handled by GuardedLink); this
    // catches the cases the SPA can't intercept itself.
    const dirtyRef = useRef(false);
    dirtyRef.current = registration?.dirty ?? false;
    useEffect(() => {
        const handler = (e: BeforeUnloadEvent) => {
            if (!dirtyRef.current) return;
            e.preventDefault();
            // Modern browsers ignore the message but still need returnValue
            // to be set (truthy) to show their generic prompt.
            e.returnValue = '';
        };
        window.addEventListener('beforeunload', handler);
        return () => window.removeEventListener('beforeunload', handler);
    }, []);

    return (
        <UnsavedChangesContext.Provider value={{ registration, setRegistration }}>
            {children}
        </UnsavedChangesContext.Provider>
    );
}

// ---------------------------------------------------------------------------
// Hook — used by each settings tab to register itself
// ---------------------------------------------------------------------------

/**
 * Call this hook inside a settings tab component to register its dirty state,
 * save handler, and discard handler with the shared bar.
 *
 * The registration is updated whenever dirty/saving change, and is
 * automatically cleared on unmount so stale state never leaks to the next tab.
 */
export function useUnsavedChanges(reg: {
    dirty: boolean;
    save: () => Promise<void> | void;
    discard: () => void;
    saving?: boolean;
}) {
    const { setRegistration } = useContext(UnsavedChangesContext);

    // Keep stable refs to the latest callbacks so the effect deps stay minimal.
    const saveRef = useRef(reg.save);
    const discardRef = useRef(reg.discard);
    saveRef.current = reg.save;
    discardRef.current = reg.discard;

    const { dirty, saving = false } = reg;

    useEffect(() => {
        // Register (or update) with the latest values.
        setRegistration({
            dirty,
            save: () => saveRef.current(),
            discard: () => discardRef.current(),
            saving,
        });

        // Cleanup: unregister when the tab unmounts.
        return () => {
            setRegistration(null);
        };
        // Re-run whenever dirty or saving changes; callbacks use refs so they
        // don't need to be in deps.
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [dirty, saving]);
}

// ---------------------------------------------------------------------------
// Consumer hook — used by the layout to read current state
// ---------------------------------------------------------------------------

export function useUnsavedChangesState(): UnsavedChangesContextValue {
    return useContext(UnsavedChangesContext).registration;
}

// ---------------------------------------------------------------------------
// Shared confirm dialog — shown when navigating away with unsaved changes
// (used both by the settings tab bar and the Gateway tab's internal sub-nav)
// ---------------------------------------------------------------------------

export function UnsavedDialog({
    onSave,
    onDiscard,
    onCancel,
    saving,
}: {
    onSave: () => void | Promise<void>;
    onDiscard: () => void;
    onCancel: () => void;
    saving: boolean;
}) {
    return (
        <div className="modal-overlay animate-fade-in" onClick={onCancel}>
            <div
                className="modal-panel w-full max-w-sm"
                onClick={e => e.stopPropagation()}
            >
                <div className="modal-header">
                    <h3 className="modal-title">Unsaved changes</h3>
                </div>
                <div className="modal-body">
                    <p className="text-sm text-(--base-07)">
                        You have unsaved changes on this tab. What would you like to do?
                    </p>
                </div>
                <div className="modal-footer">
                    <button
                        type="button"
                        onClick={onCancel}
                        className="btn btn-secondary"
                        disabled={saving}
                    >
                        Cancel
                    </button>
                    <button
                        type="button"
                        onClick={onDiscard}
                        className="btn btn-secondary text-(--error-light) border-(--error) hover:bg-(--error)/10"
                        disabled={saving}
                    >
                        Discard
                    </button>
                    <button
                        type="button"
                        onClick={onSave}
                        disabled={saving}
                        className="btn btn-primary disabled:opacity-40 inline-flex items-center gap-1.5"
                    >
                        {saving && <Loader2 size={13} className="animate-spin" />}
                        Save
                    </button>
                </div>
            </div>
        </div>
    );
}
