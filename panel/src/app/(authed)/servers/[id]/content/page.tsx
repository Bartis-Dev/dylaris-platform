"use client";

import { useCallback, useEffect, useMemo, useState, type KeyboardEvent, type MouseEvent } from 'react';
import { useParams } from 'next/navigation';
import {
    Package, Search, Download, Trash2, ExternalLink,
    CircleCheck, CircleAlert, AlertTriangle, Filter, Box, X, RefreshCw,
} from 'lucide-react';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import { useAppData } from '@/lib/AppDataContext';
import { Skeleton, SkeletonText, SkeletonCard } from '@/components/Skeleton';
import { systemEvents } from '@/lib/systemEvents';
import {
    searchModrinth, getModrinthProject, getModrinthVersions, getModrinthCategories,
    listInstalledMods, installMod, uninstallMod, pickPrimaryFile,
    getServerModpackContents,
    type ModrinthSearchHit, type ModrinthSearchResult, type ModrinthProject,
    type ModrinthVersion, type ModrinthCategory, type InstalledMod,
} from '@/lib/api/modrinth';
import { pickNewestMatchingVersion, compareInstalledVsLatest, type ModStatus } from '@/lib/modVersionCompare';

// Modrinth Content tab, Modrinth-style layout: an always-visible category
// sidebar (with the loader + MC-version filters below it, gated behind an
// "Advanced" toggle), a single-column result list, and a detail column on the
// right (description with a short/full switch, plus the version list with the
// newest build for this server's MC version highlighted).

