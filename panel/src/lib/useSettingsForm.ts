'use client';

import { useCallback, useEffect, useRef, useState } from 'react';
import { useUnsavedChanges } from '@/components/settings/UnsavedChanges';
import { confirmDialog, type ConfirmOptions } from '@/components/ui/ConfirmDialog';
import { toast } from '@/components/ui/Toast';

/**
 * One settings form: load, edit, dirty-track, save, discard.
 *
 * The snapshot idiom below was copied by hand into every tab that used the save
 * bar - same ref, same JSON compare, same null-snapshot guard - while the other
 * twenty-odd surfaces each invented something different, which is how the panel
 * ended up with fifteen autosaving controls, thirty-eight local Save buttons
 * and thirteen screens carrying more than one of those at once.
 *
 * Two rules are encoded here rather than left to each call site:
 *
 *   A form that failed to LOAD is never dirty. Otherwise an empty form saves
 *   its emptiness over real configuration the moment anyone touches a field.
 *
 *   A save that changes something with consequences asks first. Routing,
 *   billing and anything needing a restart are settings whose cost is paid by
 *   someone who is not looking at this screen.
 */

/** What a save returned. `value` lets the server correct what it stored. */
export interface SettingsSaveResult<T> {
    ok: boolean;
    message?: string;
    value?: T;
}

export interface SettingsFormOptions<T> {
    /** Resolve null to mean "could not load"; the form then stays read-only. */
    load: () => Promise<T | null>;
    save: (value: T) => Promise<SettingsSaveResult<T>>;
    /** Toast on success. Omit for a form whose save speaks for itself. */
    successMessage?: string;
    /**
     * Return options to ask before committing, or null to commit silently.
     * Receives both sides so it can ask only about the field that changed.
     */
    confirmBeforeSave?: (next: T, previous: T) => ConfirmOptions | null;
}

export interface SettingsForm<T> {
    value: T | null;
    /**
     * The last SAVED value, which is not the same question as `value`.
     *
     * A card that reports whether a feature is in force has to read this one. A
     * badge driven by the form turns green the moment a switch is flipped, and
     * stays green if the operator walks away without saving - which is a lie
     * about the running system delivered by the control that is supposed to
     * describe it.
     */
    saved: T | null;
    /** Edit. No-op before the first successful load. */
    update: (updater: (prev: T) => T) => void;
    /** Shorthand for a flat merge. */
    patch: (partial: Partial<T>) => void;
    loading: boolean;
    /** True when load() resolved null. The form must stay disabled. */
    loadFailed: boolean;
    dirty: boolean;
    saving: boolean;
    /** Resolves TRUE only when the value was actually written. */
    save: () => Promise<boolean>;
    discard: () => void;
    reload: () => Promise<void>;
    /** Last save error, kept for inline display; a toast has already faded. */
    error: string | null;
}

function same(a: unknown, b: unknown): boolean {
    return JSON.stringify(a) === JSON.stringify(b);
}

export function useSettingsForm<T>(opts: SettingsFormOptions<T>): SettingsForm<T> {
    const [value, setValue] = useState<T | null>(null);
    const [loading, setLoading] = useState(true);
    const [loadFailed, setLoadFailed] = useState(false);
    const [saving, setSaving] = useState(false);
    const [error, setError] = useState<string | null>(null);

    // null until a load succeeds. That is the load-failure guard: dirty is
    // computed against it, so no snapshot means never dirty.
    //
    // State rather than a ref, even though it is never rendered directly: dirty
    // IS derived from it during render, and a ref that changes without a
    // re-render would leave the save bar showing a state that is no longer true.
    const [snapshot, setSnapshot] = useState<T | null>(null);

    // Identifies the newest in-flight load, so a slower earlier one cannot
    // overwrite it when it finally arrives.
    const loadTicket = useRef(0);

    // The callers are inline arrow functions at every call site, so holding
    // them in refs keeps the effects below from re-running every render.
    const loadRef = useRef(opts.load);
    const saveRef = useRef(opts.save);
    const confirmRef = useRef(opts.confirmBeforeSave);
    const successRef = useRef(opts.successMessage);
    loadRef.current = opts.load;
    saveRef.current = opts.save;
    confirmRef.current = opts.confirmBeforeSave;
    successRef.current = opts.successMessage;

    const reload = useCallback(async () => {
        // Two overlapping reloads used to resolve in arrival order, last writer
        // wins - and the nastiest interleaving was a LATE null landing on top of
        // an earlier success: snapshot back to null over a populated value,
        // which makes `dirty` permanently false. The form then shows editable
        // data that can never be saved and never says why.
        const ticket = ++loadTicket.current;
        setLoading(true);
        setError(null);
        try {
            const loaded = await loadRef.current();
            if (ticket !== loadTicket.current) return;
            if (loaded === null) {
                setLoadFailed(true);
                setSnapshot(null);
                return;
            }
            setLoadFailed(false);
            setSnapshot(loaded);
            setValue(loaded);
        } catch {
            if (ticket !== loadTicket.current) return;
            setLoadFailed(true);
            setSnapshot(null);
        } finally {
            if (ticket === loadTicket.current) setLoading(false);
        }
    }, []);

    useEffect(() => {
        void reload();
    }, [reload]);

    const update = useCallback((updater: (prev: T) => T) => {
        setValue(prev => (prev === null ? prev : updater(prev)));
    }, []);

    const patch = useCallback((partial: Partial<T>) => {
        setValue(prev => (prev === null ? prev : { ...prev, ...partial }));
    }, []);

    const dirty = snapshot !== null && value !== null && !same(value, snapshot);

    const save = useCallback(async (): Promise<boolean> => {
        const current = value;
        const previous = snapshot;
        if (current === null || previous === null) return false;

        const ask = confirmRef.current?.(current, previous) ?? null;
        // Answering "no" is a refusal to save, not a save. Reporting it as
        // success is how a guard used to navigate away from the very screen the
        // operator had just declined to commit.
        if (ask && !(await confirmDialog(ask))) return false;

        setSaving(true);
        setError(null);
        try {
            const res = await saveRef.current(current);
            if (!res.ok) {
                const msg = res.message || 'Could not save.';
                setError(msg);
                toast(msg, false);
                return false;
            }
            // The server may have normalised what it stored. Taking its answer
            // as the new snapshot is what keeps the form from reading dirty
            // straight after a successful save.
            const stored = res.value ?? current;
            setSnapshot(stored);
            setValue(stored);
            if (successRef.current) toast(successRef.current, true);
            return true;
        } catch (err) {
            // A save that THREW leaves the form dirty and the operator on the
            // page. Without the finally below, `saving` stayed true forever and
            // the bar disabled both of its buttons - dirty edits, no way to save
            // them, no way to discard them.
            const msg = err instanceof Error ? err.message : 'Could not save.';
            setError(msg);
            toast(msg, false);
            return false;
        } finally {
            setSaving(false);
        }
    }, [value, snapshot]);

    const discard = useCallback(() => {
        if (snapshot === null) return;
        setValue(snapshot);
        setError(null);
    }, [snapshot]);

    useUnsavedChanges({ dirty, save, discard, saving });

    return {
        value,
        saved: snapshot,
        update,
        patch,
        loading,
        loadFailed,
        dirty,
        saving,
        save,
        discard,
        reload,
        error,
    };
}
