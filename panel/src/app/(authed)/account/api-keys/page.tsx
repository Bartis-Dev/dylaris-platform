"use client";

import React, { useCallback, useEffect, useState } from 'react';
import {
    Key, Plus, Copy, Trash2, AlertTriangle, CircleCheck, CircleAlert,
    Shield, X, EyeOff,
} from 'lucide-react';
import { useAppData } from '@/lib/AppDataContext';
import { listAPIKeys, createAPIKey, revokeAPIKey, type APIKey } from '@/lib/api/apiKeys';

// Phase 9 — per-user API key management. Lives under /account/ because
// keys are owned by users, not the admin platform. Plaintext is shown
// exactly once on creation; subsequent listings only carry metadata.

const ALL_PERMISSIONS = [
    { id: 'rcon.exec', label: 'RCON exec', description: 'Run RCON commands against the scoped server(s).' },
];

export default function ApiKeysPage() {
    const { user, servers } = useAppData();
    const [keys, setKeys] = useState<APIKey[]>([]);
    const [loading, setLoading] = useState(true);
    const [creating, setCreating] = useState(false);

    const [form, setForm] = useState<{
        name: string;
        servers: string[];
        permissions: string[];
        ratePerMin: number;
    }>({ name: '', servers: [], permissions: ['rcon.exec'], ratePerMin: 60 });

    const [revealedKey, setRevealedKey] = useState<{ plaintext: string; name: string } | null>(null);
    const [revoking, setRevoking] = useState<APIKey | null>(null);
    const [toast, setToast] = useState<{ msg: string; ok: boolean } | null>(null);

    const showToast = useCallback((msg: string, ok = true) => {
        setToast({ msg, ok });
        setTimeout(() => setToast(null), 3500);
    }, []);

    const refresh = useCallback(async () => {
        const res = await listAPIKeys();
        if (res.success && res.keys) setKeys(res.keys);
        setLoading(false);
    }, []);

    useEffect(() => { refresh(); }, [refresh]);

    const handleCreate = async () => {
        const name = form.name.trim();
        if (!name) { showToast('Name required', false); return; }
        if (form.permissions.length === 0) { showToast('Pick at least one permission', false); return; }
        const res = await createAPIKey({
            name,
            servers: form.servers,
            permissions: form.permissions,
            ratePerMin: form.ratePerMin,
        });
        if (res.success && res.plaintext) {
            setCreating(false);
            setForm({ name: '', servers: [], permissions: ['rcon.exec'], ratePerMin: 60 });
            setRevealedKey({ plaintext: res.plaintext, name });
            refresh();
        } else {
            showToast(res.message || 'Create failed', false);
        }
    };

    const handleRevoke = async () => {
        if (!revoking) return;
        const res = await revokeAPIKey(revoking.id);
        setRevoking(null);
        if (res.success) {
            showToast('Key revoked.', true);
            refresh();
        } else {
            showToast(res.message || 'Revoke failed', false);
        }
    };

    if (!user) return null;

    return (
        <main className="flex-1 overflow-y-auto p-6 max-w-4xl">
            <header className="flex items-center gap-3 mb-4">
                <Key size={20} className="text-(--accent-light)" />
                <h1 className="text-base font-display font-semibold text-(--base-09)">API Keys</h1>
                <div className="ml-auto">
                    <button onClick={() => setCreating(true)} className="btn btn-primary btn-sm">
                        <Plus size={12} />
                        New API Key
                    </button>
                </div>
            </header>

            <div className="card p-4 mb-4 text-xs text-(--base-07) flex items-start gap-2">
                <Shield size={14} className="text-(--accent-light) shrink-0 mt-0.5" />
                <div>
                    <p>
                        Used by external automation (Discord bots, stream alerts, scripts) to call
                        Dylaris APIs without your session token. Authenticate with{' '}
                        <code className="font-mono">Authorization: Bearer dyl_…</code>. Each key is
                        scoped to a list of servers + capabilities.
                    </p>
                    <p className="mt-1 text-(--base-06)">
                        Endpoint: <code className="font-mono">POST /api/external/rcon/&lt;server-uuid&gt;/exec</code>
                        {' '}— body: <code className="font-mono">{'{ "command": "list" }'}</code>
                    </p>
                </div>
            </div>

            {loading ? (
                <div className="card p-6 text-center text-sm text-(--base-06)">Loading…</div>
            ) : keys.length === 0 ? (
                <div className="card p-8 flex flex-col items-center text-center gap-2">
                    <Key size={28} className="text-(--base-05)" />
                    <p className="text-sm text-(--base-07)">No API keys yet.</p>
                </div>
            ) : (
                <div className="space-y-2">
                    {keys.map(k => (
                        <article key={k.id} className="card p-3 flex items-start gap-3">
                            <Key size={16} className={k.revokedAt ? 'text-(--base-05)' : 'text-(--accent-light)'} />
                            <div className="min-w-0 flex-1">
                                <div className="flex items-center gap-2 flex-wrap">
                                    <span className="font-medium text-sm text-(--base-09)">{k.name}</span>
                                    {k.revokedAt && (
                                        <span className="mono-label bg-(--base-03) text-(--base-06) px-1.5 rounded-sm">revoked</span>
                                    )}
                                    <span className="mono-label bg-(--base-03) text-(--base-07) px-1.5 rounded-sm">
                                        {k.ratePerMin}/min
                                    </span>
                                </div>
                                <div className="mt-1 text-xs text-(--base-06)">
                                    <span className="text-(--base-07)">Permissions:</span> {k.scope.permissions.join(', ') || '—'}
                                </div>
                                <div className="text-xs text-(--base-06)">
                                    <span className="text-(--base-07)">Servers:</span>{' '}
                                    {k.scope.servers.length === 0
                                        ? <span className="text-(--base-05) italic">(none)</span>
                                        : k.scope.servers.map(uuid => {
                                            const srv = servers.find(s => s.uuid === uuid);
                                            return srv ? srv.name : uuid.slice(0, 8) + '…';
                                        }).join(', ')}
                                </div>
                                {k.lastUsedAt && (
                                    <div className="text-xs text-(--base-06) mt-0.5">
                                        Last used: <span className="text-(--base-08)">{new Date(k.lastUsedAt).toLocaleString()}</span>
                                    </div>
                                )}
                            </div>
                            {!k.revokedAt && (
                                <button onClick={() => setRevoking(k)} className="btn btn-secondary btn-sm shrink-0">
                                    <Trash2 size={12} className="text-(--error)" />
                                    Revoke
                                </button>
                            )}
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
                                <Key size={16} />
                                New API Key
                            </h3>
                            <button onClick={() => setCreating(false)} className="text-(--base-06)"><X size={16} /></button>
                        </div>
                        <div className="modal-body space-y-4">
                            <div>
                                <label className="input-label">Name</label>
                                <input
                                    type="text"
                                    value={form.name}
                                    onChange={e => setForm({ ...form, name: e.target.value })}
                                    className="input-field w-full"
                                    placeholder="discord-bot-rcon"
                                    maxLength={128}
                                />
                            </div>

                            <div>
                                <label className="input-label">Permissions</label>
                                <div className="space-y-1.5 mt-1">
                                    {ALL_PERMISSIONS.map(p => {
                                        const checked = form.permissions.includes(p.id);
                                        return (
                                            <label key={p.id} className="flex items-start gap-2 p-2 rounded-md border border-(--base-04) cursor-pointer hover:bg-(--base-03)">
                                                <input
                                                    type="checkbox"
                                                    checked={checked}
                                                    onChange={() => setForm({
                                                        ...form,
                                                        permissions: checked
                                                            ? form.permissions.filter(x => x !== p.id)
                                                            : [...form.permissions, p.id],
                                                    })}
                                                    className="mt-0.5"
                                                />
                                                <div className="min-w-0">
                                                    <div className="text-sm font-medium text-(--base-09)">{p.id}</div>
                                                    <div className="text-xs text-(--base-06)">{p.description}</div>
                                                </div>
                                            </label>
                                        );
                                    })}
                                </div>
                            </div>

                            <div>
                                <label className="input-label">Server scope</label>
                                {servers.length === 0 ? (
                                    <p className="text-xs text-(--base-06) mt-1">You don&apos;t own any servers — nothing to scope.</p>
                                ) : (
                                    <div className="space-y-1 mt-1 max-h-40 overflow-y-auto border border-(--base-04) rounded-md p-1">
                                        {servers.map(s => {
                                            const checked = form.servers.includes(s.uuid);
                                            return (
                                                <label key={s.uuid} className="flex items-center gap-2 px-2 py-1 rounded-md cursor-pointer hover:bg-(--base-03)">
                                                    <input
                                                        type="checkbox"
                                                        checked={checked}
                                                        onChange={() => setForm({
                                                            ...form,
                                                            servers: checked
                                                                ? form.servers.filter(x => x !== s.uuid)
                                                                : [...form.servers, s.uuid],
                                                        })}
                                                    />
                                                    <span className="text-sm text-(--base-09)">{s.name}</span>
                                                    <span className="text-xs text-(--base-06) font-mono ml-auto">{s.uuid.slice(0, 8)}…</span>
                                                </label>
                                            );
                                        })}
                                    </div>
                                )}
                            </div>

                            <div>
                                <label className="input-label">Rate limit (req/min)</label>
                                <input
                                    type="number"
                                    min={1}
                                    max={1000}
                                    value={form.ratePerMin}
                                    onChange={e => setForm({ ...form, ratePerMin: parseInt(e.target.value || '60', 10) })}
                                    className="input-field input-mono w-32"
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
                                <Key size={16} />
                                {revealedKey.name} — copy now
                            </h3>
                        </div>
                        <div className="modal-body space-y-3">
                            <p className="text-sm text-(--base-07) flex items-start gap-2">
                                <AlertTriangle size={14} className="text-(--warning-light) shrink-0 mt-0.5" />
                                This is the only time the full key will be shown. Copy it into your
                                secret store now.
                            </p>
                            <div className="flex items-center gap-2 p-3 rounded-md bg-(--base-02) border border-(--base-04) font-mono text-sm break-all">
                                <span className="flex-1">{revealedKey.plaintext}</span>
                                <button
                                    onClick={() => { navigator.clipboard.writeText(revealedKey.plaintext); showToast('Copied.', true); }}
                                    className="btn btn-secondary btn-sm"
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

            {/* Revoke confirmation */}
            {revoking && (
                <div className="modal-overlay animate-fade-in" onClick={() => setRevoking(null)}>
                    <div className="modal-panel max-w-sm" onClick={e => e.stopPropagation()}>
                        <div className="modal-header">
                            <h3 className="modal-title flex items-center gap-2 text-(--error-light)">
                                <AlertTriangle size={18} />
                                Revoke {revoking.name}?
                            </h3>
                        </div>
                        <div className="modal-body">
                            <p className="text-sm text-(--base-07)">
                                Existing calls using this key will fail with 401 immediately.
                                Revoked keys stay listed for audit but can&apos;t be reactivated.
                            </p>
                        </div>
                        <div className="modal-footer">
                            <button onClick={() => setRevoking(null)} className="btn btn-secondary">Cancel</button>
                            <button onClick={handleRevoke} className="btn btn-danger">Revoke</button>
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