type Section = 'browse' | 'installed';
// Row status shown in the browse list: 'checking' is a UI-only state (the
// matching-version fetch for an installed row is still in flight) layered on
// top of the pure ModStatus from lib/modVersionCompare.
type RowStatus = ModStatus | 'checking';

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

    // Category sidebar (single-select). Categories are proxied from Modrinth.
    const [categories, setCategories] = useState<ModrinthCategory[]>([]);
    const [categoriesLoading, setCategoriesLoading] = useState(true);
    const [selectedCategory, setSelectedCategory] = useState<string | null>(null);

    const [installed, setInstalled] = useState<InstalledMod[]>([]);
    const installedByProject = useMemo(() => {
        const m = new Map<string, InstalledMod>();
        for (const im of installed) m.set(im.modrinthProjectId, im);
        return m;
    }, [installed]);

    // Per-row "is there a newer matching build" state for BROWSE rows that are
    // already installed. Keyed by Modrinth project id; a missing entry means
    // "not fetched yet" (renders as 'checking'), distinct from an empty array
    // (fetched, nothing matched the current filter). Only installed rows are
    // fetched eagerly — not-installed rows resolve their install target
    // on-click, same lazy pattern as opening the detail column.
    const [installedRowVersions, setInstalledRowVersions] = useState<Map<string, ModrinthVersion[]>>(new Map());
    // Project ids with an install/remove/update request in flight, for
    // per-row spinner + disabled state.
    const [busyProjects, setBusyProjects] = useState<Set<string>>(new Set());

    // Modpack cross-check: projectId -> the pack's version of that mod. Non-empty
    // only for a server installed from a modpack; drives the banner + warnings.
    const [packByProject, setPackByProject] = useState<Map<string, { versionId: string; versionNumber: string }>>(new Map());

    const [selectedSlug, setSelectedSlug] = useState<string | null>(null);
    const [projectDetail, setProjectDetail] = useState<ModrinthProject | null>(null);
    const [projectVersions, setProjectVersions] = useState<ModrinthVersion[]>([]);
    const [projectLoading, setProjectLoading] = useState(false);
    const [descMode, setDescMode] = useState<'short' | 'full'>('short');

    const [toast, setToast] = useState<{ msg: string; ok: boolean } | null>(null);

    const showToast = useCallback((msg: string, ok = true) => {
        setToast({ msg, ok });
        setTimeout(() => setToast(null), 3500);
    }, []);

    const projectType = useMemo<'mod' | 'plugin' | undefined>(() => {
        if (advanced || !defaultLoader) return undefined;
        return PROJECT_TYPE_FOR_LOADER[defaultLoader];
    }, [advanced, defaultLoader]);

    // ----- Categories -----

    useEffect(() => {
        let cancelled = false;
        setCategoriesLoading(true);
        getModrinthCategories().then(cats => {
            if (cancelled) return;
            setCategories(cats);
            setCategoriesLoading(false);
        });
        return () => { cancelled = true; };
    }, []);

    // Show the categories for this server's content type (mod/plugin), falling
    // back to the full list if none match, and de-duplicate by name.
    const browseProjectType = PROJECT_TYPE_FOR_LOADER[defaultLoader] || 'mod';
    const visibleCategories = useMemo(() => {
        const forType = categories.filter(c => c.project_type === browseProjectType);
        const list = forType.length ? forType : categories;
        const seen = new Set<string>();
        return list.filter(c => (seen.has(c.name) ? false : (seen.add(c.name), true)));
    }, [categories, browseProjectType]);

    // ----- Browse search -----

    const runSearch = useCallback(async () => {
        setSearchLoading(true);
        const res = await searchModrinth({
            query: query.trim() || undefined,
            loaders: filterLoaders.length ? filterLoaders : undefined,
            versions: filterVersions.length ? filterVersions : undefined,
            categories: selectedCategory ? [selectedCategory] : undefined,
            projectType,
            limit: 20,
            index: 'relevance',
        });
        setSearchResult(res);
        setSearchLoading(false);
    }, [query, filterLoaders, filterVersions, selectedCategory, projectType]);

    // Debounce typed search input (and category/filter changes).
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

    // Load the modpack snapshot once per server. Fail-open: on any error the map
    // stays empty and the tab behaves exactly as a non-modpack server.
    useEffect(() => {
        if (!serverId) return;
        let cancelled = false;
        (async () => {
            const contents = await getServerModpackContents(serverId);
            if (cancelled) return;
            const next = new Map<string, { versionId: string; versionNumber: string }>();
            for (const c of contents) {
                next.set(c.modrinthProjectId, { versionId: c.modrinthVersionId, versionNumber: c.modrinthVersionNumber });
            }
            setPackByProject(next);
        })();
        return () => { cancelled = true; };
    }, [serverId]);

    // SSE — server_mods.changed fires when another session installs/uninstalls.
    useEffect(() => {
        const unsub = systemEvents.on('server_mods.changed', (evt) => {
            const sid = (evt.payload as any)?.serverId;
            if (sid === undefined || sid === serverId) refreshInstalled();
        });
        return () => { unsub(); };
    }, [serverId, refreshInstalled]);

    // For browse rows that are already installed, fetch the version list
    // filtered by the current loader + MC-version filter, so the row can show
    // "Update available" without the user having to open the mod first. Scoped
    // to the (small) installed-and-currently-visible subset — NOT every
    // visible row — since Modrinth version lists are otherwise only fetched
    // on demand (mod-open / install-click), and eagerly fetching for all ~20
    // search hits on every keystroke would be a heavy, mostly-wasted burst of
    // calls against not-yet-installed projects nobody asked about.
    useEffect(() => {
        if (!searchResult) return;
        const hitsToCheck = searchResult.hits.filter(h => installedByProject.has(h.project_id));
        if (hitsToCheck.length === 0) return;

        // Drop stale entries for these ids immediately so a filter change
        // shows 'checking' rather than a stale (possibly wrong) status while
        // the refetch is in flight.
        setInstalledRowVersions(prev => {
            const next = new Map(prev);
            for (const h of hitsToCheck) next.delete(h.project_id);
            return next;
        });

        let cancelled = false;
        const loaders = filterLoaders.length ? filterLoaders : undefined;
        const versions = filterVersions.length ? filterVersions : undefined;
        (async () => {
            const entries = await Promise.all(hitsToCheck.map(async h => {
                const list = await getModrinthVersions(h.slug, { loaders, versions });
                return [h.project_id, list] as const;
            }));
            if (cancelled) return;
            setInstalledRowVersions(prev => {
                const next = new Map(prev);
                for (const [id, list] of entries) next.set(id, list);
                return next;
            });
        })();
        return () => { cancelled = true; };
    }, [searchResult, installedByProject, filterLoaders, filterVersions]);

    // ----- Project detail column -----

    const openProjectDetail = useCallback(async (slug: string) => {
        setSelectedSlug(slug);
        setDescMode('short');
        setProjectLoading(true);
        setProjectDetail(null);
        setProjectVersions([]);
        // Fetch versions filtered by the active loader only (not MC version), so
        // the full version history for this loader is visible and we can
        // highlight the newest build matching the server's MC version.
        const [p, versions] = await Promise.all([
            getModrinthProject(slug),
            getModrinthVersions(slug, { loaders: advanced ? undefined : filterLoaders }),
        ]);
        setProjectDetail(p);
        setProjectVersions(versions);
        setProjectLoading(false);
    }, [advanced, filterLoaders]);

    const closeProjectDetail = () => {
        setSelectedSlug(null);
        setProjectDetail(null);
        setProjectVersions([]);
    };

    const sortedVersions = useMemo(
        () => [...projectVersions].sort((a, b) => b.date_published.localeCompare(a.date_published)),
        [projectVersions],
    );
    // Newest build (they are date-sorted) whose game_versions covers this
    // server's MC version — the one we recommend + highlight.
    const highlightVersionId = useMemo(() => {
        if (!defaultMcVersion) return null;
        return sortedVersions.find(v => v.game_versions.includes(defaultMcVersion))?.id ?? null;
    }, [sortedVersions, defaultMcVersion]);

    // ----- Install / uninstall -----

    // Advisory, non-blocking modpack cross-check. Differentiated by wording
    // (window.confirm cannot carry visual tone): the same-version case is
    // informational, the other two lead with "Warning:". Returns false if the
    // user cancels (or the confirm should block the install). Shared by every
    // install path (detail-pane version list, row Install, row Update) so a
    // cancelled confirm is always evaluated BEFORE any destructive action
    // (e.g. the row Update flow must not remove the old file first).
    const confirmModpackCrossCheck = (project: ModrinthProject, version: ModrinthVersion): boolean => {
        const inPack = packByProject.get(project.id);
        if (inPack) {
            if (inPack.versionId === version.id) {
                return window.confirm(`"${project.title}" is already in this server's modpack at this version. Install it again anyway?`);
            }
            const packVer = inPack.versionNumber || 'a different version';
            return window.confirm(`Warning: this server's modpack ships ${packVer} of "${project.title}". Installing ${version.version_number} may stop the server from starting, or leave players on the pack version unable to connect. Install anyway?`);
        }
        if (packByProject.size > 0 && project.client_side === 'required') {
            return window.confirm(`Warning: "${project.title}" must run on each player's client too, or they will not be able to connect. It is not part of the distributed modpack. Install anyway?`);
        }
        return true;
    };

    // Fires the actual install call (no confirm — callers run
    // confirmModpackCrossCheck first).
    const doInstall = async (project: ModrinthProject, version: ModrinthVersion) => {
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

    const handleInstall = async (project: ModrinthProject, version: ModrinthVersion) => {
        if (!confirmModpackCrossCheck(project, version)) return;
        await doInstall(project, version);
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

    // ----- Per-row browse actions (install / remove / update) -----

    const withBusy = async (projectId: string, fn: () => Promise<void>) => {
        setBusyProjects(prev => new Set(prev).add(projectId));
        try {
            await fn();
        } finally {
            setBusyProjects(prev => {
                const next = new Set(prev);
                next.delete(projectId);
                return next;
            });
        }
    };

    // Row "Install": resolve the newest version matching the current filter
    // for this project (fetched lazily, on click — not eagerly for every
    // browse row) and install it.
    const handleRowInstall = (hit: ModrinthSearchHit) => withBusy(hit.project_id, async () => {
        const loaders = filterLoaders.length ? filterLoaders : undefined;
        const versions = filterVersions.length ? filterVersions : undefined;
        const [project, candidates] = await Promise.all([
            getModrinthProject(hit.slug),
            getModrinthVersions(hit.slug, { loaders, versions }),
        ]);
        if (!project) { showToast('Failed to load project details', false); return; }
        const latest = pickNewestMatchingVersion(candidates, { loaders: filterLoaders, mcVersions: filterVersions });
        if (!latest) { showToast(`No version of "${hit.title}" matches the current loader / MC-version filter`, false); return; }
        await handleInstall(project, latest);
    });

    const handleRowRemove = (installedMod: InstalledMod) => withBusy(installedMod.modrinthProjectId, async () => {
        await handleUninstall(installedMod);
    });

    // Row "Update": remove the old file THEN install the newer one, so the
    // mods/plugins folder never ends up holding both jars for the same
    // project. The modpack cross-check confirm runs BEFORE the removal so a
    // cancelled confirm never leaves the server with the mod uninstalled.
    const handleRowUpdate = (hit: ModrinthSearchHit, installedMod: InstalledMod, latest: ModrinthVersion) =>
        withBusy(hit.project_id, async () => {
            const project = await getModrinthProject(hit.slug);
            if (!project) { showToast('Failed to load project details', false); return; }
            if (!confirmModpackCrossCheck(project, latest)) return;
            const removeRes = await uninstallMod(serverId, installedMod.id);
            if (!removeRes.success) {
                showToast(removeRes.message || 'Update failed: could not remove the old version', false);
                return;
            }
            await doInstall(project, latest);
        });

    // ----- Advanced toggle -----

    const handleAdvancedToggle = () => {
        setAdvanced(prev => {
            const next = !prev;
            if (!next) {
                // Turning Advanced off resets the loader + MC-version filters
                // back to the server's stored values (not just "clears" them).
                setFilterLoaders(defaultLoader ? [defaultLoader] : []);
                setFilterVersions(defaultMcVersion ? [defaultMcVersion] : []);
            }
            return next;
        });
    };

    if (!server) return null;

    const unknownLoader = !advanced && defaultLoader && PROJECT_TYPE_FOR_LOADER[defaultLoader] === undefined;

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
                        className={`flex items-center gap-1.5 px-3 py-2 text-sm font-medium border-b-2 transition-colors ${
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

            {packByProject.size > 0 && (
                <div className="shrink-0 flex items-start gap-2 px-3 py-2 rounded-md border border-(--base-04) bg-(--base-03) text-xs text-(--base-07)">
                    <Package size={13} className="mt-0.5 shrink-0 text-(--accent-light)" />
                    <span>
                        This server runs a modpack. Mods you add here are not part of the distributed pack;
                        players who lack a required client-side mod may fail to connect.
                    </span>
                </div>
            )}

            {section === 'browse' && (
                <div className="flex-1 flex gap-4 overflow-hidden min-h-0">
                    {/* Category sidebar (always visible) + Advanced-gated filters */}
                    <aside className="w-52 shrink-0 overflow-y-auto card p-3 space-y-4">
                        <div>
                            <label className="input-label">Categories</label>
                            <div className="mt-1.5 space-y-0.5">
                                {categoriesLoading ? (
                                    <div className="space-y-1.5 py-1">
                                        {[0, 1, 2, 3, 4, 5].map(i => <SkeletonText key={i} width="w-full" className="h-3.5" />)}
                                    </div>
                                ) : visibleCategories.length === 0 ? (
                                    <p className="text-xs text-(--base-06) italic">Categories unavailable.</p>
                                ) : (
                                    visibleCategories.map(cat => {
                                        const on = selectedCategory === cat.name;
                                        return (
                                            <button
                                                key={cat.name}
                                                onClick={() => setSelectedCategory(on ? null : cat.name)}
                                                className={`w-full flex items-center gap-2 px-2 py-1 rounded-md text-xs capitalize transition-colors ${
                                                    on
                                                        ? 'bg-(--accent-ghost) text-(--accent-light)'
                                                        : 'text-(--base-07) hover:bg-(--base-03) hover:text-(--base-09)'
                                                }`}
                                            >
                                                {/* Category icon is a trusted inline SVG proxied from Modrinth. */}
                                                <span
                                                    className="shrink-0 [&_svg]:w-3.5 [&_svg]:h-3.5"
                                                    aria-hidden="true"
                                                    dangerouslySetInnerHTML={{ __html: cat.icon }}
                                                />
                                                <span className="truncate">{cat.name.replace(/-/g, ' ')}</span>
                                            </button>
                                        );
                                    })
                                )}
                            </div>
                        </div>

                        {/* Advanced toggle — gates the loader / MC-version filters below.
                            Turning it off resets both back to the server's stored values. */}
                        <div className="border-t border-(--base-03) pt-3 flex items-center justify-between gap-2">
                            <div className="min-w-0">
                                <div className="flex items-center gap-1.5 text-xs font-medium text-(--base-09)">
                                    <Filter size={11} className="text-(--base-06)" />
                                    Advanced
                                </div>
                                <p className="text-[10px] text-(--base-06) mt-0.5">Edit loader / MC-version filters</p>
                            </div>
                            <button
                                type="button"
                                role="switch"
                                aria-checked={advanced}
                                onClick={handleAdvancedToggle}
                                className={`toggle-track shrink-0 ${advanced ? 'toggle-track-on' : 'toggle-track-off'}`}
                                title="Toggle loader / version filters"
                            >
                                <span className={`toggle-knob ${advanced ? 'toggle-knob-on' : 'toggle-knob-off'}`} />
                            </button>
                        </div>

                        {/* Loader + MC-version filters, greyed until Advanced is on. */}
                        <div className={advanced ? '' : 'opacity-50 pointer-events-none select-none'}>
                            <label className="input-label mb-0">Loaders</label>
                            <div className="mt-1.5 space-y-1 max-h-40 overflow-y-auto">
                                {LOADER_OPTIONS.map(l => {
                                    const on = filterLoaders.includes(l);
                                    return (
                                        <label key={l} className="flex items-center gap-2 text-xs cursor-pointer hover:text-(--base-09)">
                                            <input
                                                type="checkbox"
                                                checked={on}
                                                disabled={!advanced}
                                                onChange={() => setFilterLoaders(prev => on ? prev.filter(x => x !== l) : [...prev, l])}
                                                className="accent-(--accent)"
                                            />
                                            {l}
                                        </label>
                                    );
                                })}
                            </div>
                            <label className="input-label mt-3">Game versions (comma)</label>
                            <input
                                type="text"
                                value={filterVersions.join(',')}
                                onChange={e => setFilterVersions(e.target.value.split(',').map(s => s.trim()).filter(Boolean))}
                                disabled={!advanced}
                                className="input-field input-mono w-full text-xs"
                                placeholder="1.20.2, 1.21"
                            />
                        </div>
                    </aside>

                    {/* Result list (single column). Real min-width so it never
                        collapses to unusable once the detail column claims its share. */}
                    <div className="flex-1 min-w-[360px] flex flex-col overflow-hidden">
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

                        {unknownLoader && (
                            <div className="shrink-0 mb-2 px-3 py-2 rounded-md bg-(--warning-ghost) border border-(--warning)/30 text-(--warning-light) text-xs flex items-start gap-2">
                                <AlertTriangle size={12} className="mt-0.5 shrink-0" />
                                Unknown loader <code className="font-mono">{defaultLoader}</code> — results aren't pre-filtered by mod/plugin type.
                            </div>
                        )}

                        <div className="flex-1 overflow-y-auto space-y-2">
                            {searchLoading ? (
                                <div className="text-center py-12 text-sm text-(--base-06)">Searching…</div>
                            ) : !searchResult || searchResult.hits.length === 0 ? (
                                <div className="text-center py-12 text-sm text-(--base-06)">No projects match.</div>
                            ) : (
                                searchResult.hits.map(hit => {
                                    const installedMod = installedByProject.get(hit.project_id);
                                    const candidates = installedMod ? installedRowVersions.get(hit.project_id) : undefined;
                                    const status: RowStatus = !installedMod
                                        ? 'not-installed'
                                        : candidates === undefined
                                            ? 'checking'
                                            : compareInstalledVsLatest(installedMod.modrinthVersionId, candidates, { loaders: filterLoaders, mcVersions: filterVersions });
                                    return (
                                        <ModListRow
                                            key={hit.project_id}
                                            hit={hit}
                                            status={status}
                                            busy={busyProjects.has(hit.project_id)}
                                            selected={selectedSlug === hit.slug}
                                            onOpen={() => openProjectDetail(hit.slug)}
                                            onInstall={() => handleRowInstall(hit)}
                                            onRemove={() => installedMod && handleRowRemove(installedMod)}
                                            onUpdate={() => {
                                                if (!installedMod || !candidates) return;
                                                const latest = pickNewestMatchingVersion(candidates, { loaders: filterLoaders, mcVersions: filterVersions });
                                                if (latest) handleRowUpdate(hit, installedMod, latest);
                                            }}
                                        />
                                    );
                                })
                            )}
                        </div>
                    </div>

                    {/* Detail column — hidden below `lg` so it never squeezes the
                        list to unusable on narrow windows; roughly half the
                        remaining width from `lg` up, capped so it doesn't grow
                        absurdly wide on very large screens. */}
                    {(projectDetail || projectLoading) && (
                        <aside className="hidden lg:flex lg:w-[46%] lg:max-w-2xl shrink-0 flex-col card overflow-hidden">
                            {projectLoading || !projectDetail ? (
                                <div className="p-4 space-y-3">
                                    <div className="flex items-start gap-3">
                                        <Skeleton className="w-12 h-12 rounded-md shrink-0" />
                                        <div className="flex-1 space-y-2">
                                            <SkeletonText width="w-1/2" className="h-4" />
                                            <SkeletonText width="w-3/4" className="h-2.5" />
                                        </div>
                                    </div>
                                    <SkeletonCard height="h-40" />
                                </div>
                            ) : (
                                <>
                                    <div className="shrink-0 p-4 border-b border-(--base-03) flex items-start gap-3">
                                        {projectDetail.icon_url && (
                                            // eslint-disable-next-line @next/next/no-img-element
                                            <img src={projectDetail.icon_url} alt="" className="w-12 h-12 rounded-md shrink-0" />
                                        )}
                                        <div className="min-w-0 flex-1">
                                            <h3 className="text-sm font-semibold text-(--base-09) truncate">{projectDetail.title}</h3>
                                            <div className="flex items-center gap-3 mt-1 text-[10px] font-mono text-(--base-06)">
                                                <span>{projectDetail.downloads.toLocaleString()} downloads</span>
                                                <a href={`https://modrinth.com/project/${projectDetail.slug}`} target="_blank" rel="noopener noreferrer" className="text-(--accent-light) flex items-center gap-1">
                                                    modrinth.com <ExternalLink size={9} />
                                                </a>
                                            </div>
                                        </div>
                                        <button onClick={closeProjectDetail} className="text-(--base-06) hover:text-(--base-09) transition-colors shrink-0" title="Close">
                                            <X size={16} />
                                        </button>
                                    </div>

                                    <div className="flex-1 overflow-y-auto p-4 space-y-4">
                                        {/* Description with a short/full switch */}
                                        <div>
                                            <div className="flex items-center justify-between mb-1.5">
                                                <label className="input-label mb-0">Description</label>
                                                <div className="flex bg-(--base-03)/60 rounded-md p-0.5">
                                                    {(['short', 'full'] as const).map(m => (
                                                        <button
                                                            key={m}
                                                            onClick={() => setDescMode(m)}
                                                            className={`px-2 py-0.5 rounded-sm text-[10px] font-medium capitalize transition-colors ${
                                                                descMode === m ? 'bg-(--base-02) text-(--base-09)' : 'text-(--base-06) hover:text-(--base-09)'
                                                            }`}
                                                        >
                                                            {m}
                                                        </button>
                                                    ))}
                                                </div>
                                            </div>
                                            {descMode === 'short' ? (
                                                <p className="text-sm text-(--base-07) leading-relaxed">{projectDetail.description}</p>
                                            ) : projectDetail.body ? (
                                                <div className="text-sm text-(--base-08) leading-relaxed wrap-break-word [&_h1]:text-base [&_h1]:font-semibold [&_h1]:text-(--base-09) [&_h1]:mt-3 [&_h1]:mb-1 [&_h2]:text-sm [&_h2]:font-semibold [&_h2]:text-(--base-09) [&_h2]:mt-3 [&_h2]:mb-1 [&_h3]:font-semibold [&_h3]:text-(--base-09) [&_h3]:mt-2 [&_p]:my-2 [&_ul]:list-disc [&_ul]:pl-5 [&_ul]:my-2 [&_ol]:list-decimal [&_ol]:pl-5 [&_ol]:my-2 [&_a]:text-(--accent-light) [&_a]:underline [&_code]:font-mono [&_code]:text-xs [&_code]:bg-(--base-03) [&_code]:px-1 [&_code]:rounded [&_pre]:bg-(--base-01) [&_pre]:p-2 [&_pre]:rounded [&_pre]:overflow-x-auto [&_pre]:my-2 [&_img]:max-w-full [&_img]:rounded [&_img]:my-2 [&_hr]:my-3 [&_hr]:border-(--base-03) [&_blockquote]:border-l-2 [&_blockquote]:border-(--base-04) [&_blockquote]:pl-3 [&_blockquote]:text-(--base-06) [&_table]:text-xs">
                                                    <ReactMarkdown
                                                        remarkPlugins={[remarkGfm]}
                                                        components={{ a: ({ node, ...props }) => <a {...props} target="_blank" rel="noopener noreferrer" /> }}
                                                    >
                                                        {projectDetail.body}
                                                    </ReactMarkdown>
                                                </div>
                                            ) : (
                                                <p className="text-xs text-(--base-06) italic">No full description provided.</p>
                                            )}
                                        </div>

                                        {/* Versions — newest build for this MC version highlighted */}
                                        <div>
                                            <label className="input-label">Versions</label>
                                            <div className="mt-1.5 space-y-1.5">
                                                {sortedVersions.length === 0 ? (
                                                    <p className="text-xs text-(--base-06)">No compatible versions found.</p>
                                                ) : (
                                                    sortedVersions.slice(0, 30).map(v => {
                                                        const highlight = v.id === highlightVersionId;
                                                        return (
                                                            <div
                                                                key={v.id}
                                                                className={`flex items-center gap-2 p-2 rounded-md border ${
                                                                    highlight ? 'border-(--accent-border) bg-(--accent-ghost)' : 'border-(--base-04)'
                                                                }`}
                                                            >
                                                                <div className="min-w-0 flex-1">
                                                                    <div className="flex items-center gap-1.5">
                                                                        <span className="text-sm font-medium text-(--base-09) truncate">{v.version_number}</span>
                                                                        {highlight && <span className="mono-label text-(--accent-light) shrink-0">newest · {defaultMcVersion}</span>}
                                                                    </div>
                                                                    <div className="text-[10px] font-mono text-(--base-06) truncate">
                                                                        {v.version_type} · {v.loaders.join(', ')} · MC {v.game_versions.join(', ')}
                                                                    </div>
                                                                </div>
                                                                <button onClick={() => handleInstall(projectDetail, v)} className="btn btn-primary btn-sm shrink-0">
                                                                    <Download size={11} />
                                                                    Install
                                                                </button>
                                                            </div>
                                                        );
                                                    })
                                                )}
                                            </div>
                                        </div>
                                    </div>
                                </>
                            )}
                        </aside>
                    )}
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

