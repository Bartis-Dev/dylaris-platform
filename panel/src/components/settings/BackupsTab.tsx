"use client";

import React, { useState, useEffect, useCallback } from 'react';
import {
    Plus, Trash2, Pencil, X, CircleCheck, CircleAlert, HardDrive, Cloud, Save, Cable,
} from 'lucide-react';
import {
    BackupStorage,
    listBackupStorages, createBackupStorage, updateBackupStorage, deleteBackupStorage, testBackupStorage,
} from '@/lib/api';
import LoadingState from '@/components/LoadingState';

interface LocalConfig {
    basePath: string;
}
interface S3Config {
    endpoint: string;
    region: string;
    bucket: string;
    accessKeyId: string;
    secretAccessKey: string;
    forcePathStyle: boolean;
}

const EMPTY_LOCAL: LocalConfig = { basePath: '/var/lib/dylaris/backups' };
const EMPTY_S3: S3Config = {
    endpoint: '',
    region: 'us-east-1',
    bucket: '',
    accessKeyId: '',
    secretAccessKey: '',
    forcePathStyle: true,
};

export default function BackupsTab() {
    const [storages, setStorages] = useState<BackupStorage[]>([]);
    const [loading, setLoading] = useState(true);
    const [editing, setEditing] = useState<BackupStorage | null>(null);
    const [toast, setToast] = useState<{ msg: string; ok: boolean } | null>(null);

    const showToast = (msg: string, ok = true) => {
        setToast({ msg, ok });
        setTimeout(() => setToast(null), 3500);
    };

    const reload = useCallback(async () => {
        setLoading(true);
        const res = await listBackupStorages();
        if (res.success && res.storages) setStorages(res.storages);
        setLoading(false);
    }, []);

    useEffect(() => { reload(); }, [reload]);

    const handleNew = (provider: 'local' | 's3') => {
        setEditing({
            id: 0,
            name: provider === 's3' ? 'Hetzner Object Storage' : 'Local backups',
            provider,
            config: (provider === 's3' ? EMPTY_S3 : EMPTY_LOCAL) as unknown as Record<string, unknown>,
            isDefault: storages.length === 0,
        });
    };

    const handleSave = async () => {
        if (!editing) return;
        const payload = { ...editing };
        const res = editing.id === 0
            ? await createBackupStorage(payload)
            : await updateBackupStorage(editing.id, payload);
        if (res.success) {
            showToast('Storage saved.');
            setEditing(null);
            reload();
        } else {
            showToast('Save failed.', false);
        }
    };

    const handleDelete = async (id: number) => {
        if (!confirm('Delete this storage? Existing backups in this storage become inaccessible.')) return;
        const res = await deleteBackupStorage(id);
        if (res.success) reload();
    };

    const handleTest = async (id: number) => {
        const res = await testBackupStorage(id);
        showToast(res.success ? 'Connection OK' : (res.message || 'Connection failed'), res.success);
    };

    if (loading) return <LoadingState />;

    return (
        <div className="max-w-3xl space-y-6">
            <div>
                <h2 className="h-section mb-1">Backup Storages</h2>
                <p className="text-sm text-(--base-07)">Configure where backup archives are written. Each server can pick a storage per backup-job. Set one as default to apply it to jobs that don't pick explicitly.</p>
            </div>

            <div className="card card-pad">
                <div className="flex items-center justify-between mb-4">
                    <h3 className="text-sm font-display font-semibold text-(--accent-light)">Configured Storages</h3>
                    <div className="flex gap-2">
                        <button onClick={() => handleNew('local')} className="btn btn-secondary btn-sm">
                            <HardDrive size={12} /> Add Local
                        </button>
                        <button onClick={() => handleNew('s3')} className="btn btn-primary btn-sm">
                            <Cloud size={12} /> Add S3
                        </button>
                    </div>
                </div>

                {storages.length === 0 ? (
                    <p className="alert alert-info text-xs">No backup storages configured yet. Add one to start scheduling backups.</p>
                ) : (
                    <div className="space-y-2">
                        {storages.map(s => (
                            <div key={s.id} className="flex items-center justify-between gap-3 bg-(--base-03) border border-(--base-04) rounded-md px-3 py-2.5">
                                <div className="flex items-center gap-3 min-w-0">
                                    {s.provider === 's3'
                                        ? <Cloud size={16} className="text-(--accent-light) shrink-0" />
                                        : <HardDrive size={16} className="text-(--primary-light) shrink-0" />}
                                    <div className="min-w-0">
                                        <div className="flex items-center gap-2">
                                            <span className="text-sm font-medium text-(--base-09)">{s.name}</span>
                                            {s.isDefault && <span className="badge badge-accent">default</span>}
                                        </div>
                                        <div className="mono-label">{s.provider}</div>
                                    </div>
                                </div>
                                <div className="flex items-center gap-1.5">
                                    <button onClick={() => handleTest(s.id)} className="btn btn-secondary btn-sm" title="Test connection">
                                        <Cable size={12} /> Test
                                    </button>
                                    <button onClick={() => setEditing(s)} className="btn btn-secondary btn-sm">
                                        <Pencil size={12} /> Edit
                                    </button>
                                    <button onClick={() => handleDelete(s.id)} className="btn btn-danger btn-sm">
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
                            <h3 className="modal-title">{editing.id === 0 ? 'New Storage' : 'Edit Storage'}</h3>
                            <button onClick={() => setEditing(null)} className="p-1 text-(--base-06) hover:text-(--base-09)">
                                <X size={16} />
                            </button>
                        </div>
                        <div className="modal-body space-y-4">
                            <div className="form-group">
                                <label className="input-label">Name</label>
                                <input type="text" value={editing.name} onChange={e => setEditing({ ...editing, name: e.target.value })} className="input-field" />
                            </div>

                            {editing.provider === 'local' && (
                                <div className="form-group">
                                    <label className="input-label">Base Path</label>
                                    <input
                                        type="text"
                                        value={(editing.config as unknown as LocalConfig).basePath || ''}
                                        onChange={e => setEditing({ ...editing, config: { ...(editing.config as object), basePath: e.target.value } })}
                                        className="input-field font-mono"
                                        placeholder="/var/lib/dylaris/backups"
                                    />
                                    <p className="text-xs text-(--base-06)">Backups land in this directory on the Core host. Use a mount that survives container restarts.</p>
                                </div>
                            )}

                            {editing.provider === 's3' && (
                                <>
                                    {(['endpoint', 'region', 'bucket', 'accessKeyId', 'secretAccessKey'] as (keyof S3Config)[]).map(field => (
                                        <div key={field} className="form-group">
                                            <label className="input-label">{field}</label>
                                            <input
                                                type={field === 'secretAccessKey' ? 'password' : 'text'}
                                                value={String((editing.config as unknown as S3Config)[field] ?? '')}
                                                onChange={e => setEditing({ ...editing, config: { ...(editing.config as object), [field]: e.target.value } })}
                                                className="input-field font-mono"
                                                placeholder={field === 'endpoint' ? 'https://fsn1.your-objectstorage.com' : ''}
                                            />
                                        </div>
                                    ))}
                                    <div className="flex items-center justify-between">
                                        <label className="input-label">Force Path Style</label>
                                        <button
                                            type="button"
                                            onClick={() => setEditing({ ...editing, config: { ...(editing.config as object), forcePathStyle: !(editing.config as unknown as S3Config).forcePathStyle } })}
                                            className="toggle-track"
                                            role="switch"
                                            aria-checked={(editing.config as unknown as S3Config).forcePathStyle}
                                        >
                                            <span className={`toggle-knob ${(editing.config as unknown as S3Config).forcePathStyle ? 'translate-x-5 bg-(--accent)' : 'translate-x-0.5 bg-(--base-05)'}`} />
                                        </button>
                                    </div>
                                    <p className="text-xs text-(--base-06)">Hetzner Object Storage needs path-style ON. AWS S3 / Cloudflare R2 typically OFF.</p>
                                </>
                            )}

                            <div className="flex items-center justify-between pt-2 border-t border-(--base-03)">
                                <label className="input-label">Use as Default</label>
                                <button
                                    type="button"
                                    onClick={() => setEditing({ ...editing, isDefault: !editing.isDefault })}
                                    className="toggle-track"
                                    role="switch"
                                    aria-checked={editing.isDefault}
                                >
                                    <span className={`toggle-knob ${editing.isDefault ? 'translate-x-5 bg-(--accent)' : 'translate-x-0.5 bg-(--base-05)'}`} />
                                </button>
                            </div>
                        </div>
                        <div className="modal-footer">
                            <button onClick={() => setEditing(null)} className="btn btn-secondary">Cancel</button>
                            <button onClick={handleSave} className="btn btn-primary">
                                <Save size={13} /> Save
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
