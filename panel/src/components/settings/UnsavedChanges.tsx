"use client";

import React, {
    createContext,
    useContext,
    useState,
    useEffect,
    useCallback,
    useRef,
} from 'react';

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
