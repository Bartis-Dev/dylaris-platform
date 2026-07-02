"use client";

import React, { useCallback, useEffect, useState } from 'react';
import {
    Users, Plus, Copy, Trash2, AlertTriangle, CircleCheck, CircleAlert,
    MonitorSmartphone, X,
} from 'lucide-react';
import { useAppData } from '@/lib/AppDataContext';
import {
    listClients, createClient, deleteClient,
    type SolderClient,
} from '@/lib/api/solderAccess';
import { SkeletonList } from '@/components/Skeleton';

// Per-user Solder client management. A client is a Technic Launcher
// identity — its UUID (?cid=) whitelists private packs for that launcher.
// UUIDs are not secret; they are shown plainly in the list.

export default function SolderClientsPage() {
    const { user } = useAppData();
    const [clients, setClients] = useState<SolderClient[]>([]);
    const [loading, setLoading] = useState(true);
    const [creating, setCreating] = useState(false);
    const [name, setName] = useState('');
    const [deleting, setDeleting] = useState<SolderClient | null>(null);
    const [toast, setToast] = useState<{ msg: string; ok: boolean } | null>(null);

    const showToast = useCallback((msg: string, ok = true) => {
        setToast({ msg, ok });
        setTimeout(() => setToast(null), 3500);
    }, []);

    const refresh = useCallback(async () => {
        try {
            const data = await listClients();
            setClients(data);
        } catch {
            setClients([]);
        } finally {
            setLoading(false);
        }
    }, []);

    useEffect(() => { refresh(); }, [refresh]);

    const handleCreate = async () => {
        const trimmed = name.trim();
        if (!trimmed) { showToast('Name required', false); return; }
        const res = await createClient(trimmed);
        if (res.success) {
            setCreating(false);
            setName('');
            showToast('Client created.', true);
            refresh();
        } else {
            showToast('Create failed', false);
        }
    };

    const handleDelete = async () => {
        if (!deleting) return;
        const res = await deleteClient(deleting.id);
        setDeleting(null);
        if (res.success) {
            showToast('Client deleted.', true);
            refresh();
        } else {
            showToast('Delete failed', false);
        }
    };

    if (!user) return null;

    return (
        <main className="flex-1 overflow-y-auto p-6 max-w-4xl">
            <header className="flex items-center gap-3 mb-4">
                <MonitorSmartphone size={20} className="text-(--accent-light)" />
                <h1 className="text-base font-display font-semibold text-(--base-09)">Solder Clients</h1>
                <div className="ml-auto">
                    <button onClick={() => setCreating(true)} className="btn btn-primary btn-sm">
                        <Plus size={12} />
                        New Client
                    </button>
                </div>
            </header>

            <div className="card p-4 mb-4 text-xs text-(--base-07) flex items-start gap-2">
                <Users size={14} className="text-(--accent-light) shrink-0 mt-0.5" />
                <p>
                    A client represents a Technic Launcher identity. Its UUID is passed as{' '}
                    <code className="font-mono">?cid=</code> to whitelist private packs for
                    that launcher. Assign clients to specific packs on the pack&apos;s page.
                </p>
            </div>

            {loading ? (
                <SkeletonList rows={3} />
            ) : clients.length === 0 ? (
                <div className="card p-8 flex flex-col items-center text-center gap-2">
                    <MonitorSmartphone size={28} className="text-(--base-05)" />
                    <p className="text-sm text-(--base-07)">No clients yet.</p>
                    <p className="text-xs text-(--base-06)">Create a client and assign it to a pack to whitelist a launcher.</p>
                </div>
            ) : (
                <div className="space-y-2">
                    {clients.map(c => (
                        <article key={c.id} className="card p-3 flex items-start gap-3">
                            <MonitorSmartphone size={16} className="text-(--accent-light) mt-0.5 shrink-0" />
                            <div className="min-w-0 flex-1">
                                <span className="font-medium text-sm text-(--base-09)">{c.name}</span>
                                <div className="mt-1 flex items-center gap-2 flex-wrap">
                                    <span className="mono-label bg-(--base-03) text-(--base-07) px-1.5 rounded-sm font-mono text-[11px] break-all">
                                        {c.uuid}
                                    </span>
                                    <button
                                        onClick={() => { navigator.clipboard.writeText(c.uuid); showToast('UUID copied.', true); }}
                                        className="btn-icon btn-ghost shrink-0"
                                        title="Copy UUID"
                                    >
                                        <Copy size={12} />
                                    </button>
                                </div>
                                <div className="text-xs text-(--base-06) mt-1">
                                    Created: <span className="text-(--base-08)">{new Date(c.createdAt).toLocaleDateString()}</span>
                                </div>
                            </div>
                            <button
                                onClick={() => setDeleting(c)}
                                className="btn-icon btn-ghost shrink-0"
                                title="Delete client"
                            >
                                <Trash2 size={14} className="text-(--error)" />
                            </button>
                        </article>
                    ))}
                </div>
            )}

            {/* Create modal */}
            {creating && (
                <div className="modal-overlay animate-fade-in" onClick={() => setCreating(false)}>
                    <div className="modal-panel max-w-lg" onClick={e => e.stopPropagation()}>
                        <div className="modal-header">
                            <h3 className="modal-title flex items-center gap-2">
                                <MonitorSmartphone size={16} />
                                New Solder Client
                            </h3>
                            <button onClick={() => setCreating(false)} className="text-(--base-06)"><X size={16} /></button>
                        </div>
                        <div className="modal-body space-y-4">
                            <div>
                                <label className="input-label">Name</label>
                                <input
                                    type="text"
                                    value={name}
                                    onChange={e => setName(e.target.value)}
                                    onKeyDown={e => { if (e.key === 'Enter') handleCreate(); }}
                                    className="input-field w-full"
                                    placeholder="my-launcher"
                                    maxLength={128}
                                    autoFocus
                                />
                            </div>
                        </div>
                        <div className="modal-footer">
                            <button onClick={() => setCreating(false)} className="btn btn-secondary">Cancel</button>
                            <button onClick={handleCreate} className="btn btn-primary">Create client</button>
                        </div>
                    </div>
                </div>
            )}

            {/* Delete confirmation */}
            {deleting && (
                <div className="modal-overlay animate-fade-in" onClick={() => setDeleting(null)}>
                    <div className="modal-panel max-w-sm" onClick={e => e.stopPropagation()}>
                        <div className="modal-header">
                            <h3 className="modal-title flex items-center gap-2 text-(--error-light)">
                                <AlertTriangle size={18} />
                                Delete {deleting.name}?
                            </h3>
                        </div>
                        <div className="modal-body">
                            <p className="text-sm text-(--base-07)">
                                This client will be removed from all packs it is assigned to. Launchers
                                using this UUID will lose access to whitelisted private packs immediately.
                            </p>
                        </div>
                        <div className="modal-footer">
                            <button onClick={() => setDeleting(null)} className="btn btn-secondary">Cancel</button>
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
