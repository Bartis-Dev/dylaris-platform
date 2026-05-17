"use client";

import React, { useState, useEffect } from 'react';
import { getLibrarySettings, saveLibrarySettings, testLibraryConnection, LibrarySettings } from '@/lib/api';
import { Cable, Save, CircleCheck, CircleAlert, HardDrive, Cloud } from 'lucide-react';
import LoadingState from '@/components/LoadingState';

const STORAGE_TYPES = [
    { id: 'local', label: 'Local Path', description: 'Files stored on this server\'s filesystem or a mounted network share (NFS/SMB).', icon: HardDrive },
    { id: 's3', label: 'S3 / Object Storage', description: 'Any S3-compatible API: AWS S3, Cloudflare R2, Backblaze B2, Hetzner, MinIO, Wasabi.', icon: Cloud },
];

// Provider presets fill in endpoint pattern + region hints so admins don't
// have to look up the exact URL format. "custom" leaves all fields blank
// for self-hosted MinIO etc.
interface ProviderPreset {
    id: string;
    label: string;
    endpoint: string;       // template — admins still edit, just pre-fill
    regionHint: string;
    regionPlaceholder: string;
    forcePathStyleDefault: boolean;
    note: string;
}

const S3_PROVIDERS: ProviderPreset[] = [
    { id: 'custom',    label: 'Custom / Other',     endpoint: '',                                                regionHint: 'Provider-specific.',                      regionPlaceholder: 'auto',     forcePathStyleDefault: false, note: '' },
    { id: 'aws',       label: 'AWS S3',             endpoint: 'https://s3.{region}.amazonaws.com',               regionHint: 'AWS region code (e.g. us-east-1).',       regionPlaceholder: 'us-east-1', forcePathStyleDefault: false, note: 'Endpoint auto-derives from region — leave the template literal or write the resolved URL.' },
    { id: 'cloudflare-r2', label: 'Cloudflare R2',  endpoint: 'https://<ACCOUNT_ID>.r2.cloudflarestorage.com',   regionHint: 'R2 only supports "auto".',                 regionPlaceholder: 'auto',     forcePathStyleDefault: false, note: 'Replace <ACCOUNT_ID> with your R2 account ID.' },
    { id: 'backblaze', label: 'Backblaze B2',       endpoint: 'https://s3.{region}.backblazeb2.com',             regionHint: 'B2 region (e.g. us-west-002).',           regionPlaceholder: 'us-west-002', forcePathStyleDefault: false, note: 'Use the S3-compatible endpoint, not the native B2 API.' },
    { id: 'hetzner',   label: 'Hetzner Object Storage', endpoint: 'https://{region}.your-objectstorage.com',     regionHint: 'Hetzner location (hel1, fsn1, nbg1).',    regionPlaceholder: 'hel1',     forcePathStyleDefault: false, note: '' },
    { id: 'wasabi',    label: 'Wasabi',             endpoint: 'https://s3.{region}.wasabisys.com',               regionHint: 'Wasabi region.',                          regionPlaceholder: 'us-east-1', forcePathStyleDefault: false, note: '' },
    { id: 'minio',     label: 'MinIO / Self-hosted', endpoint: 'https://minio.example.com',                      regionHint: 'Often "us-east-1" by convention.',        regionPlaceholder: 'us-east-1', forcePathStyleDefault: true,  note: 'Path-style addressing is enabled — MinIO requires it unless configured otherwise.' },
];

export default function LibraryTab() {
    const [settings, setSettings] = useState<LibrarySettings>({ type: 'local', path: '' });
    const [loading, setLoading] = useState(true);
    const [saving, setSaving] = useState(false);
    const [testing, setTesting] = useState(false);
    const [toast, setToast] = useState<{ msg: string; ok: boolean } | null>(null);
    const [selectedProvider, setSelectedProvider] = useState('custom');

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

    const applyProvider = (id: string) => {
        setSelectedProvider(id);
        const p = S3_PROVIDERS.find(x => x.id === id);
        if (!p || id === 'custom') return;
        setSettings(prev => ({
            ...prev,
            s3Endpoint: prev.s3Endpoint || p.endpoint,
            s3Region: prev.s3Region || p.regionPlaceholder,
        }));
    };

    const currentProvider = S3_PROVIDERS.find(p => p.id === selectedProvider) ?? S3_PROVIDERS[0];

    if (loading) return <LoadingState />;

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
                    {STORAGE_TYPES.map(t => {
                        const Icon = t.icon;
                        const active = settings.type === t.id;
                        return (
                            <button
                                key={t.id}
                                onClick={() => set('type', t.id)}
                                className={`card p-4 text-left transition-all relative ${
                                    active
                                        ? 'border-(--accent) ring-1 ring-(--accent)/40 bg-(--accent-ghost)'
                                        : 'border-(--base-03) hover:border-(--base-05)'
                                }`}
                            >
                                <div className="flex items-start gap-3">
                                    <div className={`w-9 h-9 rounded-md flex items-center justify-center shrink-0 ${active ? 'bg-(--accent)/20 text-(--accent-light)' : 'bg-(--base-03) text-(--base-06)'}`}>
                                        <Icon size={18} />
                                    </div>
                                    <div className="min-w-0">
                                        <div className={`font-medium text-sm flex items-center gap-1.5 ${active ? 'text-(--accent-light)' : 'text-(--base-09)'}`}>
                                            {t.label}
                                            {active && <CircleCheck size={13} className="text-(--accent-light)" />}
                                        </div>
                                        <div className="text-xs text-(--base-06) mt-1">{t.description}</div>
                                    </div>
                                </div>
                            </button>
                        );
                    })}
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
                    <div className="flex flex-col gap-[5px]">
                        <label className="input-label">Provider Preset</label>
                        <select
                            value={selectedProvider}
                            onChange={e => applyProvider(e.target.value)}
                            className="input-field w-full md:w-72"
                        >
                            {S3_PROVIDERS.map(p => (
                                <option key={p.id} value={p.id}>{p.label}</option>
                            ))}
                        </select>
                        <p className="text-xs text-(--base-06) mt-0.5">
                            Pre-fills endpoint pattern + region placeholder. You can still edit every field manually.
                        </p>
                        {currentProvider.note && (
                            <p className="text-xs text-(--accent-light) bg-(--accent)/5 border border-(--accent)/20 rounded-md px-2 py-1.5 mt-1">
                                {currentProvider.note}
                            </p>
                        )}
                    </div>

                    <div className="grid grid-cols-2 gap-4">
                        <div className="flex flex-col gap-[5px]">
                            <label className="input-label">Endpoint URL</label>
                            <input
                                type="text"
                                value={settings.s3Endpoint || ''}
                                onChange={e => set('s3Endpoint', e.target.value)}
                                placeholder={currentProvider.endpoint || 'https://s3.example.com'}
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
                                placeholder={currentProvider.regionPlaceholder}
                                className="input-mono w-full"
                            />
                            <p className="text-xs text-(--base-06)">{currentProvider.regionHint}</p>
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
                            value={settings.s3SecretKey || ''}
                            onChange={e => set('s3SecretKey', e.target.value)}
                            placeholder="••••••••••••••••••••"
                            className="input-mono w-full"
                            autoComplete="new-password"
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
                    className="btn btn-secondary disabled:opacity-40"
                >
                    <Cable size={14} />
                    {testing ? 'Testing...' : 'Test Connection'}
                </button>
                <button
                    onClick={handleSave}
                    disabled={saving}
                    className="btn btn-primary disabled:opacity-40"
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
