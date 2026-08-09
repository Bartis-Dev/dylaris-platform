"use client";

import React, { useCallback, useEffect, useState } from 'react';
import { useParams } from 'next/navigation';
import Link from 'next/link';
import {
    Package, ArrowLeft, Plus, Trash2, ExternalLink, RefreshCw,
    CircleCheck, CircleAlert, X, Edit, ChevronRight, Layers,
    Settings, UserX, UserCheck, Lock,
} from 'lucide-react';
import { systemEvents } from '@/lib/systemEvents';
import {
    getPack, listBuilds, createBuild, deleteBuild,
    type Pack, type PackBuild,
} from '@/lib/api/packs';
import { setSolderConfig } from '@/lib/api/solderPublish';
import {
    listPackClients, listClients, addPackClient, removePackClient,
    type SolderClient,
} from '@/lib/api/solderAccess';
import { useAppData } from '@/lib/AppDataContext';
import { SkeletonHeader, SkeletonList, SkeletonText, Skeleton } from '@/components/Skeleton';
import { Badge } from '@/components/ui/Badge';
import { useBusy } from '@/lib/useBusy';

// Pack detail. Shows pack metadata + its builds. Each build pins a
// Minecraft version + loader and links to the per-build content editor at
// /modpacks/<id>/builds/<buildId>. Publish-target chips (solder/modrinth) are
// simple text badges for now.

// Solder config modal state
interface SolderConfigForm {
    solderDisplayName: string;
    solderSlug: string;
    private: boolean;
    recommendedBuild: string;
    latestBuild: string;
}

