"use client";

import React, { useCallback, useEffect, useMemo, useState } from 'react';
import { useParams } from 'next/navigation';
import {
    Package, Search, Settings2, Download, Trash2, ExternalLink,
    CircleCheck, CircleAlert, AlertTriangle, RefreshCw, Filter,
    ChevronDown, Box,
} from 'lucide-react';
import { useAppData } from '@/lib/AppDataContext';
import { Skeleton, SkeletonText, SkeletonCard } from '@/components/Skeleton';
import { systemEvents } from '@/lib/systemEvents';
import {
    searchModrinth, getModrinthProject, getModrinthVersions,
    listInstalledMods, installMod, uninstallMod, pickPrimaryFile,
    type ModrinthSearchHit, type ModrinthSearchResult, type ModrinthProject,
    type ModrinthVersion, type InstalledMod,
} from '@/lib/api/modrinth';

// Phase 10 — Modrinth Content tab. Default mode auto-filters to the server's
// loader + MC version; an "Advanced" toggle reveals the full Modrinth-style
// filter sidebar. Installed mods are listed inline so the user can uninstall
// without flipping tabs.

type Section = 'browse' | 'installed';

const LOADER_OPTIONS = [
    'paper', 'spigot', 'bukkit', 'purpur',
    'fabric', 'forge', 'quilt', 'neoforge',
    'velocity', 'waterfall', 'bungeecord',
];

const PROJECT_TYPE_FOR_LOADER: Record<string, 'mod' | 'plugin'> = {
    paper: 'plugin', spigot: 'plugin', bukkit: 'plugin', purpur: 'plugin',
    velocity: 'plugin', waterfall: 'plugin', bungeecord: 'plugin',
    fabric: 'mod', forge: 'mod', quilt: 'mod', neoforge: 'mod',
};

