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
    /** Edit. No-op before the first successful load. */
    update: (updater: (prev: T) => T) => void;
    /** Shorthand for a flat merge. */
    patch: (partial: Partial<T>) => void;
    loading: boolean;
    /** True when load() resolved null. The form must stay disabled. */
    loadFailed: boolean;
    dirty: boolean;
    saving: boolean;
    save: () => Promise<void>;
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
    const snapshot = useRef<T | null>(null);

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
        setLoading(true);
        setError(null);
        const loaded = await loadRef.current();
        setLoading(false);
        if (loaded === null) {
            setLoadFailed(true);
            snapshot.current = null;
            return;
        }
        setLoadFailed(false);
        snapshot.current = loaded;
        setValue(loaded);
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

    const dirty = snapshot.current !== null && value !== null && !same(value, snapshot.current);

    const save = useCallback(async () => {
        const current = value;
        const previous = snapshot.current;
        if (current === null || previous === null) return;

        const ask = confirmRef.current?.(current, previous) ?? null;
        if (ask && !(await confirmDialog(ask))) return;

        setSaving(true);
        setError(null);
        const res = await saveRef.current(current);
        setSaving(false);

        if (!res.ok) {
            const msg = res.message || 'Could not save.';
            setError(msg);
            toast(msg, false);
            return;
        }
        // The server may have normalised what it stored. Taking its answer as
        // the new snapshot is what keeps the form from reading dirty straight
        // after a successful save.
        const stored = res.value ?? current;
        snapshot.current = stored;
        setValue(stored);
        if (successRef.current) toast(successRef.current, true);
    }, [value]);

    const discard = useCallback(() => {
        if (snapshot.current === null) return;
        setValue(snapshot.current);
        setError(null);
    }, []);

    useUnsavedChanges({ dirty, save, discard, saving });

    return {
        value,
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
