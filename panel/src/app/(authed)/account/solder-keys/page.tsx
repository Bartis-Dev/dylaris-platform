"use client";

import React, { useCallback, useEffect, useState } from 'react';
import {
    KeyRound, Plus, Copy, Trash2, AlertTriangle, CircleCheck, CircleAlert,
    Shield, X, EyeOff,
} from 'lucide-react';
import { useAppData } from '@/lib/AppDataContext';
import {
    listKeys, createKey, deleteKey,
    type SolderKey,
} from '@/lib/api/solderAccess';
import { SkeletonList } from '@/components/Skeleton';

// Per-user Solder key management. A key (?k=) grants a launcher read access
// to ALL of the owner's private packs. Treat like a password — shown once on
// creation; subsequent listings carry no key material (only the hash is stored).

export default function SolderKeysPage() {
    const { user } = useAppData();
    const [keys, setKeys] = useState<SolderKey[]>([]);
    const [loading, setLoading] = useState(true);
    const [creating, setCreating] = useState(false);
    const [name, setName] = useState('');
    const [revealedKey, setRevealedKey] = useState<{ plaintext: string; name: string } | null>(null);
    const [deleting, setDeleting] = useState<SolderKey | null>(null);
    const [toast, setToast] = useState<{ msg: string; ok: boolean } | null>(null);

    const showToast = useCallback((msg: string, ok = true) => {
        setToast({ msg, ok });
        setTimeout(() => setToast(null), 3500);
    }, []);

    const refresh = useCallback(async () => {
        try {
            const data = await listKeys();
            setKeys(data);
        } catch {
            setKeys([]);
        } finally {
            setLoading(false);
        }
    }, []);

    useEffect(() => { refresh(); }, [refresh]);

    const handleCreate = async () => {
        const trimmed = name.trim();
        if (!trimmed) { showToast('Name required', false); return; }
        const res = await createKey(trimmed);
        if (res.success && res.plaintext) {
            setCreating(false);
            setName('');
            setRevealedKey({ plaintext: res.plaintext, name: trimmed });
            refresh();
        } else {
            showToast('Create failed', false);
        }
    };

    const handleDelete = async () => {
        if (!deleting) return;
        const res = await deleteKey(deleting.id);
        setDeleting(null);
        if (res.success) {
            showToast('Key deleted.', true);
            refresh();
        } else {
            showToast('Delete failed', false);
        }
    };

    if (!user) return null;

    return (
        <main className="flex-1 overflow-y-auto p-6 max-w-4xl">
            <header className="flex items-center gap-3 mb-4">
                <KeyRound size={20} className="text-(--accent-light)" />
                <h1 className="text-base font-display font-semibold text-(--base-09)">Solder Keys</h1>
                <div className="ml-auto">
                    <button onClick={() => setCreating(true)} className="btn btn-primary btn-sm">
                        <Plus size={12} />
                        New Key
                    </button>
                </div>
            </header>

            <div className="card p-4 mb-4 text-xs text-(--base-07) flex items-start gap-2">
                <Shield size={14} className="text-(--accent-light) shrink-0 mt-0.5" />
                <p>
                    A Solder key (<code className="font-mono">?k=</code>) grants a launcher read access to{' '}
                    <strong className="text-(--base-08)">all</strong> of your private packs. Treat it like a
                    password — it is shown only once on creation and cannot be recovered afterwards.
                </p>
            </div>

            {loading ? (
                <SkeletonList rows={3} />
            ) : keys.length === 0 ? (
                <div className="card p-8 flex flex-col items-center text-center gap-2">
                    <KeyRound size={28} className="text-(--base-05)" />
                    <p className="text-sm text-(--base-07)">No keys yet.</p>
                    <p className="text-xs text-(--base-06)">Create a key and pass it as <code className="font-mono">?k=</code> to give a launcher access to all your private packs.</p>
                </div>
            ) : (
                <div className="space-y-2">
                    {keys.map(k => (
                        <article key={k.id} className="card p-3 flex items-start gap-3">
                            <KeyRound size={16} className="text-(--accent-light) mt-0.5 shrink-0" />
                            <div className="min-w-0 flex-1">
                                <span className="font-medium text-sm text-(--base-09)">{k.name}</span>
                                <div className="text-xs text-(--base-06) mt-1">
                                    Created: <span className="text-(--base-08)">{new Date(k.createdAt).toLocaleDateString()}</span>
                                </div>
                            </div>
                            <button
                                onClick={() => setDeleting(k)}
                                className="btn-icon btn-ghost shrink-0"
                                title="Delete key"
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
                                <KeyRound size={16} />
                                New Solder Key
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
                                    placeholder="home-launcher"
                                    maxLength={128}
                                    autoFocus
                                />
                            </div>
                        </div>
                        <div className="modal-footer">
                            <button onClick={() => setCreating(false)} className="btn btn-secondary">Cancel</button>
                            <button onClick={handleCreate} className="btn btn-primary">Create key</button>
                        </div>
                    </div>
                </div>
            )}

            {/* Plaintext reveal — shown once */}
            {revealedKey && (
                <div className="modal-overlay animate-fade-in" onClick={() => setRevealedKey(null)}>
                    <div className="modal-panel max-w-lg" onClick={e => e.stopPropagation()}>
                        <div className="modal-header">
                            <h3 className="modal-title flex items-center gap-2 text-(--accent-light)">
                                <KeyRound size={16} />
                                {revealedKey.name} — copy now
                            </h3>
                        </div>
                        <div className="modal-body space-y-3">
                            <p className="text-sm text-(--base-07) flex items-start gap-2">
                                <AlertTriangle size={14} className="text-(--warning-light) shrink-0 mt-0.5" />
                                This is the only time the full key will be shown. Copy it into your
                                launcher config now — it cannot be recovered afterwards.
                            </p>
                            <div className="flex items-center gap-2 p-3 rounded-md bg-(--base-02) border border-(--base-04) font-mono text-sm break-all">
                                <span className="flex-1">{revealedKey.plaintext}</span>
                                <button
                                    onClick={() => { navigator.clipboard.writeText(revealedKey.plaintext); showToast('Copied.', true); }}
                                    className="btn btn-secondary btn-sm shrink-0"
                                >
                                    <Copy size={12} />
                                    Copy
                                </button>
                            </div>
                        </div>
                        <div className="modal-footer">
                            <button onClick={() => setRevealedKey(null)} className="btn btn-primary">
                                <EyeOff size={12} />
                                Hide
                            </button>
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
                                Any launcher using this key will immediately lose access to your private packs.
                                This action cannot be undone.
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
