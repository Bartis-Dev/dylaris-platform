"use client";

import React, { useCallback, useEffect, useState } from 'react';
import { useParams } from 'next/navigation';
import Link from 'next/link';
import {
    Package, ArrowLeft, Plus, Trash2, ExternalLink, RefreshCw,
    CircleCheck, CircleAlert, X, Edit, ChevronRight, Layers, Users,
} from 'lucide-react';
import { systemEvents } from '@/lib/systemEvents';
import {
    getModpack, listVersions, createVersion, deleteVersion,
    type Modpack, type ModpackVersion,
} from '@/lib/api/modpacks';
import {
    listCollaborators, addCollaborator, removeCollaborator, type Collaborator,
} from '@/lib/api/modpackPublish';
import { useAppData } from '@/lib/AppDataContext';

// Phase 14.1 — Modpack detail. Shows pack metadata + version history with
// channel badges (draft/beta/release). Version → mods builder UI lands in
// P14.2; for now each version row links to its (future) builder page and
// supports delete.

const CHANNEL_STYLES: Record<string, string> = {
    draft:   'bg-(--base-03) text-(--base-07)',
    beta:    'bg-(--warning-ghost) text-(--warning-light)',
    release: 'bg-(--success-ghost) text-(--success-light)',
};

export default function ModpackDetailPage() {
    const params = useParams();
    const modpackId = Number(params?.id);
    const { featureFlags } = useAppData();
    const modpacksDisabled = !featureFlags.modpacks;
    const [pack, setPack] = useState<Modpack | null>(null);
    const [versions, setVersions] = useState<ModpackVersion[]>([]);
    const [loading, setLoading] = useState(true);
    const [creatingVersion, setCreatingVersion] = useState<{ versionString: string; channel: 'draft' | 'beta' | 'release'; changelog: string } | null>(null);
    const [deletePrompt, setDeletePrompt] = useState<ModpackVersion | null>(null);
    const [toast, setToast] = useState<{ msg: string; ok: boolean } | null>(null);
    const [collabs, setCollabs] = useState<Collaborator[]>([]);
    const [collabsLoaded, setCollabsLoaded] = useState(false);
    const [addCollabName, setAddCollabName] = useState('');
    const [collabBusy, setCollabBusy] = useState(false);

    const showToast = useCallback((msg: string, ok = true) => {
        setToast({ msg, ok });
        setTimeout(() => setToast(null), 3500);
    }, []);

    const refresh = useCallback(async () => {
        const [p, vs] = await Promise.all([getModpack(modpackId), listVersions(modpackId)]);
        setPack(p);
        setVersions(vs);
        setLoading(false);
    }, [modpackId]);

    useEffect(() => { refresh(); }, [refresh]);

    const refreshCollabs = useCallback(async () => {
        if (!pack?.modrinthProjectId) { setCollabs([]); setCollabsLoaded(true); return; }
        const list = await listCollaborators(modpackId);
        setCollabs(list);
        setCollabsLoaded(true);
    }, [modpackId, pack?.modrinthProjectId]);

    useEffect(() => { refreshCollabs(); }, [refreshCollabs]);

    const handleAddCollab = async () => {
        const name = addCollabName.trim();
        if (!name) return;
        setCollabBusy(true);
        const res = await addCollaborator(modpackId, name);
        setCollabBusy(false);
        if (res.success) {
            showToast(`Invited ${name}`, true);
            setAddCollabName('');
            refreshCollabs();
        } else {
            showToast(res.message || 'Invite failed', false);
        }
    };

    const handleRemoveCollab = async (c: Collaborator) => {
        const uid = c.user?.id;
        if (!uid) return;
        setCollabBusy(true);
        const res = await removeCollaborator(modpackId, uid);
        setCollabBusy(false);
        if (res.success) {
            showToast('Removed.', true);
            refreshCollabs();
        } else {
            showToast(res.message || 'Remove failed', false);
        }
    };

    useEffect(() => {
        const unsubV = systemEvents.on('modpack_versions.changed', (evt) => {
            const mid = (evt.payload as any)?.modpackId;
            if (mid === undefined || mid === modpackId) refresh();
        });
        const unsubP = systemEvents.on('modpacks.changed', () => { refresh(); });
        return () => { unsubV(); unsubP(); };
    }, [modpackId, refresh]);

    const handleCreateVersion = async () => {
        if (!creatingVersion) return;
        if (!creatingVersion.versionString.trim()) { showToast('Version string required', false); return; }
        const res = await createVersion(modpackId, creatingVersion);
        if (res.success) {
            setCreatingVersion(null);
            showToast('Version created.', true);
            refresh();
        } else {
            showToast(res.message || 'Create failed', false);
        }
    };

    const handleDeleteVersion = async () => {
        if (!deletePrompt) return;
        const res = await deleteVersion(modpackId, deletePrompt.id);
        if (res.success) {
            setDeletePrompt(null);
            showToast('Version deleted.', true);
            refresh();
        } else {
            showToast(res.message || 'Delete failed', false);
        }
    };

    if (loading) {
        return <main className="flex-1 p-6 text-sm text-(--base-06)">Loading…</main>;
    }
    if (!pack) {
        return (
            <main className="flex-1 flex flex-col items-center justify-center p-6 text-(--base-06) gap-3">
                <p className="text-sm">Modpack not found.</p>
                <Link href="/modpacks" className="btn btn-secondary btn-sm">
                    <ArrowLeft size={12} />
                    Back to list
                </Link>
            </main>
        );
    }

    return (
        <main className="flex-1 overflow-y-auto p-6 max-w-4xl">
            <Link href="/modpacks" className="text-xs text-(--base-06) hover:text-(--base-09) inline-flex items-center gap-1 mb-3">
                <ArrowLeft size={11} />
                All modpacks
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

            <header className="flex items-start gap-3 mb-4">
                <div className="w-12 h-12 rounded-md bg-(--accent-ghost) flex items-center justify-center shrink-0">
                    <Package size={20} className="text-(--accent-light)" />
                </div>
                <div className="min-w-0 flex-1">
                    <h1 className="text-lg font-display font-bold text-(--base-09)">{pack.name}</h1>
                    <div className="text-xs text-(--base-06) font-mono">/{pack.slug}</div>
                    {pack.summary && <p className="text-sm text-(--base-07) mt-1">{pack.summary}</p>}
                </div>
                <button className="btn btn-secondary btn-sm" disabled title="Edit modpack metadata (coming)">
                    <Edit size={12} />
                    Edit
                </button>
            </header>

            <div className="flex items-center gap-2 flex-wrap mb-4 text-[10px] font-mono">
                {pack.loader && <span className="bg-(--base-03) px-1.5 py-0.5 rounded-sm text-(--base-07)">{pack.loader}</span>}
                {pack.mcVersion && <span className="bg-(--base-03) px-1.5 py-0.5 rounded-sm text-(--base-07)">MC {pack.mcVersion}</span>}
                <span className="bg-(--base-03) px-1.5 py-0.5 rounded-sm text-(--base-07)">visibility: {pack.modrinthVisibility}</span>
                {pack.modrinthProjectId ? (
                    <a
                        href={`https://modrinth.com/modpack/${pack.slug}`}
                        target="_blank"
                        rel="noopener noreferrer"
                        className="inline-flex items-center gap-1 bg-(--success-ghost) text-(--success-light) px-1.5 py-0.5 rounded-sm"
                    >
                        on modrinth <ExternalLink size={9} />
                    </a>
                ) : (
                    <span className="bg-(--base-03) px-1.5 py-0.5 rounded-sm text-(--base-06)">local only</span>
                )}
            </div>

            <section>
                <div className="flex items-center gap-2 mb-3">
                    <Layers size={16} className="text-(--accent-light)" />
                    <h2 className="text-sm font-medium text-(--base-09)">Versions</h2>
                    <div className="ml-auto flex items-center gap-2">
                        <button onClick={refresh} className="btn btn-secondary btn-sm">
                            <RefreshCw size={12} />
                        </button>
                        <button
                            onClick={() => setCreatingVersion({ versionString: '', channel: 'draft', changelog: '' })}
                            className="btn btn-primary btn-sm disabled:opacity-40 disabled:cursor-not-allowed"
                            disabled={modpacksDisabled}
                            title={modpacksDisabled ? 'Modpack authoring is disabled' : undefined}
                        >
                            <Plus size={12} />
                            New version
                        </button>
                    </div>
                </div>

                {versions.length === 0 ? (
                    <div className="card p-6 text-center text-sm text-(--base-06)">
                        No versions yet. Create one to start adding mods.
                    </div>
                ) : (
                    <div className="space-y-2">
                        {versions.map(v => (
                            <article key={v.id} className="card p-3 flex items-center gap-3">
                                <div className={`mono-label px-2 py-0.5 rounded-sm ${CHANNEL_STYLES[v.channel] || CHANNEL_STYLES.draft}`}>
                                    {v.channel}
                                </div>
                                <div className="min-w-0 flex-1">
                                    <div className="text-sm font-medium text-(--base-09)">{v.versionString}</div>
                                    <div className="text-xs text-(--base-06)">
                                        Created {new Date(v.createdAt).toLocaleString()}
                                        {v.publishedAt && <> · Published {new Date(v.publishedAt).toLocaleString()}</>}
                                    </div>
                                    {v.changelog && (
                                        <p className="text-xs text-(--base-07) mt-1 line-clamp-2">{v.changelog}</p>
                                    )}
                                </div>
                                <Link
                                    href={`/modpacks/${modpackId}/versions/${v.id}`}
                                    className="btn btn-secondary btn-sm"
                                >
                                    Open
                                    <ChevronRight size={12} />
                                </Link>
                                <button
                                    onClick={() => setDeletePrompt(v)}
                                    className="btn btn-secondary btn-sm disabled:opacity-40 disabled:cursor-not-allowed"
                                    disabled={modpacksDisabled}
                                    title={modpacksDisabled ? 'Modpack authoring is disabled' : 'Delete version'}
                                >
                                    <Trash2 size={12} className="text-(--error)" />
                                </button>
                            </article>
                        ))}
                    </div>
                )}
            </section>

            {/* Collaborators — only meaningful after first publish */}
            {pack.modrinthProjectId && (
                <section className="mt-6">
                    <div className="flex items-center gap-2 mb-3">
                        <Users size={16} className="text-(--accent-light)" />
                        <h2 className="text-sm font-medium text-(--base-09)">Testers / Collaborators</h2>
                        <span className="text-xs text-(--base-06)">
                            (Modrinth users who can install drafts/betas in their launcher)
                        </span>
                    </div>

                    <div className="card p-4 space-y-3">
                        <div className="flex items-center gap-2">
                            <input
                                type="text"
                                value={addCollabName}
                                onChange={e => setAddCollabName(e.target.value)}
                                onKeyDown={e => { if (e.key === 'Enter') handleAddCollab(); }}
                                placeholder="Modrinth username"
                                className="input-field flex-1"
                                disabled={collabBusy}
                            />
                            <button
                                onClick={handleAddCollab}
                                className="btn btn-primary btn-sm disabled:opacity-40 disabled:cursor-not-allowed"
                                disabled={collabBusy || !addCollabName.trim() || modpacksDisabled}
                                title={modpacksDisabled ? 'Modpack authoring is disabled' : undefined}
                            >
                                <Plus size={12} />
                                Invite
                            </button>
                        </div>

                        {!collabsLoaded ? (
                            <p className="text-xs text-(--base-06)">Loading collaborators…</p>
                        ) : collabs.length === 0 ? (
                            <p className="text-xs text-(--base-06) text-center py-3">
                                No collaborators yet.
                            </p>
                        ) : (
                            <div className="space-y-1">
                                {collabs.map((c, i) => (
                                    <div key={c.user?.id || i} className="flex items-center gap-2 p-2 rounded-md border border-(--base-04)">
                                        <Users size={12} className="text-(--accent-light) shrink-0" />
                                        <div className="min-w-0 flex-1">
                                            <div className="text-sm text-(--base-09)">{c.user?.username || c.user?.id || '(unknown)'}</div>
                                            <div className="text-[10px] font-mono text-(--base-06)">
                                                {c.role || 'collaborator'}
                                                {c.accepted === false && <> · invite pending</>}
                                            </div>
                                        </div>
                                        <button
                                            onClick={() => handleRemoveCollab(c)}
                                            className="btn btn-secondary btn-sm disabled:opacity-40 disabled:cursor-not-allowed"
                                            disabled={collabBusy || modpacksDisabled}
                                            title={modpacksDisabled ? 'Modpack authoring is disabled' : undefined}
                                        >
                                            <Trash2 size={11} className="text-(--error)" />
                                        </button>
                                    </div>
                                ))}
                            </div>
                        )}
                    </div>
                </section>
            )}

            {/* Create version */}
            {creatingVersion && (
                <div className="modal-overlay animate-fade-in" onClick={() => setCreatingVersion(null)}>
                    <div className="modal-panel max-w-md" onClick={e => e.stopPropagation()}>
                        <div className="modal-header">
                            <h3 className="modal-title flex items-center gap-2">
                                <Layers size={16} />
                                New version
                            </h3>
                            <button onClick={() => setCreatingVersion(null)} className="text-(--base-06)"><X size={16} /></button>
                        </div>
                        <div className="modal-body space-y-4">
                            <div>
                                <label className="input-label">Version string</label>
                                <input
                                    type="text"
                                    value={creatingVersion.versionString}
                                    onChange={e => setCreatingVersion({ ...creatingVersion, versionString: e.target.value })}
                                    className="input-field input-mono w-full"
                                    placeholder="0.1.0"
                                />
                            </div>
                            <div>
                                <label className="input-label">Channel</label>
                                <div className="grid grid-cols-3 gap-2 mt-1">
                                    {(['draft', 'beta', 'release'] as const).map(c => (
                                        <button
                                            key={c}
                                            type="button"
                                            onClick={() => setCreatingVersion({ ...creatingVersion, channel: c })}
                                            className={`px-3 py-2 rounded-md border text-sm transition-colors capitalize ${
                                                creatingVersion.channel === c
                                                    ? 'border-(--accent) bg-(--accent-ghost) text-(--accent-light)'
                                                    : 'border-(--base-04) text-(--base-07) hover:bg-(--base-03)'
                                            }`}
                                        >
                                            {c}
                                        </button>
                                    ))}
                                </div>
                                <p className="text-xs text-(--base-06) mt-1">
                                    Draft = local only. Beta + Release require a connected Modrinth PAT to publish.
                                </p>
                            </div>
                            <div>
                                <label className="input-label">Changelog (optional)</label>
                                <textarea
                                    value={creatingVersion.changelog}
                                    onChange={e => setCreatingVersion({ ...creatingVersion, changelog: e.target.value })}
                                    className="input-field w-full font-mono text-xs"
                                    rows={4}
                                    placeholder="What changed in this version?"
                                />
                            </div>
                        </div>
                        <div className="modal-footer">
                            <button onClick={() => setCreatingVersion(null)} className="btn btn-secondary">Cancel</button>
                            <button onClick={handleCreateVersion} className="btn btn-primary">Create</button>
                        </div>
                    </div>
                </div>
            )}

            {/* Delete version */}
            {deletePrompt && (
                <div className="modal-overlay animate-fade-in" onClick={() => setDeletePrompt(null)}>
                    <div className="modal-panel max-w-sm" onClick={e => e.stopPropagation()}>
                        <div className="modal-header">
                            <h3 className="modal-title flex items-center gap-2 text-(--error-light)">
                                <Trash2 size={16} />
                                Delete version {deletePrompt.versionString}?
                            </h3>
                        </div>
                        <div className="modal-body">
                            <p className="text-sm text-(--base-07)">
                                Removes the local version + its mod list. Published Modrinth
                                versions stay live on modrinth.com — manage that there.
                            </p>
                        </div>
                        <div className="modal-footer">
                            <button onClick={() => setDeletePrompt(null)} className="btn btn-secondary">Cancel</button>
                            <button onClick={handleDeleteVersion} className="btn btn-danger">Delete</button>
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
