"use client";

import React, { useState, useEffect, useMemo, useRef, useCallback, lazy, Suspense } from 'react';
import { useParams } from 'next/navigation';
import { AlertTriangle, Save, RotateCcw, Code2, ListChecks, Search, ChevronDown, FileQuestion, Power } from 'lucide-react';
import { useAppData } from '@/lib/AppDataContext';
import { getFileContent, saveFile } from '@/lib/api';
import {
    VANILLA_SCHEMA, GROUP_LABELS, groupedSchema,
    type PropertyDef, type PropertyGroup,
} from '@/lib/server-properties-schema';
import { parseProperties, serializeProperties, type PropertiesDoc } from '@/lib/properties-codec';

const CodeMirrorEditor = lazy(() => import('@dylaris/ui-filebrowser').then(m => ({ default: m.CodeMirrorEditor })));

type Mode = 'simple' | 'advanced';

function inferValue(def: PropertyDef, raw: string | undefined): string | boolean | number {
    if (raw === undefined) return def.default;
    switch (def.type) {
        case 'toggle':
            return raw === 'true';
        case 'number':
        case 'slider':
            return Number.isFinite(Number(raw)) ? Number(raw) : def.default;
        default:
            return raw;
    }
}

function stringifyValue(def: PropertyDef, v: string | boolean | number): string {
    if (def.type === 'toggle') return v ? 'true' : 'false';
    if (def.type === 'number' || def.type === 'slider') return String(v);
    return String(v);
}

interface PropertyRowProps {
    def: PropertyDef;
    raw: string | undefined;
    onChange: (key: string, newRaw: string) => void;
}

function PropertyRow({ def, raw, onChange }: PropertyRowProps) {
    const value = inferValue(def, raw);
    const commit = (next: string | boolean | number) => onChange(def.key, stringifyValue(def, next));

    return (
        <div className="grid grid-cols-[20rem_auto] gap-6 items-start py-3 border-b border-(--base-03) last:border-b-0">
            <div className="min-w-0">
                <div className="flex items-center gap-2 flex-wrap">
                    <label htmlFor={`prop-${def.key}`} className="text-sm font-medium text-(--base-09)">{def.label}</label>
                    <code className="font-mono text-[10px] text-(--base-05)">{def.key}</code>
                    {def.requiresRestart && (
                        <span className="mono-label text-(--warning-light) bg-(--warning-ghost) border border-(--warning-border) rounded-sm px-1.5">restart</span>
                    )}
                </div>
                {def.description && (
                    <p className="text-xs text-(--base-06) mt-0.5">{def.description}</p>
                )}
            </div>
            <div className="flex items-center">
                {def.type === 'toggle' && (
                    <button
                        id={`prop-${def.key}`}
                        type="button"
                        onClick={() => commit(!(value as boolean))}
                        className="toggle-track"
                        role="switch"
                        aria-checked={value as boolean}
                    >
                        <span className={`toggle-knob ${value ? 'translate-x-5 bg-(--accent)' : 'translate-x-0.5 bg-(--base-05)'}`} />
                    </button>
                )}
                {def.type === 'text' && (
                    <input
                        id={`prop-${def.key}`}
                        type="text"
                        value={value as string}
                        onChange={e => commit(e.target.value)}
                        placeholder={def.placeholder}
                        className="input-field w-72"
                    />
                )}
                {def.type === 'textarea' && (
                    <textarea
                        id={`prop-${def.key}`}
                        value={value as string}
                        maxLength={def.maxLength}
                        onChange={e => commit(e.target.value)}
                        className="input-field w-80 min-h-20 font-mono text-xs leading-relaxed"
                    />
                )}
                {def.type === 'number' && (
                    <input
                        id={`prop-${def.key}`}
                        type="number"
                        value={value as number}
                        min={def.min}
                        max={def.max}
                        onChange={e => commit(Number(e.target.value))}
                        className="input-field w-32 text-right tabular-nums"
                    />
                )}
                {def.type === 'slider' && (
                    <div className="flex items-center gap-3 w-72">
                        <input
                            id={`prop-${def.key}`}
                            type="range"
                            value={value as number}
                            min={def.min}
                            max={def.max}
                            step={def.step ?? 1}
                            onChange={e => commit(Number(e.target.value))}
                            className="flex-1 accent-(--accent)"
                        />
                        <span className="font-mono text-xs text-(--base-08) w-10 text-right tabular-nums">{value as number}</span>
                    </div>
                )}
                {def.type === 'select' && (
                    <select
                        id={`prop-${def.key}`}
                        value={value as string}
                        onChange={e => commit(e.target.value)}
                        className="input-field w-72"
                    >
                        {def.options.map(opt => (
                            <option key={opt.value} value={opt.value}>{opt.label}</option>
                        ))}
                    </select>
                )}
            </div>
        </div>
    );
}

