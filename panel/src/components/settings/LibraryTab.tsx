"use client";

import React, { useState, useEffect } from 'react';
import { getLibrarySettings, saveLibrarySettings, testLibraryConnection, LibrarySettings } from '@/lib/api';
import { RefreshCw, Cable, Save, CircleCheck, CircleAlert } from 'lucide-react';

const STORAGE_TYPES = [
    { id: 'local', label: 'Local Path', description: 'Files stored on this server\'s filesystem or a mounted network share (NFS/SMB).' },
    { id: 's3', label: 'S3 / Object Storage', description: 'Compatible with AWS S3, MinIO, Backblaze B2, etc.' },
];

export default function LibraryTab() {
    const [settings, setSettings] = useState<LibrarySettings>({ type: 'local', path: '' });
    const [loading, setLoading] = useState(true);
    const [saving, setSaving] = useState(false);
    const [testing, setTesting] = useState(false);
    const [toast, setToast] = useState<{ msg: string; ok: boolean } | null>(null);

    const showToast = (msg: string, ok = true) => {
        setToast({ msg, ok });
        setTimeout(() => setToast(null), 3500);
    };

    useEffect(() => {
        getLibrarySettings().then(res => {
            if (res.success && res.settings) setSettings(res.settings);
            setLoading(false);
        });
    }, []);

    const handleSave = async () => {
        setSaving(true);
        const res = await saveLibrarySettings(settings);
        if (res.success) {
            showToast('Library settings saved.');
        } else {
            showToast(res.message || 'Save failed.', false);
        }
        setSaving(false);
    };

    const handleTest = async () => {
        setTesting(true);
        const res = await testLibraryConnection();
        if (res.success) {
            showToast('Connection successful!');
        } else {
            showToast(res.message || 'Connection test failed.', false);
        }
        setTesting(false);
    };

    const set = (key: keyof LibrarySettings, value: string) =>
        setSettings(prev => ({ ...prev, [key]: value }));

    if (loading) return <div className="flex items-center justify-center h-40 text-(--base-07)"><RefreshCw size={30} className="animate-spin" /></div>;

    return (
        <div className="max-w-2xl space-y-6">
            <div>
                <h2 className="text-base font-display font-bold text-(--base-09) mb-1">Library Storage</h2>
                <p className="text-sm text-(--base-07)">Configure where shared server files (JARs, modpacks) are stored. These files appear in the Library module for server setup.</p>
            </div>

            {/* Storage type selector */}
            <div>
                <label className="input-label mb-2 block">Storage Type</label>
                <div className="grid grid-cols-2 gap-3">
                    {STORAGE_TYPES.map(t => (
                        <button
                            key={t.id}
                            onClick={() => set('type', t.id)}
                            className={`card p-4 text-left transition-all ${settings.type === t.id ? 'border-(--accent-border) bg-(--accent-ghost)' : 'hover:border-(--base-05)'}`}
                        >
                            <div className="font-medium text-sm text-(--base-09)">{t.label}</div>
                            <div className="text-xs text-(--base-06) mt-1">{t.description}</div>
                        </button>
                    ))}
                </div>
            </div>

            {/* Local path fields */}
            {settings.type === 'local' && (
                <div className="flex flex-col gap-[5px]">
                    <label className="input-label">Library Path</label>
                    <input
                        type="text"
                        value={settings.path}
                        onChange={e => set('path', e.target.value)}
                        placeholder="/mnt/library or /opt/dylaris/library"
                        className="input-mono w-full"
                    />
                    <p className="text-xs text-(--base-06) mt-0.5">Absolute path on the server. For NFS/SMB mounts, ensure the path is already mounted before using.</p>
                </div>
            )}

            {/* S3 fields */}
            {settings.type === 's3' && (
                <div className="space-y-4">
                    <div className="grid grid-cols-2 gap-4">
                        <div className="flex flex-col gap-[5px]">
                            <label className="input-label">Endpoint URL</label>
                            <input
                                type="text"
                                value={settings.s3Endpoint || ''}
                                onChange={e => set('s3Endpoint', e.target.value)}
                                placeholder="https://s3.amazonaws.com"
                                className="input-mono w-full"
                            />
                        </div>
                        <div className="flex flex-col gap-[5px]">
                            <label className="input-label">Bucket Name</label>
                            <input
                                type="text"
                                value={settings.s3Bucket || ''}
                                onChange={e => set('s3Bucket', e.target.value)}
                                placeholder="my-library-bucket"
                                className="input-mono w-full"
                            />
                        </div>
                        <div className="flex flex-col gap-[5px]">
                            <label className="input-label">Region</label>
                            <input
                                type="text"
                                value={settings.s3Region || ''}
                                onChange={e => set('s3Region', e.target.value)}
                                placeholder="us-east-1"
                                className="input-mono w-full"
                            />
                        </div>
                        <div className="flex flex-col gap-[5px]">
                            <label className="input-label">Access Key</label>
                            <input
                                type="text"
                                value={settings.s3AccessKey || ''}
                                onChange={e => set('s3AccessKey', e.target.value)}
                                placeholder="AKIAIOSFODNN7EXAMPLE"
                                className="input-mono w-full"
                            />
                        </div>
                    </div>
                    <div className="flex flex-col gap-[5px]">
                        <label className="input-label">Secret Key</label>
                        <input
                            type="password"
                            placeholder="••••••••••••••••••••"
                            className="input-mono w-full"
                        />
                        <p className="text-xs text-(--base-06) mt-0.5">The secret key is write-only. Leave blank to keep the existing value.</p>
                    </div>
                </div>
            )}

            {/* Actions */}
            <div className="flex gap-3 pt-2">
                <button
                    onClick={handleTest}
                    disabled={testing}
                    className="btn btn-secondary px-4 py-2 text-sm disabled:opacity-50"
                >
                    <Cable size={14} />
                    {testing ? 'Testing...' : 'Test Connection'}
                </button>
                <button
                    onClick={handleSave}
                    disabled={saving}
                    className="btn btn-primary px-6 py-2 text-sm disabled:opacity-50"
                >
                    <Save size={14} />
                    {saving ? 'Saving...' : 'Save Settings'}
                </button>
            </div>

            {/* Toast */}
            {toast && (
                <div className="toast-container">
                    <div className="toast">
                        <div className={`toast-bar ${toast.ok ? 'bg-(--success-light)' : 'bg-(--error-light)'}`}></div>
                        {toast.ok ? <CircleCheck size={14} /> : <CircleAlert size={14} />}
                        <span className="text-sm text-(--base-09)">{toast.msg}</span>
                    </div>
                </div>
            )}
        </div>
    );
}