export default function ServerContentPage() {
    const params = useParams();
    const { servers } = useAppData();
    const serverId = Number(params?.id);
    const server = servers.find(s => s.id === serverId);

    const defaultLoader = (server?.installerType || '').toLowerCase();
    const defaultMcVersion = server?.minecraftVersion || '';

    const [section, setSection] = useState<Section>('browse');
    const [query, setQuery] = useState('');
    const [advanced, setAdvanced] = useState(false);
    const [filterLoaders, setFilterLoaders] = useState<string[]>(defaultLoader ? [defaultLoader] : []);
    const [filterVersions, setFilterVersions] = useState<string[]>(defaultMcVersion ? [defaultMcVersion] : []);
    const [searchResult, setSearchResult] = useState<ModrinthSearchResult | null>(null);
    const [searchLoading, setSearchLoading] = useState(false);

    const [installed, setInstalled] = useState<InstalledMod[]>([]);
    const installedById = useMemo(() => new Set(installed.map(m => m.modrinthProjectId)), [installed]);

    const [projectDetail, setProjectDetail] = useState<ModrinthProject | null>(null);
    const [projectVersions, setProjectVersions] = useState<ModrinthVersion[]>([]);
    const [projectLoading, setProjectLoading] = useState(false);

    const [toast, setToast] = useState<{ msg: string; ok: boolean } | null>(null);

    const showToast = useCallback((msg: string, ok = true) => {
        setToast({ msg, ok });
        setTimeout(() => setToast(null), 3500);
    }, []);

    const projectType = useMemo<'mod' | 'plugin' | undefined>(() => {
        if (advanced || !defaultLoader) return undefined;
        return PROJECT_TYPE_FOR_LOADER[defaultLoader];
    }, [advanced, defaultLoader]);

    // ----- Browse search -----

    const runSearch = useCallback(async () => {
        setSearchLoading(true);
        const res = await searchModrinth({
            query: query.trim() || undefined,
            loaders: filterLoaders.length ? filterLoaders : undefined,
            versions: filterVersions.length ? filterVersions : undefined,
            projectType,
            limit: 20,
            index: 'relevance',
        });
        setSearchResult(res);
        setSearchLoading(false);
    }, [query, filterLoaders, filterVersions, projectType]);

    // Debounce typed search input.
    useEffect(() => {
        const t = setTimeout(runSearch, 350);
        return () => clearTimeout(t);
    }, [runSearch]);

    // ----- Installed mods -----

    const refreshInstalled = useCallback(async () => {
        if (!serverId) return;
        const list = await listInstalledMods(serverId);
        setInstalled(list);
    }, [serverId]);

    useEffect(() => { refreshInstalled(); }, [refreshInstalled]);

    // SSE — server_mods.changed fires when another session installs/uninstalls.
    useEffect(() => {
        const unsub = systemEvents.on('server_mods.changed', (evt) => {
            const sid = (evt.payload as any)?.serverId;
            if (sid === undefined || sid === serverId) refreshInstalled();
        });
        return () => { unsub(); };
    }, [serverId, refreshInstalled]);

    // ----- Project detail modal -----

    const openProjectDetail = useCallback(async (slug: string) => {
        setProjectLoading(true);
        setProjectDetail(null);
        setProjectVersions([]);
        const [p, versions] = await Promise.all([
            getModrinthProject(slug),
            getModrinthVersions(slug, {
                loaders: advanced ? undefined : filterLoaders,
                versions: advanced ? undefined : filterVersions,
            }),
        ]);
        setProjectDetail(p);
        setProjectVersions(versions);
        setProjectLoading(false);
    }, [advanced, filterLoaders, filterVersions]);

    const closeProjectDetail = () => {
        setProjectDetail(null);
        setProjectVersions([]);
    };

    // ----- Install / uninstall -----

    const handleInstall = async (project: { id: string; slug: string; title: string }, version: ModrinthVersion) => {
        const file = pickPrimaryFile(version);
        if (!file) { showToast('Version has no downloadable file', false); return; }
        const res = await installMod(serverId, {
            projectId: project.id,
            projectSlug: project.slug,
            versionId: version.id,
            title: project.title,
            fileName: file.filename,
            downloadUrl: file.url,
            sha512: file.hashes.sha512,
        });
        if (res.success) {
            showToast(`Installing ${project.title}…`, true);
            refreshInstalled();
        } else {
            showToast(res.message || 'Install failed', false);
        }
    };

    const handleUninstall = async (m: InstalledMod) => {
        const res = await uninstallMod(serverId, m.id);
        if (res.success) {
            showToast(`Removed ${m.title || m.fileName}`, true);
            refreshInstalled();
        } else {
            showToast(res.message || 'Uninstall failed', false);
        }
    };

    if (!server) return null;

    return (
        <main className="flex-1 flex flex-col p-6 gap-4 overflow-hidden">
            <header className="flex items-center gap-3 shrink-0">
                <Package size={20} className="text-(--accent-light)" />
                <h1 className="text-base font-display font-semibold text-(--base-09)">Content</h1>
                <span className="text-xs text-(--base-06) hidden sm:inline">
                    Modrinth-powered mods + plugins
                    {!advanced && (defaultLoader || defaultMcVersion) && (
                        <> · auto-filter: <code className="font-mono">{[defaultLoader, defaultMcVersion].filter(Boolean).join(' · ')}</code></>
                    )}
                </span>
                <div className="ml-auto flex items-center gap-2">
                    <button
                        onClick={() => setAdvanced(a => !a)}
                        className={`btn btn-secondary btn-sm ${advanced ? 'text-(--accent-light)' : ''}`}
                        title="Toggle advanced filters"
                    >
                        <Filter size={12} />
                        {advanced ? 'Simple' : 'Advanced'}
                    </button>
                </div>
            </header>

            {/* Section strip */}
            <nav className="flex gap-1 shrink-0 border-b border-(--base-03)">
                {([
                    { id: 'browse' as const, label: 'Browse', Icon: Search },
                    { id: 'installed' as const, label: `Installed (${installed.length})`, Icon: Box },
                ]).map(({ id, label, Icon }) => (
                    <button
                        key={id}
                        onClick={() => setSection(id)}
                        className={`flex items-center gap-1.5 px-3 py-2 text-xs font-medium border-b-2 transition-colors ${
                            section === id
                                ? 'border-(--accent) text-(--accent-light)'
                                : 'border-transparent text-(--base-07) hover:text-(--base-09) hover:border-(--base-04)'
                        }`}
                    >
                        <Icon size={12} />
                        {label}
                    </button>
                ))}
            </nav>

            {section === 'browse' && (
                <div className="flex-1 flex gap-4 overflow-hidden">
                    {/* Sidebar (Advanced only) */}
                    {advanced && (
                        <aside className="w-56 shrink-0 overflow-y-auto card p-3 space-y-4">
                            <div>
                                <label className="input-label">Loaders</label>
                                <div className="mt-1 space-y-1 max-h-44 overflow-y-auto">
                                    {LOADER_OPTIONS.map(l => {
                                        const on = filterLoaders.includes(l);
                                        return (
                                            <label key={l} className="flex items-center gap-2 text-xs cursor-pointer hover:text-(--base-09)">
                                                <input
                                                    type="checkbox"
                                                    checked={on}
                                                    onChange={() => setFilterLoaders(prev => on ? prev.filter(x => x !== l) : [...prev, l])}
                                                />
                                                {l}
                                            </label>
                                        );
                                    })}
                                </div>
                            </div>
                            <div>
                                <label className="input-label">Game versions (comma)</label>
                                <input
                                    type="text"
                                    value={filterVersions.join(',')}
                                    onChange={e => setFilterVersions(e.target.value.split(',').map(s => s.trim()).filter(Boolean))}
                                    className="input-field input-mono w-full text-xs"
                                    placeholder="1.20.2, 1.21"
                                />
                            </div>
                        </aside>
                    )}

                    {/* Main column */}
                    <div className="flex-1 flex flex-col overflow-hidden">
                        <div className="relative shrink-0 mb-3">
                            <Search size={13} className="absolute left-3 top-1/2 -translate-y-1/2 text-(--base-05)" />
                            <input
                                type="text"
                                value={query}
                                onChange={e => setQuery(e.target.value)}
                                placeholder="Search mods, plugins, datapacks…"
                                className="input-field w-full pl-8"
                            />
                        </div>

                        {!advanced && defaultLoader && PROJECT_TYPE_FOR_LOADER[defaultLoader] === undefined && (
                            <div className="shrink-0 mb-2 px-3 py-2 rounded-md bg-(--warning-ghost) border border-(--warning)/30 text-(--warning-light) text-xs flex items-start gap-2">
                                <AlertTriangle size={12} className="mt-0.5 shrink-0" />
                                Unknown loader <code className="font-mono">{defaultLoader}</code> — results aren't pre-filtered by mod/plugin type.
                            </div>
                        )}

                        <div className="flex-1 overflow-y-auto">
                            {searchLoading ? (
                                <div className="text-center py-12 text-sm text-(--base-06)">Searching…</div>
                            ) : !searchResult || searchResult.hits.length === 0 ? (
                                <div className="text-center py-12 text-sm text-(--base-06)">No projects match.</div>
                            ) : (
                                <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3">
                                    {searchResult.hits.map(hit => (
                                        <ProjectCard
                                            key={hit.project_id}
                                            hit={hit}
                                            installed={installedById.has(hit.project_id)}
                                            onClick={() => openProjectDetail(hit.slug)}
                                        />
                                    ))}
                                </div>
                            )}
                        </div>
                    </div>
                </div>
            )}

            {section === 'installed' && (
                <div className="flex-1 overflow-y-auto">
                    {installed.length === 0 ? (
                        <div className="text-center py-12 text-sm text-(--base-06)">No mods installed.</div>
                    ) : (
                        <div className="space-y-2">
                            {installed.map(m => (
                                <article key={m.id} className="card p-3 flex items-start gap-3">
                                    <div className="w-10 h-10 rounded-md bg-(--base-03) flex items-center justify-center shrink-0">
                                        <Box size={16} className="text-(--accent-light)" />
                                    </div>
                                    <div className="min-w-0 flex-1">
                                        <div className="text-sm font-medium text-(--base-09)">{m.title || m.fileName}</div>
                                        <div className="text-xs text-(--base-06) font-mono">{m.fileName}</div>
                                        <div className="text-xs text-(--base-06) mt-0.5">
                                            Installed {new Date(m.installedAt).toLocaleString()}
                                        </div>
                                    </div>
                                    <div className="flex items-center gap-1 shrink-0">
                                        <a
                                            href={`https://modrinth.com/project/${m.modrinthProjectSlug || m.modrinthProjectId}`}
                                            target="_blank"
                                            rel="noopener noreferrer"
                                            className="btn btn-secondary btn-sm"
                                            title="View on Modrinth"
                                        >
                                            <ExternalLink size={12} />
                                        </a>
                                        <button onClick={() => handleUninstall(m)} className="btn btn-secondary btn-sm">
                                            <Trash2 size={12} className="text-(--error)" />
                                            Remove
                                        </button>
                                    </div>
                                </article>
                            ))}
                        </div>
                    )}
                </div>
            )}

            {/* Project detail modal */}
            {(projectDetail || projectLoading) && (
                <div className="modal-overlay animate-fade-in" onClick={closeProjectDetail}>
                    <div className="modal-panel w-full max-w-2xl max-h-[85vh] flex flex-col" onClick={e => e.stopPropagation()}>
                        {projectLoading || !projectDetail ? (
                            <div className="modal-body py-6 space-y-3">
                                <div className="flex items-start gap-3">
                                    <Skeleton className="w-12 h-12 rounded-md shrink-0" />
                                    <div className="flex-1 space-y-2">
                                        <SkeletonText width="w-1/2" className="h-4" />
                                        <SkeletonText width="w-3/4" className="h-2.5" />
                                    </div>
                                </div>
                                <SkeletonCard height="h-32" />
                            </div>
                        ) : (
                            <>
                                <div className="modal-header flex items-start gap-3">
                                    {projectDetail.icon_url && (
                                        // eslint-disable-next-line @next/next/no-img-element
                                        <img src={projectDetail.icon_url} alt="" className="w-12 h-12 rounded-md shrink-0" />
                                    )}
                                    <div className="min-w-0 flex-1">
                                        <h3 className="modal-title">{projectDetail.title}</h3>
                                        <p className="text-xs text-(--base-06) line-clamp-2">{projectDetail.description}</p>
                                        <div className="flex items-center gap-3 mt-1 text-[10px] font-mono text-(--base-06)">
                                            <span>{projectDetail.downloads.toLocaleString()} downloads</span>
                                            <a href={`https://modrinth.com/project/${projectDetail.slug}`} target="_blank" rel="noopener noreferrer" className="text-(--accent-light) flex items-center gap-1">
                                                modrinth.com <ExternalLink size={9} />
                                            </a>
                                        </div>
                                    </div>
                                </div>
                                <div className="modal-body overflow-y-auto">
                                    <div className="space-y-2">
                                        <h4 className="input-label">Versions</h4>
                                        {projectVersions.length === 0 ? (
                                            <p className="text-xs text-(--base-06)">No compatible versions match the filter.</p>
                                        ) : (
                                            projectVersions.slice(0, 15).map(v => (
                                                <div key={v.id} className="flex items-center gap-2 p-2 rounded-md border border-(--base-04)">
                                                    <div className="min-w-0 flex-1">
                                                        <div className="text-sm font-medium text-(--base-09)">{v.version_number}</div>
                                                        <div className="text-[10px] font-mono text-(--base-06)">
                                                            {v.version_type} · {v.loaders.join(', ')} · MC {v.game_versions.join(', ')}
                                                        </div>
                                                    </div>
                                                    <button
                                                        onClick={() => handleInstall(projectDetail, v)}
                                                        className="btn btn-primary btn-sm"
                                                    >
                                                        <Download size={11} />
                                                        Install
                                                    </button>
                                                </div>
                                            ))
                                        )}
                                    </div>
                                </div>
                                <div className="modal-footer">
                                    <button onClick={closeProjectDetail} className="btn btn-secondary">Close</button>
                                </div>
                            </>
                        )}
                    </div>
                </div>
            )}

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

function ProjectCard({
    hit,
    installed,
    onClick,
}: {
    hit: ModrinthSearchHit;
    installed: boolean;
    onClick: () => void;
}) {
    return (
        <button onClick={onClick} className="card p-3 text-left hover:border-(--accent-border) transition-colors group flex flex-col gap-2">
            <div className="flex items-start gap-2">
                {hit.icon_url ? (
                    // eslint-disable-next-line @next/next/no-img-element
                    <img src={hit.icon_url} alt="" className="w-10 h-10 rounded-md shrink-0" />
                ) : (
                    <div className="w-10 h-10 rounded-md bg-(--base-03) flex items-center justify-center shrink-0">
                        <Package size={14} className="text-(--base-05)" />
                    </div>
                )}
                <div className="min-w-0 flex-1">
                    <div className="font-medium text-sm text-(--base-09) truncate">{hit.title}</div>
                    <div className="text-[10px] font-mono text-(--base-06) truncate">by {hit.author}</div>
                </div>
                {installed && (
                    <span className="mono-label bg-(--success-ghost) text-(--success-light) px-1.5 rounded-sm shrink-0">installed</span>
                )}
            </div>
            <p className="text-xs text-(--base-07) line-clamp-2">{hit.description}</p>
            <div className="text-[10px] font-mono text-(--base-06) flex items-center gap-2 mt-auto">
                <Download size={10} />
                {hit.downloads.toLocaleString()}
            </div>
        </button>
    );
}
