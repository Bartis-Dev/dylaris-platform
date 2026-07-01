"use client";

import React, { useCallback, useEffect, useRef, useState } from 'react';
import { useParams } from 'next/navigation';
import Link from 'next/link';
import {
    Package, ArrowLeft, Trash2, Search,
    CircleCheck, CircleAlert, Box, Upload, Lock,
} from 'lucide-react';
import { systemEvents } from '@/lib/systemEvents';
import {
    listBuilds, listContent, addModrinthContent, removeContent, setContentSide,
    uploadContent, type PackBuild, type BuildContentEntry,
} from '@/lib/api/packs';
import { useAppData } from '@/lib/AppDataContext';
import { SkeletonHeader, SkeletonList, SkeletonText, Skeleton } from '@/components/Skeleton';
import ModrinthVersionBrowser from '@/components/modrinth/ModrinthVersionBrowser';

// Build content editor. Two panels:
//   left:  the build's content list (mods / resource-packs / plugins), with a
//          per-row side selector (client/server/both) and a source chip
//          (green Modrinth = linked, yellow Upload = local file). Remove inline.
//   right: Modrinth search filtered to the build's loader + MC version, plus a
//          direct file upload. Frozen builds are read-only.

const SIDES: BuildContentEntry['side'][] = ['both', 'client', 'server'];