export default function PackDetailPage() {
    const params = useParams();
    const packId = Number(params?.id);
    const { featureFlags } = useAppData();
    const modpacksDisabled = !featureFlags.modpacks;
    const [pack, setPack] = useState<Pack | null>(null);
    const [builds, setBuilds] = useState<PackBuild[]>([]);
    const [loading, setLoading] = useState(true);
    const [creatingBuild, runCreate] = useBusy();
    const [deletingBuild, runDelete] = useBusy();
    const [creating, setCreating] = useState<{
        versionString: string; minecraft: string; loader: string; loaderVersion: string;
    } | null>(null);
    const [deletePrompt, setDeletePrompt] = useState<PackBuild | null>(null);
    const [toast, setToast] = useState<{ msg: string; ok: boolean } | null>(null);

    // Solder config modal
    const [editingConfig, setEditingConfig] = useState(false);
    const [configForm, setConfigForm] = useState<SolderConfigForm | null>(null);
    const [configError, setConfigError] = useState('');
    const [configSaving, setConfigSaving] = useState(false);

    // Pack-client whitelist
    const [assignedClients, setAssignedClients] = useState<SolderClient[]>([]);
    const [allClients, setAllClients] = useState<SolderClient[]>([]);
    const [clientsLoading, setClientsLoading] = useState(false);
    const [selectedClientId, setSelectedClientId] = useState<number | ''>('');
    const [clientsError, setClientsError] = useState('');

    const showToast = useCallback((msg: string, ok = true) => {
        setToast({ msg, ok });
        setTimeout(() => setToast(null), 3500);
    }, []);

    const refresh = useCallback(async () => {
        const [p, bs] = await Promise.all([getPack(packId), listBuilds(packId)]);
        setPack(p);
        setBuilds(bs);
        setLoading(false);
    }, [packId]);

    useEffect(() => { refresh(); }, [refresh]);

    useEffect(() => {
        const unsubB = systemEvents.on('pack_builds.changed', (evt) => {
            const pid = (evt.payload as any)?.packId;
            if (pid === undefined || pid === packId) refresh();
        });
        const unsubP = systemEvents.on('packs.changed', () => { refresh(); });
        return () => { unsubB(); unsubP(); };
    }, [packId, refresh]);

    // Fetch client whitelist + all owner clients
    const refreshClients = useCallback(async () => {
        setClientsLoading(true);
        setClientsError('');
        try {
            const [assigned, all] = await Promise.all([
                listPackClients(packId).catch(() => { setClientsError('Failed to load assigned clients'); return [] as SolderClient[]; }),
                listClients().catch(() => [] as SolderClient[]),
            ]);
            setAssignedClients(assigned);
            setAllClients(all);
        } finally {
            setClientsLoading(false);
        }
    }, [packId]);

    useEffect(() => { refreshClients(); }, [refreshClients]);

    const handleCreate = async () => {
        if (!creating) return;
        if (!creating.versionString.trim()) { showToast('Version string required', false); return; }
        const res = await createBuild(packId, {
            versionString: creating.versionString.trim(),
            minecraft: creating.minecraft.trim() || undefined,
            loader: creating.loader.trim() || undefined,
            loaderVersion: creating.loaderVersion.trim() || undefined,
        });
        if (res.success && res.build) {
            setCreating(null);
            showToast('Build created.', true);
            refresh();
        } else {
            showToast(res.message || 'Create failed', false);
        }
    };

    const handleDelete = async () => {
        if (!deletePrompt) return;
        const res = await deleteBuild(packId, deletePrompt.id);
        if (res.success) {
            setDeletePrompt(null);
            showToast('Build deleted.', true);
            refresh();
        } else {
            showToast(res.message || 'Delete failed', false);
        }
    };

    // Open the Solder config modal pre-filled from current pack data
    const openConfigModal = () => {
        if (!pack) return;
        setConfigForm({
            solderDisplayName: pack.solderDisplayName,
            solderSlug: pack.solderSlug,
            private: pack.private,
            recommendedBuild: pack.recommendedBuild,
            latestBuild: pack.latestBuild,
        });
        setConfigError('');
        setEditingConfig(true);
    };

    const handleSaveConfig = async () => {
        if (!configForm) return;
        setConfigSaving(true);
        setConfigError('');
        const res = await setSolderConfig(packId, {
            solderSlug: configForm.solderSlug,
            solderDisplayName: configForm.solderDisplayName,
            recommendedBuild: configForm.recommendedBuild || undefined,
            latestBuild: configForm.latestBuild || undefined,
            private: configForm.private,
        });
        setConfigSaving(false);
        if (res.success) {
            setEditingConfig(false);
            showToast('Solder config saved.', true);
            refresh();
        } else {
            setConfigError(res.message || 'Failed to save Solder config');
        }
    };

    // Add a client to the pack whitelist
    const handleAddClient = async () => {
        if (!selectedClientId) return;
        const res = await addPackClient(packId, Number(selectedClientId));
        if (res.success) {
            setSelectedClientId('');
            await refreshClients();
        } else {
            showToast('Failed to add client', false);
        }
    };

    // Remove a client from the pack whitelist
    const handleRemoveClient = async (clientId: number) => {
        const res = await removePackClient(packId, clientId);
        if (res.success) {
            await refreshClients();
        } else {
            showToast('Failed to remove client', false);
        }
    };

    // Builds that are Solder-published (for recommended/latest dropdowns)
    const solderBuilds = builds.filter(b => b.solderPublished);

    // Clients available to add (not yet assigned)
    const assignedIds = new Set(assignedClients.map(c => c.id));
    const availableClients = allClients.filter(c => !assignedIds.has(c.id));

    if (loading) {
        return (
            <main className="flex-1 overflow-y-auto p-6 max-w-4xl">
                <SkeletonText width="w-32" className="mb-3" />
                <header className="flex items-start gap-3 mb-4">
                    <Skeleton className="w-12 h-12 rounded-md shrink-0" />
                    <div className="flex-1">
                        <SkeletonHeader />
                    </div>
                </header>
                <div className="flex items-center gap-2 mb-4">
                    <SkeletonText width="w-16" />
                    <SkeletonText width="w-20" />
                    <SkeletonText width="w-24" />
                </div>
                <SkeletonText width="w-28" className="mb-3 h-4" />
                <SkeletonList rows={4} />
            </main>
        );
    }
    if (!pack) {
        return (
            <main className="flex-1 flex flex-col items-center justify-center p-6 text-(--base-06) gap-3">
                <p className="text-sm">Pack not found.</p>
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
                        Existing packs remain readable and downloadable.
                    </div>
                </div>
            )}

            <header className="flex items-start gap-3 mb-4">
                <div className="w-12 h-12 rounded-md bg-(--accent-ghost) flex items-center justify-center shrink-0">
                    <Package size={20} className="text-(--accent-light)" />
                </div>
                <div className="min-w-0 flex-1">
                    <h1 className="text-lg font-display font-bold text-(--base-09)">{pack.internalName}</h1>
                    <div className="text-xs text-(--base-06) font-mono">/{pack.internalSlug}</div>
                    {pack.summary && <p className="text-sm text-(--base-07) mt-1">{pack.summary}</p>}
                </div>
                <button
                    className="btn btn-secondary btn-sm"
                    onClick={openConfigModal}
                    title="Edit Solder config"
                >
                    <Edit size={12} />
                    Edit
                </button>
            </header>

            <div className="flex items-center gap-2 flex-wrap mb-4">
                {pack.solderDisplayName && (
                    <Badge variant="neutral">solder: {pack.solderDisplayName}</Badge>
                )}
                {/* Named for its axis: this is the Modrinth publish visibility,
                    not the Solder one. Unqualified it read as a contradiction
                    next to the whitelist card, which says "this pack is public"
                    off pack.private - a different flag entirely. */}
                <Badge variant="neutral">modrinth visibility: {pack.modrinthVisibility}</Badge>
                {pack.modrinthProjectId ? (
                    <a
                        href={`https://modrinth.com/modpack/${pack.internalSlug}`}
                        target="_blank"
                        rel="noopener noreferrer"
                    >
                        <Badge variant="success" icon={<ExternalLink size={9} />}>on modrinth</Badge>
                    </a>
                ) : (
                    <Badge variant="neutral">local only</Badge>
                )}
                {pack.private && <Badge variant="accent" icon={<Lock size={8} />}>private</Badge>}
            </div>

            <section>
                <div className="flex items-center gap-2 mb-3">
                    <Layers size={16} className="text-(--accent-light)" />
                    <h2 className="text-sm font-medium text-(--base-09)">Builds</h2>
                    <div className="ml-auto flex items-center gap-2">
                        <button onClick={refresh} className="btn btn-secondary btn-sm">
                            <RefreshCw size={12} />
                        </button>
                        <button
                            onClick={() => setCreating({ versionString: '', minecraft: '', loader: 'fabric', loaderVersion: '' })}
                            className="btn btn-primary btn-sm disabled:opacity-40 disabled:cursor-not-allowed"
                            disabled={modpacksDisabled}
                            title={modpacksDisabled ? 'Modpack authoring is disabled' : undefined}
                        >
                            <Plus size={12} />
                            New build
                        </button>
                    </div>
                </div>

                {builds.length === 0 ? (
                    <div className="card p-6 text-center text-sm text-(--base-06)">
                        No builds yet. Create one to start adding content.
                    </div>
                ) : (
                    <div className="space-y-2">
                        {builds.map(b => (
                            <article key={b.id} className="card p-3 flex items-center gap-3">
                                <div className="min-w-0 flex-1">
                                    <div className="text-sm font-medium text-(--base-09) inline-flex items-center gap-2">
                                        {b.versionString}
                                        <span className="mono-label text-(--base-06) font-normal">
                                            MC {b.minecraft || 'any'} · {b.loader || 'any loader'}
                                            {b.loaderVersion && ` ${b.loaderVersion}`}
                                        </span>
                                    </div>
                                    <div className="text-xs text-(--base-06) mt-0.5 flex items-center gap-2 flex-wrap">
                                        <span>Created {new Date(b.createdAt).toLocaleString()}</span>
                                        <Badge variant={b.solderPublished ? 'success' : 'neutral'}>solder: {b.solderPublished ? 'published' : 'not published'}</Badge>
                                        <Badge variant={b.modrinthPublished ? 'success' : 'neutral'}>modrinth: {b.modrinthPublished ? 'published' : 'not published'}</Badge>
                                        {b.frozen && <Badge variant="warning">frozen</Badge>}
                                    </div>
                                </div>
                                <Link
                                    href={`/modpacks/${packId}/builds/${b.id}`}
                                    className="btn btn-secondary btn-sm"
                                >
                                    Open
                                    <ChevronRight size={12} />
                                </Link>
                                <button
                                    onClick={() => setDeletePrompt(b)}
                                    className="btn btn-secondary btn-sm disabled:opacity-40 disabled:cursor-not-allowed"
                                    disabled={modpacksDisabled}
                                    title={modpacksDisabled ? 'Modpack authoring is disabled' : 'Delete build'}
                                >
                                    <Trash2 size={12} className="text-(--error)" />
                                </button>
                            </article>
                        ))}
                    </div>
                )}
            </section>

            {/* Pack-client whitelist section */}
            <section className="mt-6">
                <div className="flex items-center gap-2 mb-3">
                    <UserCheck size={16} className="text-(--accent-light)" />
                    <h2 className="text-sm font-medium text-(--base-09)">Solder Access (private packs)</h2>
                </div>

                {!pack.private ? (
                    <div className="card p-3 mb-3 text-xs text-(--base-06) flex items-start gap-2">
                        <CircleAlert size={14} className="text-(--base-05) shrink-0 mt-0.5" />
                        <span>
                            This pack is public; the whitelist is only enforced for private packs.
                            Set the pack to private via <button onClick={openConfigModal} className="underline hover:text-(--base-09) cursor-pointer">Edit</button> to hide it from launchers that are not on the list.
                        </span>
                    </div>
                ) : (
                    // Said out loud because the old copy promised "restrict
                    // launcher access", and the limit was measured: a private
                    // pack answers 404 on the Solder API, while its mod zips
                    // came back 200 with no credential. The Technic protocol
                    // downloads mods anonymously, so an artifact URL is a
                    // capability - the whitelist gates discovery, not download.
                    <div className="card p-3 mb-3 text-xs text-(--base-06) flex items-start gap-2">
                        <CircleAlert size={14} className="text-(--base-05) shrink-0 mt-0.5" />
                        <span>
                            Only whitelisted clients can find this pack through the launcher API.
                            Downloads themselves carry no credential (the Technic protocol has no
                            way to authenticate them), so anyone who already has a mod URL from this
                            pack can keep fetching it. Treat the pack as unlisted, not secret.
                        </span>
                    </div>
                )}

                {clientsError && (
                    <div className="card p-3 mb-3 text-xs text-(--error-light) flex items-start gap-2">
                        <CircleAlert size={14} className="shrink-0 mt-0.5" />
                        {clientsError}
                    </div>
                )}

                {allClients.length === 0 && !clientsLoading ? (
                    <div className="card p-6 flex flex-col items-center text-center gap-2">
                        <UserX size={24} className="text-(--base-05)" />
                        <p className="text-sm text-(--base-07)">No Solder clients yet.</p>
                        <p className="text-xs text-(--base-06)">
                            <Link href="/account/solder-clients" className="underline hover:text-(--base-09)">Create a client</Link>
                            {' '}to control who can access this pack via the Technic Launcher.
                        </p>
                    </div>
                ) : (
                    <div className="card p-4 space-y-4">
                        {/* Add client */}
                        {availableClients.length > 0 && (
                            <div className="flex items-center gap-2">
                                <select
                                    className="input-field flex-1"
                                    value={selectedClientId}
                                    onChange={e => setSelectedClientId(e.target.value ? Number(e.target.value) : '')}
                                >
                                    <option value="">Select a client to add…</option>
                                    {availableClients.map(c => (
                                        <option key={c.id} value={c.id}>{c.name}</option>
                                    ))}
                                </select>
                                <button
                                    className="btn btn-primary btn-sm"
                                    onClick={handleAddClient}
                                    disabled={!selectedClientId}
                                >
                                    <Plus size={12} />
                                    Add
                                </button>
                            </div>
                        )}

                        {/* Assigned clients */}
                        {clientsLoading ? (
                            <SkeletonList rows={2} />
                        ) : assignedClients.length === 0 ? (
                            <p className="text-xs text-(--base-06) text-center py-2">
                                No clients whitelisted yet.
                                {availableClients.length === 0 && allClients.length > 0 && ' All your clients are already assigned.'}
                            </p>
                        ) : (
                            <div className="space-y-2">
                                {assignedClients.map(c => (
                                    <article key={c.id} className="flex items-center gap-3 py-1">
                                        <UserCheck size={14} className="text-(--accent-light) shrink-0" />
                                        <div className="min-w-0 flex-1">
                                            <span className="text-sm text-(--base-09) font-medium">{c.name}</span>
                                            <span className="mono-label bg-(--base-03) text-(--base-07) px-1.5 rounded-sm ml-2">
                                                {c.uuid.slice(0, 8)}…
                                            </span>
                                        </div>
                                        <button
                                            onClick={() => handleRemoveClient(c.id)}
                                            className="btn-icon btn-ghost"
                                            title="Remove client"
                                        >
                                            <Trash2 size={13} className="text-(--error)" />
                                        </button>
                                    </article>
                                ))}
                            </div>
                        )}
                    </div>
                )}
            </section>

            {/* Create build */}
            {creating && (
                <div className="modal-overlay animate-fade-in" onClick={() => setCreating(null)}>
                    <div className="modal-panel max-w-md" onClick={e => e.stopPropagation()}>
                        <div className="modal-header">
                            <h3 className="modal-title flex items-center gap-2">
                                <Layers size={16} />
                                New build
                            </h3>
                            <button onClick={() => setCreating(null)} className="text-(--base-06)"><X size={16} /></button>
                        </div>
                        <div className="modal-body space-y-4">
                            <div>
                                <label className="input-label">Version string</label>
                                <input
                                    type="text"
                                    value={creating.versionString}
                                    onChange={e => setCreating({ ...creating, versionString: e.target.value })}
                                    className="input-field input-mono w-full"
                                    placeholder="0.1.0"
                                />
                            </div>
                            <div className="grid grid-cols-2 gap-3">
                                <div>
                                    <label className="input-label">Minecraft</label>
                                    <input
                                        type="text"
                                        value={creating.minecraft}
                                        onChange={e => setCreating({ ...creating, minecraft: e.target.value })}
                                        className="input-field input-mono w-full"
                                        placeholder="1.20.2"
                                    />
                                </div>
                                <div>
                                    <label className="input-label">Loader</label>
                                    <input
                                        type="text"
                                        value={creating.loader}
                                        onChange={e => setCreating({ ...creating, loader: e.target.value.toLowerCase() })}
                                        className="input-field input-mono w-full"
                                        placeholder="fabric"
                                    />
                                </div>
                            </div>
                            <div>
                                <label className="input-label">Loader version (optional)</label>
                                <input
                                    type="text"
                                    value={creating.loaderVersion}
                                    onChange={e => setCreating({ ...creating, loaderVersion: e.target.value })}
                                    className="input-field input-mono w-full"
                                    placeholder="0.15.11"
                                />
                            </div>
                        </div>
                        <div className="modal-footer">
                            <button onClick={() => setCreating(null)} className="btn btn-secondary">Cancel</button>
                            <button onClick={() => runCreate(handleCreate)} disabled={creatingBuild} className="btn btn-primary disabled:opacity-40">Create</button>
                        </div>
                    </div>
                </div>
            )}

            {/* Delete build */}
            {deletePrompt && (
                <div className="modal-overlay animate-fade-in" onClick={() => setDeletePrompt(null)}>
                    <div className="modal-panel max-w-sm" onClick={e => e.stopPropagation()}>
                        <div className="modal-header">
                            <h3 className="modal-title flex items-center gap-2 text-(--error-light)">
                                <Trash2 size={16} />
                                Delete build {deletePrompt.versionString}?
                            </h3>
                        </div>
                        <div className="modal-body">
                            <p className="text-sm text-(--base-07)">
                                Removes the local build + its content list. Published Modrinth
                                versions stay live on modrinth.com — manage that there.
                            </p>
                        </div>
                        <div className="modal-footer">
                            <button onClick={() => setDeletePrompt(null)} className="btn btn-secondary">Cancel</button>
                            <button onClick={() => runDelete(handleDelete)} disabled={deletingBuild} className="btn btn-danger disabled:opacity-40">Delete</button>
                        </div>
                    </div>
                </div>
            )}

            {/* Solder config modal */}
            {editingConfig && configForm && (
                <div className="modal-overlay animate-fade-in" onClick={() => setEditingConfig(false)}>
                    <div className="modal-panel max-w-lg" onClick={e => e.stopPropagation()}>
                        <div className="modal-header">
                            <h3 className="modal-title flex items-center gap-2">
                                <Settings size={16} />
                                Solder config
                            </h3>
                            <button onClick={() => setEditingConfig(false)} className="text-(--base-06)"><X size={16} /></button>
                        </div>
                        <div className="modal-body space-y-4">
                            <div>
                                <label className="input-label">Display name</label>
                                <input
                                    type="text"
                                    value={configForm.solderDisplayName}
                                    onChange={e => setConfigForm({ ...configForm, solderDisplayName: e.target.value })}
                                    className="input-field w-full"
                                    placeholder="My Modpack"
                                    maxLength={128}
                                />
                            </div>
                            <div>
                                <label className="input-label">Solder slug</label>
                                <input
                                    type="text"
                                    value={configForm.solderSlug}
                                    onChange={e => setConfigForm({ ...configForm, solderSlug: e.target.value })}
                                    className="input-mono w-full"
                                    placeholder="my-modpack"
                                    maxLength={64}
                                />
                                <p className="text-xs text-(--base-06) mt-1">Lowercase, <code className="font-mono">a-z 0-9 _ -</code>. This is the launcher slug.</p>
                            </div>
                            <div>
                                <label className="input-label">Recommended build</label>
                                <select
                                    className="input-field w-full"
                                    value={configForm.recommendedBuild}
                                    onChange={e => setConfigForm({ ...configForm, recommendedBuild: e.target.value })}
                                >
                                    <option value="">(none)</option>
                                    {solderBuilds.map(b => (
                                        <option key={b.id} value={b.versionString}>{b.versionString}</option>
                                    ))}
                                </select>
                                {solderBuilds.length === 0 && (
                                    <p className="text-xs text-(--base-06) mt-1">No Solder-published builds yet — publish a build first.</p>
                                )}
                            </div>
                            <div>
                                <label className="input-label">Latest build</label>
                                <select
                                    className="input-field w-full"
                                    value={configForm.latestBuild}
                                    onChange={e => setConfigForm({ ...configForm, latestBuild: e.target.value })}
                                >
                                    <option value="">(none)</option>
                                    {solderBuilds.map(b => (
                                        <option key={b.id} value={b.versionString}>{b.versionString}</option>
                                    ))}
                                </select>
                            </div>
                            <label className="flex items-center gap-2 cursor-pointer select-none">
                                <input
                                    type="checkbox"
                                    checked={configForm.private}
                                    onChange={e => setConfigForm({ ...configForm, private: e.target.checked })}
                                    className="accent-(--accent)"
                                />
                                <span className="text-sm text-(--base-09)">Private (require client whitelist)</span>
                            </label>
                            {configError && (
                                <p className="text-xs text-(--error-light)">{configError}</p>
                            )}
                        </div>
                        <div className="modal-footer">
                            <button onClick={() => setEditingConfig(false)} className="btn btn-secondary">Cancel</button>
                            <button onClick={handleSaveConfig} className="btn btn-primary" disabled={configSaving}>
                                {configSaving ? 'Saving…' : 'Save'}
                            </button>
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
