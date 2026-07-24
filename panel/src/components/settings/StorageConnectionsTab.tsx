"use client";

import { useState, useEffect, useCallback } from 'react';
import { Plus, Trash2, Pencil, X, CircleCheck, CircleAlert, Cloud, Save, Cable } from 'lucide-react';
import {
    StorageConnection,
    StorageConnectionConfig,
    StorageConnectionInput,
    listStorageConnections, createStorageConnection, updateStorageConnection, deleteStorageConnection, testStorageConnection,
} from '@/lib/api';
import { SkeletonHeader, SkeletonCard } from '@/components/Skeleton';

// editing holds a StorageConnection plus a transient secret. The list never
// carries the secret (secretSet only); a save sends secretAccessKey only when
// the operator typed one, so a blank field keeps the stored credential.
type EditingConnection = StorageConnection & { secretAccessKey?: string };

const EMPTY: EditingConnection = {
    id: 0,
    name: '',
    provider: 's3',
    config: { endpoint: '', region: '', bucket: '', forcePathStyle: true, prefix: '' },
    accessKey: '',
    secretAccessKey: '',
    secretSet: false,
};

// The plain-text s3 config fields, in display order (the secret is handled
// separately below).
const S3_FIELDS: { key: 'endpoint' | 'region' | 'bucket' | 'prefix'; label: string; placeholder?: string }[] = [
    { key: 'endpoint', label: 'Endpoint', placeholder: 'https://fsn1.your-objectstorage.com' },
    { key: 'region', label: 'Region', placeholder: 'us-east-1' },
    { key: 'bucket', label: 'Bucket' },
    { key: 'prefix', label: 'Prefix (optional)' },
];