export default function BuildContentEditorPage() {
    const params = useParams();
    const packId = Number(params?.id);
    const buildId = Number(params?.buildId);
    const { featureFlags } = useAppData();
    const modpacksDisabled = !featureFlags.modpacks;

    const [build, setBuild] = useState<PackBuild | null>(null);
    const [content, setContent] = useState<BuildContentEntry[]>([]);
    const [loading, setLoading] = useState(true);

    // Upload state
    const fileInputRef = useRef<HTMLInputElement | null>(null);
    const [uploading, setUploading] = useState(false);

    const [toast, setToast] = useState<{ msg: string; ok: boolean } | null>(null);
    const showToast = useCallback((msg: string, ok = true) => {
        setToast({ msg, ok });
        setTimeout(() => setToast(null), 3500);
    }, []);

    const refresh = useCallback(async () => {
        const [bs, list] = await Promise.all([listBuilds(packId), listContent(packId, buildId)]);
        setBuild(bs.find(b => b.id === buildId) || null);
        setContent(list);
        setLoading(false);
    }, [packId, buildId]);

    useEffect(() => { refresh(); }, [refresh]);

    useEffect(() => {
        const unsubC = systemEvents.on('pack_content.changed', (evt) => {
            const bid = (evt.payload as any)?.buildId;
            if (bid === undefined || bid === buildId) refresh();
        });
        const unsubB = systemEvents.on('pack_builds.changed', () => { refresh(); });
        return () => { unsubC(); unsubB(); };
    }, [buildId, refresh]);

    const isFrozen = !!build?.frozen;
    const disabled = modpacksDisabled || isFrozen;

    const handleUpload = async (e: React.ChangeEvent<HTMLInputElement>) => {
        const file = e.target.files?.[0];
        if (fileInputRef.current) fileInputRef.current.value = '';
        if (!file) return;
        setUploading(true);
        const res = await uploadContent(packId, buildId, file, 'both', 'mod');
        setUploading(false);
        if (res.success) {
            showToast(`Uploaded ${file.name}`, true);
            refresh();
        } else {
            showToast(res.message || 'Upload failed', false);
        }
    };

    const handleSetSide = async (entry: BuildContentEntry, side: string) => {
        const res = await setContentSide(packId, buildId, entry.id, side);
        if (res.success) {
            refresh();
        } else {
            showToast(res.message || 'Failed to set side', false);
        }
    };

    const handleRemove = async (entry: BuildContentEntry) => {
        const res = await removeContent(packId, buildId, entry.id);
        if (res.success) {
            showToast(`Removed ${entry.prettyName || entry.modSlug}`, true);
            refresh();
        } else {
            showToast(res.message || 'Remove failed', false);
        }
    };

    if (loading) return (
        <main className="flex-1 flex flex-col overflow-hidden">
            <header className="shrink-0 p-6 pb-3 max-w-6xl">
                <SkeletonText width="w-32" className="mb-3" />
                <div className="flex items-start gap-3">
                    <Skeleton className="w-10 h-10 rounded-md shrink-0" />
                    <div className="flex-1">
                        <SkeletonHeader />
                    </div>
                </div>
            </header>
            <div className="flex-1 grid grid-cols-1 lg:grid-cols-2 gap-4 px-6 pb-6 max-w-6xl w-full overflow-hidden">
                <section className="card p-4 flex flex-col overflow-hidden">
                    <SkeletonText width="w-32" className="mb-3 h-4" />
                    <SkeletonList rows={4} />
                </section>
                <section className="card p-4 flex flex-col overflow-hidden">
                    <SkeletonText width="w-32" className="mb-3 h-4" />
                    <Skeleton className="h-9 w-full rounded mb-3" />
                    <SkeletonList rows={5} />
                </section>
            </div>
        </main>
    );
    if (!build) {
        return (
            <main className="flex-1 flex flex-col items-center justify-center p-6 text-(--base-06) gap-3">
                <p className="text-sm">Build not found.</p>
                <Link href={`/modpacks/${packId}`} className="btn btn-secondary btn-sm">
                    <ArrowLeft size={12} />
                    Back
                </Link>
            </main>
        );
    }

    return (
        <main className="flex-1 flex flex-col overflow-hidden">
            <header className="shrink-0 p-6 pb-3 max-w-6xl">
                <Link href={`/modpacks/${packId}`} className="text-xs text-(--base-06) hover:text-(--base-09) inline-flex items-center gap-1 mb-3">
                    <ArrowLeft size={11} />
                    Back to pack
                </Link>
                {modpacksDisabled && (
                    <div className="card p-3 border border-(--warning) bg-(--warning)/10 mb-4 flex items-start gap-2">
                        <CircleAlert size={16} className="text-(--warning) mt-0.5 shrink-0" />
                        <div className="text-xs text-(--base-09)">
                            Modpack authoring is disabled by the platform admin.
                            Existing packs remain readable and downloadable.
                        </div>
                    </div>
                )}
                <div className="flex items-start gap-3">
                    <div className="w-10 h-10 rounded-md bg-(--accent-ghost) flex items-center justify-center shrink-0">
                        <Package size={18} className="text-(--accent-light)" />
                    </div>
                    <div className="min-w-0 flex-1">
                        <h1 className="text-lg font-display font-bold text-(--base-09) inline-flex items-center gap-2">
                            Build Content
                            <span className="font-mono text-sm text-(--base-07) font-normal">
                                {build.versionString}
                            </span>
                            {isFrozen && (
                                <Lock size={14} className="text-(--accent-light)" aria-label="Frozen" />
                            )}
                        </h1>
                        <p className="text-xs text-(--base-06)">
                            Search filtered to <code className="font-mono">{build.loader || 'any loader'}</code> ·{' '}
                            <code className="font-mono">MC {build.minecraft || 'any'}</code>
                        </p>
                        {isFrozen && (
                            <p className="text-xs text-(--base-06) italic mt-1">
                                This build is frozen — it was persisted on first publish/export.
                                Create a new build to change its content list.
                            </p>
                        )}
                    </div>
                    <div className="flex items-center gap-2">
                        <input
                            ref={fileInputRef}
                            type="file"
                            className="hidden"
                            onChange={handleUpload}
                        />
                        <button
                            onClick={() => fileInputRef.current?.click()}
                            className="btn btn-secondary btn-sm disabled:opacity-40 disabled:cursor-not-allowed"
                            disabled={disabled || uploading}
                            title={disabled ? (isFrozen ? 'Build is frozen' : 'Modpack authoring is disabled') : 'Upload a local file'}
                        >
                            <Upload size={12} />
                            {uploading ? 'Uploading…' : 'Upload'}
                        </button>
                    </div>
                </div>
            </header>

            <div className="flex-1 grid grid-cols-1 lg:grid-cols-2 gap-4 px-6 pb-6 max-w-6xl w-full overflow-hidden">
                {/* Current content */}
                <section className="card p-4 flex flex-col overflow-hidden">
                    <header className="flex items-center gap-2 mb-3 shrink-0">
                        <Box size={14} className="text-(--accent-light)" />
                        <h2 className="text-sm font-medium text-(--base-09)">In this build ({content.length})</h2>
                    </header>
                    <div className="flex-1 overflow-y-auto space-y-2">
                        {content.length === 0 ? (
                            <p className="text-xs text-(--base-06) text-center py-8">No content yet. Add some from the search panel →</p>
                        ) : (
                            content.map(entry => (
                                <article key={entry.id} className="flex items-center gap-3 p-2 rounded-md border border-(--base-04)">
                                    <Box size={14} className="text-(--accent-light) shrink-0" />
                                    <div className="min-w-0 flex-1">
                                        <div className="text-sm font-medium text-(--base-09) truncate">{entry.prettyName || entry.modSlug}</div>
                                        <div className="text-[10px] font-mono text-(--base-06) truncate">
                                            {entry.version || entry.contentType}
                                        </div>
                                    </div>
                                    <span className={`mono-label px-1.5 rounded-sm shrink-0 ${
                                        entry.linked
                                            ? 'bg-(--success-ghost) text-(--success-light)'
                                            : 'bg-(--warning-ghost) text-(--warning-light)'
                                    }`}>
                                        {entry.linked ? 'Modrinth' : 'Upload'}
                                    </span>
                                    <select
                                        value={entry.side}
                                        onChange={e => handleSetSide(entry, e.target.value)}
                                        className="input-field input-mono py-1 text-[11px] w-24 shrink-0 disabled:opacity-40 disabled:cursor-not-allowed"
                                        disabled={disabled}
                                        title={disabled ? (isFrozen ? 'Build is frozen' : 'Modpack authoring is disabled') : 'Set side'}
                                    >
                                        {SIDES.map(s => <option key={s} value={s}>{s}</option>)}
                                    </select>
                                    <button
                                        onClick={() => handleRemove(entry)}
                                        className="btn btn-secondary btn-sm shrink-0 disabled:opacity-40 disabled:cursor-not-allowed"
                                        title={disabled ? (isFrozen ? 'Build is frozen' : 'Modpack authoring is disabled') : 'Remove'}
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
                    <ModrinthVersionBrowser
                        loader={build.loader || undefined}
                        mcVersion={build.minecraft || undefined}
                        projectType="mod"
                        installedProjectIds={new Set(content.map(c => c.modrinthProjectId).filter(Boolean) as string[])}
                        disabled={disabled}
                        disabledTitle={isFrozen ? 'Build is frozen' : 'Modpack authoring is disabled'}
                        onPick={(projectId, versionId, hit) => {
                            if (disabled) return;
                            addModrinthContent(packId, buildId, { projectId, versionId, resolveDeps: true })
                                .then(res => {
                                    if (res.success) {
                                        showToast(`Added ${hit.title}`, true);
                                        refresh();
                                    } else {
                                        showToast(res.message || 'Add failed', false);
                                    }
                                });
                        }}
                    />
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
