"use client";

import React, { useCallback, useEffect, useState } from 'react';
import Link from 'next/link';
import {
    Package, Plus, ExternalLink, RefreshCw, CircleCheck, CircleAlert,
    X, Trash2, Pencil,
} from 'lucide-react';
import { systemEvents } from '@/lib/systemEvents';
import { listModpacks, createModpack, deleteModpack, type Modpack } from '@/lib/api/modpacks';
import { useAppData } from '@/lib/AppDataContext';
import { SkeletonCard } from '@/components/Skeleton';

// Phase 14 — top-level Modpacks list. Per-user authored modpacks. The
// builder UI lives at /modpacks/<id>; this page covers create + list +
// delete + jump-to-detail.

const LOADER_OPTIONS = ['fabric', 'forge', 'quilt', 'neoforge', 'paper', 'spigot'];

export default function ModpacksListPage() {
    const { featureFlags } = useAppData();
    const modpacksDisabled = !featureFlags.modpacks;
    const [packs, setPacks] = useState<Modpack[]>([]);
    const [loading, setLoading] = useState(true);
    const [creating, setCreating] = useState<{
        name: string; slug: string; loader: string; mcVersion: string; summary: string;
    } | null>(null);
    const [deletePrompt, setDeletePrompt] = useState<Modpack | null>(null);
    const [toast, setToast] = useState<{ msg: string; ok: boolean } | null>(null);

    const showToast = useCallback((msg: string, ok = true) => {
        setToast({ msg, ok });
        setTimeout(() => setToast(null), 3500);
    }, []);

    const refresh = useCallback(async () => {
        const list = await listModpacks();
        setPacks(list);
        setLoading(false);
    }, []);

    useEffect(() => { refresh(); }, [refresh]);

    useEffect(() => {
        const unsub = systemEvents.on('modpacks.changed', () => { refresh(); });
        return () => { unsub(); };
    }, [refresh]);

    const handleCreate = async () => {
        if (!creating) return;
        if (!creating.name.trim()) { showToast('Name required', false); return; }
        const res = await createModpack({
            name: creating.name.trim(),
            slug: creating.slug.trim() || undefined,
            loader: creating.loader || undefined,
            mcVersion: creating.mcVersion.trim() || undefined,
            summary: creating.summary.trim() || undefined,
        });
        if (res.success && res.modpack) {
            setCreating(null);
            showToast('Modpack created.', true);
            refresh();
        } else {
            showToast(res.message || 'Create failed', false);
        }
    };

    const handleDelete = async () => {
        if (!deletePrompt) return;
        const res = await deleteModpack(deletePrompt.id);
        if (res.success) {
            setDeletePrompt(null);
            showToast('Modpack deleted.', true);
            refresh();
        } else {
            showToast(res.message || 'Delete failed', false);
        }
    };

    return (
        <main className="flex-1 overflow-y-auto p-6 max-w-5xl">
            <header className="flex items-center gap-3 mb-4">
                <Package size={20} className="text-(--accent-light)" />
                <h1 className="text-base font-display font-semibold text-(--base-09)">My Modpacks</h1>
                <div className="ml-auto flex items-center gap-2">
                    <button onClick={refresh} className="btn btn-secondary btn-sm" disabled={loading}>
                        <RefreshCw size={12} className={loading ? 'animate-spin' : ''} />
                    </button>
                    <button
                        onClick={() => setCreating({ name: '', slug: '', loader: 'fabric', mcVersion: '', summary: '' })}
                        className="btn btn-primary btn-sm disabled:opacity-40 disabled:cursor-not-allowed"
                        disabled={modpacksDisabled}
                        title={modpacksDisabled ? 'Modpack authoring is disabled' : undefined}
                    >
                        <Plus size={13} />
                        New Modpack
                    </button>
                </div>
            </header>

            {modpacksDisabled && (
                <div className="card p-3 border border-(--warning) bg-(--warning)/10 mb-4 flex items-start gap-2">
                    <CircleAlert size={16} className="text-(--warning) mt-0.5 shrink-0" />
                    <div className="text-xs text-(--base-09)">
                        Modpack authoring is disabled by the platform admin.
                        Existing modpacks remain readable and downloadable.
                    </div>
                </div>
            )}

            <div className="card p-4 mb-4 text-xs text-(--base-07) flex items-start gap-2">
                <Package size={14} className="text-(--accent-light) shrink-0 mt-0.5" />
                <div>
                    Author modpacks here, publish to Modrinth (
                    <Link href="/account/modrinth" className="text-(--accent-light) inline-flex items-center gap-1">
                        connect your PAT <ExternalLink size={9} />
                    </Link>
                    ). The 3-stage channel model lets you iterate as Draft (local), test as Beta
                    (Modrinth unlisted + collaborators), and ship as Release.
                </div>
            </div>

            {loading ? (
                <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-3">
                    {Array.from({ length: 6 }).map((_, i) => (
                        <SkeletonCard key={i} height="h-28" />
                    ))}
                </div>
            ) : packs.length === 0 ? (
                <div className="card p-8 flex flex-col items-center text-center gap-2">
                    <Package size={28} className="text-(--base-05)" />
                    <p className="text-sm text-(--base-07)">No modpacks yet.</p>
                    <p className="text-xs text-(--base-06)">Create your first one to get started.</p>
                </div>
            ) : (
                <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-3">
                    {packs.map(p => (
                        <Link key={p.id} href={`/modpacks/${p.id}`} className="card p-4 hover:border-(--accent-border) transition-colors group">
                            <div className="flex items-start gap-3">
                                <div className="w-10 h-10 rounded-md bg-(--base-03) flex items-center justify-center shrink-0">
                                    <Package size={18} className="text-(--accent-light)" />
                                </div>
                                <div className="min-w-0 flex-1">
                                    <div className="text-sm font-medium text-(--base-09) truncate group-hover:text-(--accent-light)">{p.name}</div>
                                    <div className="text-[10px] font-mono text-(--base-06) truncate">/{p.slug}</div>
                                </div>
                                <button
                                    onClick={(e) => { e.preventDefault(); setDeletePrompt(p); }}
                                    className="text-(--base-06) hover:text-(--error-light) shrink-0 disabled:opacity-30 disabled:hover:text-(--base-06) disabled:cursor-not-allowed"
                                    title={modpacksDisabled ? 'Modpack authoring is disabled' : 'Delete'}
                                    disabled={modpacksDisabled}
                                >
                                    <Trash2 size={12} />
                                </button>
                            </div>
                            {p.summary && <p className="text-xs text-(--base-07) line-clamp-2 mt-2">{p.summary}</p>}
                            <div className="mt-3 flex items-center gap-2 flex-wrap text-[10px] font-mono">
                                {p.loader && <span className="bg-(--base-03) px-1.5 rounded-sm text-(--base-07)">{p.loader}</span>}
                                {p.mcVersion && <span className="bg-(--base-03) px-1.5 rounded-sm text-(--base-07)">MC {p.mcVersion}</span>}
                                {p.modrinthProjectId
                                    ? <span className="bg-(--success-ghost) text-(--success-light) px-1.5 rounded-sm">on modrinth</span>
                                    : <span className="bg-(--base-03) px-1.5 rounded-sm text-(--base-06)">local only</span>
                                }
                            </div>
                        </Link>
                    ))}
                </div>
            )}

            {/* Create */}
            {creating && (
                <div className="modal-overlay animate-fade-in" onClick={() => setCreating(null)}>
                    <div className="modal-panel max-w-lg" onClick={e => e.stopPropagation()}>
                        <div className="modal-header">
                            <h3 className="modal-title flex items-center gap-2">
                                <Package size={16} />
                                New modpack
                            </h3>
                            <button onClick={() => setCreating(null)} className="text-(--base-06)"><X size={16} /></button>
                        </div>
                        <div className="modal-body space-y-4">
                            <div>
                                <label className="input-label">Name</label>
                                <input
                                    type="text"
                                    value={creating.name}
                                    onChange={e => setCreating({ ...creating, name: e.target.value })}
                                    className="input-field w-full"
                                    placeholder="Skyblock Reborn"
                                    maxLength={128}
                                />
                            </div>
                            <div>
                                <label className="input-label">Slug (URL-safe, optional)</label>
                                <input
                                    type="text"
                                    value={creating.slug}
                                    onChange={e => setCreating({ ...creating, slug: e.target.value.toLowerCase() })}
                                    className="input-field input-mono w-full"
                                    placeholder="auto-derived from name"
                                />
                                <p className="text-xs text-(--base-06) mt-1">Lowercase, 2-64 chars, alphanumeric + - / _</p>
                            </div>
                            <div className="grid grid-cols-2 gap-3">
                                <div>
                                    <label className="input-label">Loader</label>
                                    <select
                                        value={creating.loader}
                                        onChange={e => setCreating({ ...creating, loader: e.target.value })}
                                        className="input-field w-full"
                                    >
                                        {LOADER_OPTIONS.map(l => <option key={l} value={l}>{l}</option>)}
                                    </select>
                                </div>
                                <div>
                                    <label className="input-label">MC Version</label>
                                    <input
                                        type="text"
                                        value={creating.mcVersion}
                                        onChange={e => setCreating({ ...creating, mcVersion: e.target.value })}
                                        className="input-field input-mono w-full"
                                        placeholder="1.20.2"
                                    />
                                </div>
                            </div>
                            <div>
                                <label className="input-label">Summary (optional)</label>
                                <input
                                    type="text"
                                    value={creating.summary}
                                    onChange={e => setCreating({ ...creating, summary: e.target.value })}
                                    className="input-field w-full"
                                    placeholder="One-line description"
                                    maxLength={255}
                                />
                            </div>
                        </div>
                        <div className="modal-footer">
                            <button onClick={() => setCreating(null)} className="btn btn-secondary">Cancel</button>
                            <button onClick={handleCreate} className="btn btn-primary">Create</button>
                        </div>
                    </div>
                </div>
            )}

            {/* Delete */}
            {deletePrompt && (
                <div className="modal-overlay animate-fade-in" onClick={() => setDeletePrompt(null)}>
                    <div className="modal-panel max-w-sm" onClick={e => e.stopPropagation()}>
                        <div className="modal-header">
                            <h3 className="modal-title flex items-center gap-2 text-(--error-light)">
                                <Trash2 size={16} />
                                Delete {deletePrompt.name}?
                            </h3>
                        </div>
                        <div className="modal-body">
                            <p className="text-sm text-(--base-07)">
                                Removes the local copy. If this pack was published to Modrinth, the
                                Modrinth project is NOT deleted — manage that on modrinth.com.
                            </p>
                        </div>
                        <div className="modal-footer">
                            <button onClick={() => setDeletePrompt(null)} className="btn btn-secondary">Cancel</button>
                            <button onClick={handleDelete} className="btn btn-danger">Delete</button>
                        </div>
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
