"use client";

import React, { useCallback, useEffect, useMemo, useState } from 'react';
import { useParams } from 'next/navigation';
import Link from 'next/link';
import {
    Package, ArrowLeft, Plus, Trash2, Search, ExternalLink, RefreshCw,
    CircleCheck, CircleAlert, X, Download, Box, AlertTriangle, Rocket, Lock,
} from 'lucide-react';
import { systemEvents } from '@/lib/systemEvents';
import {
    getModpack, listMods, addMod, removeMod, listVersions,
    type Modpack, type ModpackMod, type ModpackVersion,
} from '@/lib/api/modpacks';
import {
    searchModrinth, getModrinthVersions, pickPrimaryFile,
    type ModrinthSearchHit, type ModrinthVersion,
} from '@/lib/api/modrinth';
import { publishModpackVersion } from '@/lib/api/modpackPublish';
import { API_URL } from '@/lib/api/core';
import { useAppData } from '@/lib/AppDataContext';

// Phase 14.2 — Modpack version builder. Two columns:
//   left:  current mods in this version (remove inline)
//   right: Modrinth search panel filtered to the pack's loader + MC version
// Click a hit → expand version list → "Add to pack" → POST AddMod.
// Side + required flags are settable per row in the current-mods list.

export default function ModpackVersionBuilderPage() {
    const params = useParams();
    const modpackId = Number(params?.id);
    const versionId = Number(params?.versionId);
    const { featureFlags } = useAppData();
    const modpacksDisabled = !featureFlags.modpacks;

    const [pack, setPack] = useState<Modpack | null>(null);
    const [mods, setMods] = useState<ModpackMod[]>([]);
    const [version, setVersion] = useState<ModpackVersion | null>(null);
    const [loading, setLoading] = useState(true);
    const [publishing, setPublishing] = useState<'beta' | 'release' | null>(null);

    // Modrinth search panel state
    const [query, setQuery] = useState('');
    const [searchHits, setSearchHits] = useState<ModrinthSearchHit[]>([]);
    const [searching, setSearching] = useState(false);
    const [expandedSlug, setExpandedSlug] = useState<string | null>(null);
    const [versions, setVersions] = useState<ModrinthVersion[]>([]);
    const [versionsLoading, setVersionsLoading] = useState(false);

    const [toast, setToast] = useState<{ msg: string; ok: boolean } | null>(null);
    const showToast = useCallback((msg: string, ok = true) => {
        setToast({ msg, ok });
        setTimeout(() => setToast(null), 3500);
    }, []);

    const refresh = useCallback(async () => {
        const [p, list, vs] = await Promise.all([
            getModpack(modpackId),
            listMods(modpackId, versionId),
            listVersions(modpackId),
        ]);
        setPack(p);
        setMods(list);
        setVersion(vs.find(v => v.id === versionId) || null);
        setLoading(false);
    }, [modpackId, versionId]);

    useEffect(() => { refresh(); }, [refresh]);

    useEffect(() => {
        const unsub = systemEvents.on('modpack_mods.changed', (evt) => {
            const vid = (evt.payload as any)?.modpackVersionId;
            if (vid === undefined || vid === versionId) refresh();
        });
        return () => { unsub(); };
    }, [versionId, refresh]);

    const installedIds = useMemo(() => new Set(mods.map(m => m.modrinthProjectId)), [mods]);

    // ----- Modrinth search (debounced) -----
    useEffect(() => {
        if (!pack) return;
        const t = setTimeout(async () => {
            setSearching(true);
            const res = await searchModrinth({
                query: query.trim() || undefined,
                loaders: pack.loader ? [pack.loader] : undefined,
                versions: pack.mcVersion ? [pack.mcVersion] : undefined,
                projectType: 'mod',
                limit: 20,
                index: 'relevance',
            });
            setSearchHits(res?.hits || []);
            setSearching(false);
        }, 350);
        return () => clearTimeout(t);
    }, [query, pack]);

    const handleExpand = async (hit: ModrinthSearchHit) => {
        if (expandedSlug === hit.slug) {
            setExpandedSlug(null);
            setVersions([]);
            return;
        }
        setExpandedSlug(hit.slug);
        setVersionsLoading(true);
        const v = await getModrinthVersions(hit.slug, {
            loaders: pack?.loader ? [pack.loader] : undefined,
            versions: pack?.mcVersion ? [pack.mcVersion] : undefined,
        });
        setVersions(v);
        setVersionsLoading(false);
    };

    const handleAddMod = async (hit: ModrinthSearchHit, v: ModrinthVersion) => {
        const file = pickPrimaryFile(v);
        if (!file) { showToast('No file on this version', false); return; }
        const res = await addMod(modpackId, versionId, {
            projectId: hit.project_id,
            projectSlug: hit.slug,
            versionId: v.id,
            title: hit.title,
            fileName: file.filename,
            downloadUrl: file.url,
            sha512: file.hashes.sha512,
            side: 'both',
            required: true,
        });
        if (res.success) {
            showToast(`Added ${hit.title}`, true);
            setExpandedSlug(null);
            setVersions([]);
            refresh();
        } else {
            showToast(res.message || 'Add failed', false);
        }
    };

    const handleRemove = async (m: ModpackMod) => {
        const res = await removeMod(modpackId, versionId, m.id);
        if (res.success) {
            showToast(`Removed ${m.title || m.fileName}`, true);
            refresh();
        } else {
            showToast(res.message || 'Remove failed', false);
        }
    };

    const handlePublish = async (promoteTo: 'beta' | 'release') => {
        if (mods.length === 0) { showToast('Add some mods before publishing', false); return; }
        setPublishing(promoteTo);
        const res = await publishModpackVersion(modpackId, versionId, { promoteTo });
        setPublishing(null);
        if (res.success) {
            showToast(res.message || `Published as ${promoteTo}`, true);
            refresh();
        } else {
            showToast(res.message || 'Publish failed', false);
        }
    };

    const handleExportMrpack = () => {
        const token = typeof window !== 'undefined'
            ? (localStorage.getItem('authToken') || localStorage.getItem('token'))
            : null;
        if (!token) return;
        const url = `${API_URL}/modpacks/${modpackId}/versions/${versionId}/mrpack?token=${encodeURIComponent(token)}`;
        window.open(url, '_blank');
    };

    // Combined gate for mod-list mutations + publish/export. Frozen versions
    // are immutable by design (Wave A pins the persisted .mrpack to a content
    // hash); platform-disabled is the admin kill-switch.
    const isFrozen = !!version?.frozen;
    const disabled = modpacksDisabled || isFrozen;

    if (loading) return <main className="flex-1 p-6 text-sm text-(--base-06)">Loading…</main>;
    if (!pack) {
        return (
            <main className="flex-1 flex flex-col items-center justify-center p-6 text-(--base-06) gap-3">
                <p className="text-sm">Modpack not found.</p>
                <Link href="/modpacks" className="btn btn-secondary btn-sm">
                    <ArrowLeft size={12} />
                    Back
                </Link>
            </main>
        );
    }

    return (
        <main className="flex-1 flex flex-col overflow-hidden">
            <header className="shrink-0 p-6 pb-3 max-w-6xl">
                <Link href={`/modpacks/${modpackId}`} className="text-xs text-(--base-06) hover:text-(--base-09) inline-flex items-center gap-1 mb-3">
                    <ArrowLeft size={11} />
                    {pack.name}
                </Link>
                {modpacksDisabled && (
                    <div className="card p-3 border border-(--warning) bg-(--warning)/10 mb-4 flex items-start gap-2">
                        <CircleAlert size={16} className="text-(--warning) mt-0.5 shrink-0" />
                        <div className="text-xs text-(--base-09)">
                            Modpack authoring is disabled by the platform admin.
                            Existing modpacks remain readable and downloadable.
                        </div>
                    </div>
                )}
                <div className="flex items-start gap-3">
                    <div className="w-10 h-10 rounded-md bg-(--accent-ghost) flex items-center justify-center shrink-0">
                        <Package size={18} className="text-(--accent-light)" />
                    </div>
                    <div className="min-w-0 flex-1">
                        <h1 className="text-lg font-display font-bold text-(--base-09) inline-flex items-center gap-2">
                            Version Builder
                            {version?.versionString && (
                                <span className="font-mono text-sm text-(--base-07) font-normal">
                                    {version.versionString}
                                </span>
                            )}
                            {isFrozen && (
                                <Lock
                                    size={14}
                                    className="text-(--accent-light)"
                                    aria-label="Frozen"
                                />
                            )}
                        </h1>
                        <p className="text-xs text-(--base-06)">
                            Search filtered to <code className="font-mono">{pack.loader || 'any loader'}</code> ·{' '}
                            <code className="font-mono">MC {pack.mcVersion || 'any'}</code>
                        </p>
                        {isFrozen && (
                            <p className="text-xs text-(--base-06) italic mt-1">
                                This version is frozen — it was persisted to storage on first publish/export.
                                Create a new version to change its mod list.
                            </p>
                        )}
                    </div>
                    <div className="flex items-center gap-2">
                        {/* Export is a read on the persisted .mrpack — stays available even
                            when the version is frozen. It only gets gated by modpacksDisabled
                            so admins can still re-download in either state. */}
                        <button onClick={handleExportMrpack} className="btn btn-secondary btn-sm" disabled={mods.length === 0}>
                            <Download size={12} />
                            Export .mrpack
                        </button>
                        <button
                            onClick={() => handlePublish('beta')}
                            className="btn btn-secondary btn-sm disabled:opacity-40 disabled:cursor-not-allowed"
                            disabled={mods.length === 0 || publishing !== null || modpacksDisabled}
                            title={modpacksDisabled ? 'Modpack authoring is disabled' : 'Publish to Modrinth as a beta version'}
                        >
                            <Rocket size={12} />
                            {publishing === 'beta' ? 'Publishing…' : 'Publish as Beta'}
                        </button>
                        <button
                            onClick={() => handlePublish('release')}
                            className="btn btn-primary btn-sm disabled:opacity-40 disabled:cursor-not-allowed"
                            disabled={mods.length === 0 || publishing !== null || modpacksDisabled}
                            title={modpacksDisabled ? 'Modpack authoring is disabled' : 'Publish to Modrinth as a release version'}
                        >
                            <Rocket size={12} />
                            {publishing === 'release' ? 'Publishing…' : 'Publish as Release'}
                        </button>
                    </div>
                </div>
                {version?.publishedAt && version.modrinthVersionId && (
                    <p className="text-xs text-(--success-light) mt-2 flex items-center gap-1">
                        <CircleCheck size={11} />
                        Published {new Date(version.publishedAt).toLocaleString()}
                        {pack?.modrinthProjectId && (
                            <a
                                href={`https://modrinth.com/modpack/${pack.slug}/version/${version.modrinthVersionId}`}
                                target="_blank"
                                rel="noopener noreferrer"
                                className="ml-1 text-(--accent-light) inline-flex items-center gap-1"
                            >
                                view on Modrinth <ExternalLink size={9} />
                            </a>
                        )}
                    </p>
                )}
            </header>

            <div className="flex-1 grid grid-cols-1 lg:grid-cols-2 gap-4 px-6 pb-6 max-w-6xl w-full overflow-hidden">
                {/* Current mods */}
                <section className="card p-4 flex flex-col overflow-hidden">
                    <header className="flex items-center gap-2 mb-3 shrink-0">
                        <Box size={14} className="text-(--accent-light)" />
                        <h2 className="text-sm font-medium text-(--base-09)">In this version ({mods.length})</h2>
                    </header>
                    <div className="flex-1 overflow-y-auto space-y-2">
                        {mods.length === 0 ? (
                            <p className="text-xs text-(--base-06) text-center py-8">No mods yet. Add some from the search panel →</p>
                        ) : (
                            mods.map(m => (
                                <article key={m.id} className="flex items-center gap-3 p-2 rounded-md border border-(--base-04)">
                                    <Box size={14} className="text-(--accent-light) shrink-0" />
                                    <div className="min-w-0 flex-1">
                                        <div className="text-sm font-medium text-(--base-09) truncate">{m.title || m.fileName}</div>
                                        <div className="text-[10px] font-mono text-(--base-06) truncate">{m.fileName}</div>
                                    </div>
                                    <span className={`mono-label px-1.5 rounded-sm ${
                                        m.side === 'client' ? 'bg-(--warning-ghost) text-(--warning-light)' :
                                        m.side === 'server' ? 'bg-(--success-ghost) text-(--success-light)' :
                                        'bg-(--base-03) text-(--base-07)'
                                    }`}>{m.side}</span>
                                    {!m.required && (
                                        <span className="mono-label bg-(--base-03) px-1.5 rounded-sm text-(--base-06)">optional</span>
                                    )}
                                    <button
                                        onClick={() => handleRemove(m)}
                                        className="btn btn-secondary btn-sm shrink-0 disabled:opacity-40 disabled:cursor-not-allowed"
                                        title={disabled ? (isFrozen ? 'Version is frozen' : 'Modpack authoring is disabled') : 'Remove'}
                                        disabled={disabled}
                                    >
                                        <Trash2 size={11} className="text-(--error)" />
                                    </button>
                                </article>
                            ))
                        )}
                    </div>
                </section>

                {/* Modrinth search */}
                <section className="card p-4 flex flex-col overflow-hidden">
                    <header className="flex items-center gap-2 mb-3 shrink-0">
                        <Search size={14} className="text-(--accent-light)" />
                        <h2 className="text-sm font-medium text-(--base-09)">Add from Modrinth</h2>
                    </header>
                    <div className="relative mb-3 shrink-0">
                        <Search size={12} className="absolute left-3 top-1/2 -translate-y-1/2 text-(--base-05)" />
                        <input
                            type="text"
                            value={query}
                            onChange={e => setQuery(e.target.value)}
                            placeholder="Search mods…"
                            className="input-field w-full pl-8 text-sm"
                        />
                    </div>
                    <div className="flex-1 overflow-y-auto space-y-1.5">
                        {searching ? (
                            <p className="text-xs text-(--base-06) text-center py-6 flex items-center justify-center gap-1.5">
                                <RefreshCw size={11} className="animate-spin" />
                                Searching…
                            </p>
                        ) : searchHits.length === 0 ? (
                            <p className="text-xs text-(--base-06) text-center py-6">No matches.</p>
                        ) : (
                            searchHits.map(hit => (
                                <div key={hit.project_id}>
                                    <button
                                        onClick={() => handleExpand(hit)}
                                        className="w-full flex items-center gap-2 p-2 rounded-md border border-(--base-04) hover:border-(--accent-border) text-left"
                                    >
                                        {hit.icon_url ? (
                                            // eslint-disable-next-line @next/next/no-img-element
                                            <img src={hit.icon_url} alt="" className="w-8 h-8 rounded-sm shrink-0" />
                                        ) : (
                                            <div className="w-8 h-8 rounded-sm bg-(--base-03) flex items-center justify-center shrink-0">
                                                <Package size={12} className="text-(--base-05)" />
                                            </div>
                                        )}
                                        <div className="min-w-0 flex-1">
                                            <div className="text-sm font-medium text-(--base-09) truncate">{hit.title}</div>
                                            <div className="text-[10px] font-mono text-(--base-06) truncate">
                                                by {hit.author} · {hit.downloads.toLocaleString()}
                                            </div>
                                        </div>
                                        {installedIds.has(hit.project_id) && (
                                            <span className="mono-label bg-(--success-ghost) text-(--success-light) px-1.5 rounded-sm shrink-0">added</span>
                                        )}
                                    </button>
                                    {expandedSlug === hit.slug && (
                                        <div className="ml-10 mt-1 mb-2 space-y-1">
                                            {versionsLoading ? (
                                                <p className="text-xs text-(--base-06) py-1 flex items-center gap-1">
                                                    <RefreshCw size={10} className="animate-spin" />
                                                    Loading versions…
                                                </p>
                                            ) : versions.length === 0 ? (
                                                <p className="text-xs text-(--base-06) py-1">
                                                    <AlertTriangle size={10} className="inline mr-1 text-(--warning-light)" />
                                                    No versions match this pack's filter.
                                                </p>
                                            ) : (
                                                versions.slice(0, 6).map(v => (
                                                    <button
                                                        key={v.id}
                                                        onClick={() => handleAddMod(hit, v)}
                                                        className="w-full flex items-center gap-2 px-2 py-1.5 rounded-md border border-(--base-04) hover:border-(--accent-border) hover:bg-(--accent-ghost)/30 text-left disabled:opacity-40 disabled:cursor-not-allowed disabled:hover:border-(--base-04) disabled:hover:bg-transparent"
                                                        disabled={disabled}
                                                        title={disabled ? (isFrozen ? 'Version is frozen — create a new version to modify mods' : 'Modpack authoring is disabled') : undefined}
                                                    >
                                                        <Plus size={10} className="text-(--accent-light) shrink-0" />
                                                        <div className="min-w-0 flex-1">
                                                            <div className="text-xs text-(--base-09)">{v.version_number}</div>
                                                            <div className="text-[10px] font-mono text-(--base-06) truncate">
                                                                {v.version_type} · {v.loaders.join(', ')} · MC {v.game_versions.join(', ')}
                                                            </div>
                                                        </div>
                                                    </button>
                                                ))
                                            )}
                                        </div>
                                    )}
                                </div>
                            ))
                        )}
                    </div>
                </section>
            </div>

            {toast && (
                <div className="toast-container">
                    <div className="toast">
                        <div className={`toast-bar ${toast.ok ? 'bg-(--success-light)' : 'bg-(--error-light)'}`}></div>
                        {toast.ok ? <CircleCheck size={14} /> : <CircleAlert size={14} />}
                        <span className="text-sm text-(--base-09)">{toast.msg}</span>
                    </div>
                </div>
            )}
        </main>
    );
}
