"use client";

import React, { useState, useEffect, useMemo, useRef, useCallback, lazy, Suspense } from 'react';
import { useParams } from 'next/navigation';
import { AlertTriangle, Save, RotateCcw, Code2, ListChecks, Search, ChevronDown } from 'lucide-react';
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
        <div className="grid grid-cols-[1fr_auto] gap-4 items-start py-3 border-b border-(--base-03) last:border-b-0">
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
            <div className="flex justify-end items-center min-w-[200px]">
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
    const [doc, setDoc] = useState<PropertiesDoc | null>(null);
    const [advancedText, setAdvancedText] = useState('');
    const [advancedDirty, setAdvancedDirty] = useState(false);
    const [savingAdvanced, setSavingAdvanced] = useState(false);
    const [toast, setToast] = useState<{ msg: string; ok: boolean } | null>(null);
    const [restartPending, setRestartPending] = useState(false);
    const [search, setSearch] = useState('');
    const [openGroups, setOpenGroups] = useState<Set<PropertyGroup>>(new Set(['world', 'network', 'performance']));

    const filePath = 'server.properties';
    const showToast = useCallback((msg: string, ok = true) => {
        setToast({ msg, ok });
        setTimeout(() => setToast(null), 3500);
    }, []);

    const reload = useCallback(async () => {
        if (!server) return;
        setLoading(true);
        setError(null);
        try {
            const res = await getFileContent(filePath, server.uuid);
            if (res?.success === false) {
                setError(res.message || 'Could not load server.properties.');
                setDoc(null);
            } else {
                const text: string = res.content ?? res.text ?? '';
                const parsed = parseProperties(text);
                setDoc(parsed);
                setAdvancedText(text);
                setAdvancedDirty(false);
            }
        } catch {
            setError('Network error while loading server.properties.');
        }
        setLoading(false);
    }, [server]);

    useEffect(() => { reload(); }, [reload]);

    const inlineSave = useCallback(async (key: string, newRaw: string) => {
        if (!doc || !server) return;
        const def = VANILLA_SCHEMA.find(d => d.key === key);
        // Optimistic update — keeps the UI responsive between keystrokes.
        const nextValues = { ...doc.values, [key]: newRaw };
        const nextOrder = doc.order.includes(key) ? doc.order : [...doc.order, key];
        const nextDoc: PropertiesDoc = { ...doc, values: nextValues, order: nextOrder };
        setDoc(nextDoc);

        const fileText = serializeProperties(doc, { [key]: newRaw });
        try {
            const res = await saveFile(filePath, fileText, server.uuid);
            if (res?.success === false) {
                showToast(res.message || 'Save failed.', false);
                return;
            }
            // Re-parse so doc.rawLines / lineIndex stay accurate for the
            // next inline save.
            setDoc(parseProperties(fileText));
            setAdvancedText(fileText);
            if (def?.requiresRestart) setRestartPending(true);
            showToast(`Saved ${key}`);
        } catch {
            showToast('Network error', false);
        }
    }, [doc, server, showToast]);

    const saveAdvanced = useCallback(async () => {
        if (!server) return;
        setSavingAdvanced(true);
        try {
            const res = await saveFile(filePath, advancedText, server.uuid);
            if (res?.success === false) {
                showToast(res.message || 'Save failed.', false);
            } else {
                const parsed = parseProperties(advancedText);
                setDoc(parsed);
                setAdvancedDirty(false);
                setRestartPending(true);
                showToast('Saved server.properties');
            }
        } catch {
            showToast('Network error', false);
        }
        setSavingAdvanced(false);
    }, [server, advancedText, showToast]);

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