export default function ServerPropertiesView() {
    const params = useParams();
    const { servers } = useAppData();
    const serverId = Number(params?.id);
    const server = servers.find(s => s.id === serverId);

    const [mode, setMode] = useState<Mode>('simple');
    const [loading, setLoading] = useState(true);
    const [error, setError] = useState<string | null>(null);
    // Separate state for "the file just doesn't exist yet" so the UI
    // can show a calm empty state with actionable guidance, instead of
    // a red error + Retry that just re-fires the same 404.
    const [notFound, setNotFound] = useState(false);
    const [doc, setDoc] = useState<PropertiesDoc | null>(null);
    const [advancedText, setAdvancedText] = useState('');
    const [advancedDirty, setAdvancedDirty] = useState(false);
    const [savingAdvanced, setSavingAdvanced] = useState(false);
    const [toast, setToast] = useState<{ msg: string; ok: boolean } | null>(null);
    const [restartPending, setRestartPending] = useState(false);
    const [search, setSearch] = useState('');
    const [openGroups, setOpenGroups] = useState<Set<PropertyGroup>>(new Set(['world', 'network', 'performance']));

    // External-change banner state: the text we last loaded/saved so
    // the background poll can tell apart "nothing changed" from "the
    // server (or another panel session) just rewrote the file". When
    // non-null, the banner offers the user a chance to reload the
    // remote version or stick with their in-progress edits.
    const lastLoadedTextRef = useRef<string>('');
    const [externalChange, setExternalChange] = useState<string | null>(null);

    // Stable primitives derived from the server object. `server` itself
    // is replaced on every 5s useAppData poll (different reference, same
    // content), and depending on it directly made reload's identity
    // churn on every tick -- which fired the whole load pipeline every
    // 5 seconds and made the tab visibly flicker. The reload/save
    // callbacks now key off these primitives instead.
    const serverUuid = server?.uuid ?? '';
    const activeSubServer = server?.activeSubServer ?? '';

    // server.properties lives inside the active sub-server dir, not
    // at the server root. The Node's validatePath resolves request
    // paths relative to <storage>/<uuid>/, so without the sub-server
    // prefix we get "file not found" no matter how many times the
    // user clicks retry. If no active sub-server is selected we
    // surface that as a friendly empty state instead of a 404 storm.
    const filePath = activeSubServer ? `${activeSubServer}/server.properties` : '';
    const showToast = useCallback((msg: string, ok = true) => {
        setToast({ msg, ok });
        setTimeout(() => setToast(null), 3500);
    }, []);

    const reload = useCallback(async () => {
        if (!serverUuid) return;
        setNotFound(false);
        setExternalChange(null);
        // No active sub-server -> no file to load. Treat it as a soft
        // empty state, not an error -- retrying won't help until the
        // user installs / switches to a sub-server.
        if (!filePath) {
            setLoading(false);
            setError(null);
            setNotFound(true);
            setDoc(null);
            return;
        }
        setLoading(true);
        setError(null);
        try {
            const res = await getFileContent(filePath, serverUuid);
            if (res?.success === false) {
                // File-not-found is the expected state for a brand-new
                // sub-server that hasn't run yet (MC generates
                // server.properties on first boot). Tell that apart
                // from real errors so the UI doesn't yell at the user.
                const msg = (res.message || '').toLowerCase();
                if (msg.includes('not found') || msg.includes('no such file') || msg.includes('does not exist') || msg.includes('enoent')) {
                    setNotFound(true);
                    setDoc(null);
                } else {
                    setError(res.message || 'Could not load server.properties.');
                    setDoc(null);
                }
            } else {
                const text: string = res.content ?? res.text ?? '';
                const parsed = parseProperties(text);
                setDoc(parsed);
                setAdvancedText(text);
                setAdvancedDirty(false);
                lastLoadedTextRef.current = text;
            }
        } catch {
            setError('Network error while loading server.properties.');
        }
        setLoading(false);
    }, [serverUuid, filePath]);

    useEffect(() => { reload(); }, [reload]);

    // Background change-detection. Polls the file every 30s and
    // surfaces a banner if the content on disk has drifted from what
    // we last loaded/saved. Doesn't auto-overwrite the user's view --
    // they choose between Reload (take theirs) and Keep mine (next
    // save will overwrite). Skipped when there's no file yet, when
    // we're in an error state, or when the user is mid-edit in
    // Advanced mode (a fresh remote write at the wrong moment would
    // be noise -- we still flag it, the user just sees an offer to
    // resolve).
    useEffect(() => {
        if (!serverUuid || !filePath || notFound || error) return;
        let cancelled = false;
        const check = async () => {
            try {
                const res = await getFileContent(filePath, serverUuid);
                if (cancelled) return;
                if (res?.success === false) return;
                const text: string = res.content ?? res.text ?? '';
                if (text !== lastLoadedTextRef.current) {
                    setExternalChange(text);
                }
            } catch { /* network blip; try again next tick */ }
        };
        const interval = setInterval(check, 30_000);
        return () => { cancelled = true; clearInterval(interval); };
    }, [serverUuid, filePath, notFound, error]);

    const acceptExternalChange = useCallback(() => {
        if (externalChange === null) return;
        const text = externalChange;
        setDoc(parseProperties(text));
        setAdvancedText(text);
        setAdvancedDirty(false);
        lastLoadedTextRef.current = text;
        setExternalChange(null);
        showToast('Reloaded from disk');
    }, [externalChange, showToast]);

    const dismissExternalChange = useCallback(() => {
        // Stamp the baseline to whatever's currently on disk so the
        // banner doesn't immediately reappear -- the user said "keep
        // mine", they'll overwrite on next save.
        if (externalChange !== null) lastLoadedTextRef.current = externalChange;
        setExternalChange(null);
    }, [externalChange]);

    // Mirror the latest doc into a ref so two inline edits fired before a
    // re-render both merge onto the newest state instead of a stale closure
    // (which dropped the earlier edit).
    const docRef = useRef(doc);
    useEffect(() => { docRef.current = doc; }, [doc]);

    const inlineSave = useCallback(async (key: string, newRaw: string) => {
        const cur = docRef.current;
        if (!cur || !serverUuid) return;
        const def = VANILLA_SCHEMA.find(d => d.key === key);
        // Optimistic update — keeps the UI responsive between keystrokes.
        const nextValues = { ...cur.values, [key]: newRaw };
        const nextOrder = cur.order.includes(key) ? cur.order : [...cur.order, key];
        const nextDoc: PropertiesDoc = { ...cur, values: nextValues, order: nextOrder };
        docRef.current = nextDoc; // keep the ref current for a rapid follow-up edit
        setDoc(nextDoc);

        const fileText = serializeProperties(nextDoc, { [key]: newRaw });
        try {
            const res = await saveFile(filePath, fileText, serverUuid);
            if (res?.success === false) {
                showToast(res.message || 'Save failed.', false);
                return;
            }
            // Re-parse so doc.rawLines / lineIndex stay accurate for the
            // next inline save.
            setDoc(parseProperties(fileText));
            setAdvancedText(fileText);
            // Bump the change-detection baseline so the background
            // poll doesn't see our own save as an external change.
            lastLoadedTextRef.current = fileText;
            if (def?.requiresRestart) setRestartPending(true);
            showToast(`Saved ${key}`);
        } catch {
            showToast('Network error', false);
        }
    }, [serverUuid, filePath, showToast]);

    const saveAdvanced = useCallback(async () => {
        if (!serverUuid) return;
        setSavingAdvanced(true);
        try {
            const res = await saveFile(filePath, advancedText, serverUuid);
            if (res?.success === false) {
                showToast(res.message || 'Save failed.', false);
            } else {
                const parsed = parseProperties(advancedText);
                setDoc(parsed);
                setAdvancedDirty(false);
                lastLoadedTextRef.current = advancedText;
                setRestartPending(true);
                showToast('Saved server.properties');
            }
        } catch {
            showToast('Network error', false);
        }
        setSavingAdvanced(false);
    }, [serverUuid, filePath, advancedText, showToast]);

    const switchMode = (next: Mode) => {
        if (next === mode) return;
        if (next === 'simple' && advancedDirty) {
            showToast('Save or discard advanced changes first', false);
            return;
        }
        setMode(next);
    };

    const groups = useMemo(() => groupedSchema(VANILLA_SCHEMA), []);
    const groupKeys = useMemo(() => Object.keys(groups) as PropertyGroup[], [groups]);
    const filteredGroups = useMemo(() => {
        if (!search.trim()) return groups;
        const needle = search.toLowerCase();
        const out: Partial<Record<PropertyGroup, PropertyDef[]>> = {};
        for (const key of groupKeys) {
            const matches = groups[key].filter(d =>
                d.key.toLowerCase().includes(needle) ||
                d.label.toLowerCase().includes(needle) ||
                (d.description?.toLowerCase().includes(needle) ?? false),
            );
            if (matches.length) out[key] = matches;
        }
        return out as Record<PropertyGroup, PropertyDef[]>;
    }, [groups, groupKeys, search]);

    // Keys present in the file but not in the platform's schema.
    // Surfaces those as raw key + text input at the bottom of Simple
    // mode so the panel stays useful when MC adds new properties (or
    // a plugin/mod injects custom ones) before our schema catches up.
    // Without this they'd silently disappear from the UI and only be
    // visible in Advanced mode.
    const unknownEntries = useMemo(() => {
        if (!doc) return [] as Array<{ key: string; value: string }>;
        const known = new Set(VANILLA_SCHEMA.map(d => d.key));
        const needle = search.trim().toLowerCase();
        const entries: Array<{ key: string; value: string }> = [];
        for (const key of doc.order) {
            if (known.has(key)) continue;
            if (needle && !key.toLowerCase().includes(needle) && !(doc.values[key] ?? '').toLowerCase().includes(needle)) {
                continue;
            }
            entries.push({ key, value: doc.values[key] ?? '' });
        }
        return entries;
    }, [doc, search]);

    const toggleGroup = (key: PropertyGroup) => {
        setOpenGroups(prev => {
            const next = new Set(prev);
            if (next.has(key)) next.delete(key); else next.add(key);
            return next;
        });
    };

    const cmRef = useRef<HTMLDivElement>(null); // lazy-load placeholder

    if (!server) return null;

    return (
        <div className="flex flex-col gap-4 h-full">
            <div className="flex items-center gap-3 flex-wrap">
                <h1 className="h-page">Configuration</h1>
                <span className="mono-label">{server.activeSubServer || 'no sub-server selected'}</span>

                <div className="ml-auto flex items-center gap-2">
                    {/* External-change banner. Shown when the
                        background poll noticed the file on disk
                        drifted from what we loaded -- usually because
                        the server itself rewrote it (a /restart, an
                        SFTP/Beam edit, another panel tab). Reload =
                        take theirs (discard local view); Keep mine =
                        re-baseline so the banner stops nagging, the
                        next save will overwrite. */}
                    {externalChange !== null && (
                        <span className="flex items-center gap-2 text-xs py-1.5 px-3 rounded-md bg-(--accent-ghost) border border-(--accent-border) text-(--accent-light)">
                            <AlertTriangle size={13} className="shrink-0" />
                            File changed externally
                            <button
                                onClick={acceptExternalChange}
                                className="ml-1 px-2 py-0.5 rounded bg-(--accent) text-white text-[11px] font-medium hover:bg-(--accent-light)"
                            >
                                Reload
                            </button>
                            <button
                                onClick={dismissExternalChange}
                                className="px-2 py-0.5 rounded border border-(--base-04) text-[11px] font-medium text-(--base-07) hover:bg-(--base-04)"
                            >
                                Keep mine
                            </button>
                        </span>
                    )}
                    {restartPending && (
                        <span className="alert alert-warning text-xs py-1.5 px-3">
                            <AlertTriangle size={13} className="text-(--warning-light) shrink-0 mt-0.5" />
                            Restart pending for changes to take effect
                        </span>
                    )}
                    <div className="flex items-center bg-(--base-02) border border-(--base-03) rounded-md p-0.5">
                        <button
                            onClick={() => switchMode('simple')}
                            className={`px-3 py-1 rounded text-xs font-medium inline-flex items-center gap-1.5 transition-colors ${mode === 'simple' ? 'bg-(--base-04) text-(--base-09)' : 'text-(--base-06) hover:text-(--base-08)'}`}
                        >
                            <ListChecks size={12} /> Simple
                        </button>
                        <button
                            onClick={() => switchMode('advanced')}
                            className={`px-3 py-1 rounded text-xs font-medium inline-flex items-center gap-1.5 transition-colors ${mode === 'advanced' ? 'bg-(--base-04) text-(--base-09)' : 'text-(--base-06) hover:text-(--base-08)'}`}
                        >
                            <Code2 size={12} /> Advanced
                        </button>
                    </div>
                </div>
            </div>

            {loading && (
                <p className="text-sm text-(--base-07)">Loading server.properties…</p>
            )}

            {error && (
                <div className="alert alert-error">
                    <AlertTriangle size={14} className="shrink-0 mt-0.5" />
                    <div className="flex-1">
                        <p>{error}</p>
                        <button onClick={reload} className="btn btn-secondary btn-sm mt-2">
                            <RotateCcw size={12} /> Retry
                        </button>
                    </div>
                </div>
            )}

            {/* Empty state: the file doesn't exist yet. Minecraft
                generates server.properties on first boot, so for a
                freshly-installed sub-server this is the normal state.
                We don't auto-create it -- that would let stale defaults
                stomp the JAR's. The user starts the server once, MC
                writes the file, then the tab works. */}
            {!loading && notFound && !filePath && (
                <div className="flex-1 flex flex-col items-center justify-center text-center text-(--base-07) gap-3 py-12">
                    <FileQuestion size={40} className="opacity-40" />
                    <div>
                        <p className="text-sm font-medium text-(--base-09)">No active sub-server selected</p>
                        <p className="text-xs text-(--base-06) mt-1">Install or switch to a sub-server in the Setup tab to edit its server.properties.</p>
                    </div>
                </div>
            )}
            {!loading && notFound && !!filePath && (
                <div className="flex-1 flex flex-col items-center justify-center text-center text-(--base-07) gap-3 py-12">
                    <FileQuestion size={40} className="opacity-40" />
                    <div>
                        <p className="text-sm font-medium text-(--base-09)">
                            <code className="font-mono text-(--base-08)">server.properties</code> doesn&apos;t exist yet
                        </p>
                        <p className="text-xs text-(--base-06) mt-1 max-w-md">
                            Minecraft generates this file the first time the server boots.
                            Start the server once from the power buttons; the tab will pick it up automatically.
                        </p>
                    </div>
                    <div className="flex items-center gap-2 mt-2">
                        <span className="inline-flex items-center gap-1.5 text-xs text-(--base-06) font-mono">
                            <Power size={12} /> Start the server from the header
                        </span>
                        <button onClick={reload} className="btn btn-secondary btn-sm">
                            <RotateCcw size={12} /> Check again
                        </button>
                    </div>
                </div>
            )}

            {!loading && !error && doc && mode === 'simple' && (
                <div className="flex-1 overflow-auto card card-pad">
                    <div className="flex items-center gap-2 mb-4 sticky top-0 bg-(--base-02) z-10 -mx-5 -mt-5 px-5 pt-5 pb-3 border-b border-(--base-03)">
                        <Search size={13} className="text-(--base-05)" />
                        <input
                            type="text"
                            value={search}
                            onChange={e => setSearch(e.target.value)}
                            placeholder="Search properties…"
                            className="input-field flex-1 max-w-md"
                        />
                    </div>
                    {(Object.keys(filteredGroups) as PropertyGroup[]).map(group => (
                        <section key={group} className="mb-4">
                            <button
                                onClick={() => toggleGroup(group)}
                                className="w-full flex items-center justify-between py-2 mb-1 mono-label hover:text-(--base-08) transition-colors"
                            >
                                <span className="flex items-center gap-1.5">
                                    <ChevronDown size={12} className={`transition-transform ${openGroups.has(group) ? '' : '-rotate-90'}`} />
                                    {GROUP_LABELS[group]}
                                </span>
                                <span>{filteredGroups[group].length}</span>
                            </button>
                            {openGroups.has(group) && (
                                <div className="pl-3 border-l border-(--base-03)">
                                    {filteredGroups[group].map(def => (
                                        <PropertyRow
                                            key={def.key}
                                            def={def}
                                            raw={doc.values[def.key]}
                                            onChange={inlineSave}
                                        />
                                    ))}
                                </div>
                            )}
                        </section>
                    ))}

                    {/* Unknown keys -- anything in server.properties that
                        our VANILLA_SCHEMA doesn't describe. New MC
                        versions, plugins / mods, or admin-customized
                        properties end up here so they're still editable
                        from Simple mode instead of being silently
                        invisible. Raw key on the left, free-form text
                        input on the right; same inlineSave path as the
                        typed rows. */}
                    {unknownEntries.length > 0 && (
                        <section className="mb-4 mt-6">
                            <div className="flex items-center justify-between py-2 mb-1 mono-label text-(--warning-light)">
                                <span className="flex items-center gap-1.5">
                                    <FileQuestion size={12} />
                                    Custom / Unknown Properties
                                </span>
                                <span>{unknownEntries.length}</span>
                            </div>
                            <p className="text-xs text-(--base-06) mb-2 pl-1">
                                Keys we don&apos;t have a schema entry for. Edits save the same way as the typed rows above.
                            </p>
                            <div className="pl-3 border-l border-(--warning-border)">
                                {unknownEntries.map(entry => (
                                    <div key={entry.key} className="grid grid-cols-[20rem_auto] gap-6 items-center py-3 border-b border-(--base-03) last:border-b-0">
                                        <div className="min-w-0">
                                            <code className="font-mono text-xs text-(--base-08) break-all">{entry.key}</code>
                                        </div>
                                        <div className="flex items-center">
                                            <input
                                                type="text"
                                                defaultValue={entry.value}
                                                onBlur={e => {
                                                    const next = e.target.value;
                                                    if (next !== entry.value) inlineSave(entry.key, next);
                                                }}
                                                onKeyDown={e => {
                                                    if (e.key === 'Enter') {
                                                        (e.currentTarget as HTMLInputElement).blur();
                                                    }
                                                }}
                                                className="input-field w-72 font-mono text-xs"
                                                placeholder="(empty)"
                                            />
                                        </div>
                                    </div>
                                ))}
                            </div>
                        </section>
                    )}
                </div>
            )}

            {!loading && !error && doc && mode === 'advanced' && (
                <div className="flex-1 flex flex-col gap-3 min-h-0">
                    <div className="flex items-center gap-2 text-xs text-(--base-06)">
                        <span>Raw <code className="font-mono text-(--base-08)">server.properties</code>. Changes are not applied until you save.</span>
                        {advancedDirty && <span className="mono-label text-(--warning-light)">unsaved</span>}
                    </div>
                    <div ref={cmRef} className="flex-1 min-h-0 rounded-md bg-(--base-01) border border-(--base-03) overflow-hidden">
                        <Suspense fallback={<div className="h-full flex items-center justify-center text-sm text-(--base-07)">Loading editor…</div>}>
                            <CodeMirrorEditor
                                value={advancedText}
                                onChange={next => { setAdvancedText(next); setAdvancedDirty(true); }}
                                filename={filePath}
                                className="h-full"
                            />
                        </Suspense>
                    </div>
                    <div className="flex items-center gap-2 justify-end">
                        <button
                            onClick={() => { setAdvancedText(serializeProperties(doc, {})); setAdvancedDirty(false); }}
                            disabled={!advancedDirty || savingAdvanced}
                            className="btn btn-secondary disabled:opacity-40"
                        >
                            <RotateCcw size={13} /> Discard
                        </button>
                        <button
                            onClick={saveAdvanced}
                            disabled={!advancedDirty || savingAdvanced}
                            className="btn btn-primary disabled:opacity-40"
                        >
                            <Save size={13} /> {savingAdvanced ? 'Saving…' : 'Save'}
                        </button>
                    </div>
                </div>
            )}

            {toast && (
                <div className="toast-container">
                    <div className="toast">
                        <div className={`toast-bar ${toast.ok ? 'bg-(--success-light)' : 'bg-(--error-light)'}`} />
                        <span className="text-sm text-(--base-09)">{toast.msg}</span>
                    </div>
                </div>
            )}
        </div>
    );
}