function ModListRow({
    hit,
    status,
    busy,
    selected,
    onOpen,
    onInstall,
    onRemove,
    onUpdate,
}: {
    hit: ModrinthSearchHit;
    status: RowStatus;
    busy: boolean;
    selected: boolean;
    onOpen: () => void;
    onInstall: () => void;
    onRemove: () => void;
    onUpdate: () => void;
}) {
    // The row opens the detail column on click, but also hosts a nested real
    // <button> for install/remove/update — a <button> can't nest another
    // <button>, so the row itself is a div with button semantics (role +
    // keyboard handling) and the action button stops propagation.
    const handleKeyDown = (e: KeyboardEvent<HTMLDivElement>) => {
        if (e.key === 'Enter' || e.key === ' ') {
            e.preventDefault();
            onOpen();
        }
    };
    const stopAnd = (fn: () => void) => (e: MouseEvent) => {
        e.stopPropagation();
        fn();
    };

    return (
        <div
            role="button"
            tabIndex={0}
            onClick={onOpen}
            onKeyDown={handleKeyDown}
            className={`card p-3 w-full flex items-start gap-3 transition-colors cursor-pointer focus:outline-none focus:border-(--accent-border) ${
                selected ? 'border-(--accent-border)' : 'hover:border-(--base-04)'
            }`}
        >
            {hit.icon_url ? (
                // eslint-disable-next-line @next/next/no-img-element
                <img src={hit.icon_url} alt="" className="w-11 h-11 rounded-md shrink-0" />
            ) : (
                <div className="w-11 h-11 rounded-md bg-(--base-03) flex items-center justify-center shrink-0">
                    <Package size={16} className="text-(--base-05)" />
                </div>
            )}
            <div className="min-w-0 flex-1">
                <div className="flex items-center gap-2">
                    <span className="font-medium text-sm text-(--base-09) truncate">{hit.title}</span>
                    <span className="text-[10px] font-mono text-(--base-06) truncate">by {hit.author}</span>
                </div>
                <p className="text-xs text-(--base-07) line-clamp-2 mt-0.5">{hit.description}</p>
                <div className="text-[10px] font-mono text-(--base-06) flex items-center gap-1.5 mt-1">
                    <Download size={10} />
                    {hit.downloads.toLocaleString()}
                </div>
            </div>

            <div className="flex flex-col items-end gap-1.5 shrink-0">
                {status === 'up-to-date' && (
                    <span className="mono-label bg-(--success-ghost) text-(--success-light) px-1.5 rounded-sm">installed</span>
                )}
                {status === 'update-available' && (
                    <span className="mono-label bg-(--warning-ghost) text-(--warning-light) px-1.5 rounded-sm">update available</span>
                )}
                {status === 'checking' && (
                    <span className="mono-label bg-(--base-03) text-(--base-06) px-1.5 rounded-sm">checking…</span>
                )}

                {status === 'not-installed' && (
                    <button onClick={stopAnd(onInstall)} disabled={busy} className="btn btn-primary btn-sm">
                        {busy ? <RefreshCw size={11} className="animate-spin" /> : <Download size={11} />}
                        Install
                    </button>
                )}
                {status === 'update-available' && (
                    <button onClick={stopAnd(onUpdate)} disabled={busy} className="btn btn-primary btn-sm">
                        {busy ? <RefreshCw size={11} className="animate-spin" /> : <Download size={11} />}
                        Update
                    </button>
                )}
                {(status === 'up-to-date' || status === 'update-available') && (
                    <button onClick={stopAnd(onRemove)} disabled={busy} className="btn btn-secondary btn-sm" title="Remove">
                        {busy ? <RefreshCw size={11} className="animate-spin" /> : <Trash2 size={11} className="text-(--error)" />}
                        Remove
                    </button>
                )}
            </div>
        </div>
    );
}