export default function StorageConnectionsTab() {
    const [connections, setConnections] = useState<StorageConnection[]>([]);
    const [loading, setLoading] = useState(true);
    const [editing, setEditing] = useState<EditingConnection | null>(null);
    const [saving, setSaving] = useState(false);
    const [testingId, setTestingId] = useState<number | null>(null);
    const [toast, setToast] = useState<{ msg: string; ok: boolean } | null>(null);

    const showToast = (msg: string, ok = true) => {
        setToast({ msg, ok });
        setTimeout(() => setToast(null), 3500);
    };

    const reload = useCallback(async () => {
        setLoading(true);
        const res = await listStorageConnections();
        if (res.success && res.connections) setConnections(res.connections);
        setLoading(false);
    }, []);

    useEffect(() => { reload(); }, [reload]);

    const setConfig = (patch: Partial<StorageConnectionConfig>) =>
        setEditing(prev => (prev ? { ...prev, config: { ...prev.config, ...patch } } : prev));

    const handleSave = async () => {
        if (!editing) return;
        if (!editing.name.trim()) { showToast('Name is required.', false); return; }
        const payload: StorageConnectionInput = {
            name: editing.name.trim(),
            provider: 's3',
            config: {
                endpoint: editing.config.endpoint ?? '',
                region: editing.config.region ?? '',
                bucket: editing.config.bucket ?? '',
                forcePathStyle: !!editing.config.forcePathStyle,
                prefix: editing.config.prefix ?? '',
            },
            accessKey: editing.accessKey ?? '',
        };
        // Write-only: only send the secret when the operator typed a new one.
        if (editing.secretAccessKey) payload.secretAccessKey = editing.secretAccessKey;

        setSaving(true);
        const res = editing.id === 0
            ? await createStorageConnection(payload)
            : await updateStorageConnection(editing.id, payload);
        setSaving(false);
        if (res.success) {
            showToast('Connection saved.');
            setEditing(null);
            reload();
        } else {
            showToast('Save failed.', false);
        }
    };

    const handleDelete = async (id: number) => {
        if (!confirm('Delete this storage connection? Features that reference it will fall back to their inline configuration.')) return;
        const res = await deleteStorageConnection(id);
        if (res.success) reload();
        else showToast('Delete failed.', false);
    };

    const handleTest = async (id: number) => {
        setTestingId(id);
        const res = await testStorageConnection(id);
        setTestingId(null);
        const ok = !!(res.success && res.ok);
        showToast(res.message || (ok ? 'Connection OK' : 'Connection failed'), ok);
    };

    if (loading) return (
        <div className="max-w-3xl space-y-6">
            <SkeletonHeader />
            <SkeletonCard height="h-64" />
        </div>
    );

    return (
        <div className="max-w-3xl space-y-6">
            <div>
                <h2 className="h-section mb-1">Storage Connections</h2>
                <p className="text-sm text-(--base-07)">Define an S3 connection once, by name, and reuse it across core storage, modpacks and backups. The secret is encrypted at rest and never leaves the server.</p>
            </div>

            <div className="card card-pad">
                <div className="flex items-center justify-between mb-4">
                    <h3 className="text-sm font-display font-semibold text-(--accent-light)">Connections</h3>
                    <button onClick={() => setEditing({ ...EMPTY })} className="btn btn-primary btn-sm">
                        <Plus size={12} /> Add Connection
                    </button>
                </div>

                {connections.length === 0 ? (
                    <p className="alert alert-info text-xs">No storage connections yet. Add one to reuse the same S3 credentials in several places.</p>
                ) : (
                    <div className="space-y-2">
                        {connections.map(c => (
                            <div key={c.id} className="flex items-center justify-between gap-3 bg-(--base-03) border border-(--base-04) rounded-md px-3 py-2.5">
                                <div className="flex items-center gap-3 min-w-0">
                                    <Cloud size={16} className="text-(--accent-light) shrink-0" />
                                    <div className="min-w-0">
                                        <div className="flex items-center gap-2">
                                            <span className="text-sm font-medium text-(--base-09)">{c.name}</span>
                                            {!c.secretSet && <span className="badge badge-warning">no secret</span>}
                                        </div>
                                        <div className="mono-label">{c.provider}{c.config.bucket ? ` · ${c.config.bucket}` : ''}</div>
                                    </div>
                                </div>
                                <div className="flex items-center gap-1.5">
                                    <button onClick={() => handleTest(c.id)} className="btn btn-secondary btn-sm" title="Test connection" disabled={testingId === c.id}>
                                        <Cable size={12} /> {testingId === c.id ? 'Testing…' : 'Test'}
                                    </button>
                                    <button onClick={() => setEditing({ ...c })} className="btn btn-secondary btn-sm">
                                        <Pencil size={12} /> Edit
                                    </button>
                                    <button onClick={() => handleDelete(c.id)} className="btn btn-danger btn-sm">
                                        <Trash2 size={12} />
                                    </button>
                                </div>
                            </div>
                        ))}
                    </div>
                )}
            </div>

            {editing && (
                <div className="modal-overlay animate-fade-in" onClick={() => setEditing(null)}>
                    <div className="modal-panel w-full max-w-lg" onClick={e => e.stopPropagation()}>
                        <div className="modal-header flex items-center justify-between">
                            <h3 className="modal-title">{editing.id === 0 ? 'New Connection' : 'Edit Connection'}</h3>
                            <button onClick={() => setEditing(null)} className="p-1 text-(--base-06) hover:text-(--base-09)">
                                <X size={16} />
                            </button>
                        </div>
                        <div className="modal-body space-y-4">
                            <div className="form-group">
                                <label className="input-label">Name</label>
                                <input
                                    type="text"
                                    value={editing.name}
                                    onChange={e => setEditing({ ...editing, name: e.target.value })}
                                    className="input-field"
                                    placeholder="hetzner-fsn1"
                                />
                            </div>

                            {S3_FIELDS.map(f => (
                                <div key={f.key} className="form-group">
                                    <label className="input-label">{f.label}</label>
                                    <input
                                        type="text"
                                        value={editing.config[f.key] ?? ''}
                                        onChange={e => setConfig({ [f.key]: e.target.value })}
                                        className="input-field font-mono"
                                        placeholder={f.placeholder}
                                    />
                                </div>
                            ))}

                            <div className="form-group">
                                <label className="input-label">Access Key ID</label>
                                <input
                                    type="text"
                                    value={editing.accessKey}
                                    onChange={e => setEditing({ ...editing, accessKey: e.target.value })}
                                    className="input-field font-mono"
                                    autoComplete="off"
                                />
                            </div>

                            <div className="form-group">
                                <label className="input-label">Secret Access Key</label>
                                <input
                                    type="password"
                                    value={editing.secretAccessKey ?? ''}
                                    onChange={e => setEditing({ ...editing, secretAccessKey: e.target.value })}
                                    className="input-field font-mono"
                                    autoComplete="new-password"
                                    placeholder={editing.secretSet ? 'Leave blank to keep the stored secret' : ''}
                                />
                            </div>

                            <div className="flex items-center justify-between">
                                <label className="input-label">Force Path Style</label>
                                <button
                                    type="button"
                                    onClick={() => setConfig({ forcePathStyle: !editing.config.forcePathStyle })}
                                    className={`toggle-track ${editing.config.forcePathStyle ? 'toggle-track-on' : 'toggle-track-off'}`}
                                    role="switch"
                                    aria-checked={!!editing.config.forcePathStyle}
                                >
                                    <span className={`toggle-knob ${editing.config.forcePathStyle ? 'toggle-knob-on' : 'toggle-knob-off'}`} />
                                </button>
                            </div>
                            <p className="text-xs text-(--base-06)">Hetzner Object Storage needs path-style ON. AWS S3 / Cloudflare R2 typically OFF.</p>
                        </div>
                        <div className="modal-footer">
                            <button onClick={() => setEditing(null)} className="btn btn-secondary">Cancel</button>
                            <button onClick={handleSave} className="btn btn-primary" disabled={saving}>
                                <Save size={13} /> {saving ? 'Saving…' : 'Save'}
                            </button>
                        </div>
                    </div>
                </div>
            )}

            {toast && (
                <div className="toast-container">
                    <div className="toast">
                        <div className={`toast-bar ${toast.ok ? 'bg-(--success-light)' : 'bg-(--error-light)'}`} />
                        {toast.ok ? <CircleCheck size={14} /> : <CircleAlert size={14} />}
                        <span className="text-sm text-(--base-09)">{toast.msg}</span>
                    </div>
                </div>
            )}
        </div>
    );
}
