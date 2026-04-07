"use client";

import React, { useState, useEffect } from 'react';
import { getFileManagerSettings, saveFileManagerSettings, FileManagerSettings } from '@/lib/api';
import { RefreshCw, Save, CircleCheck, CircleAlert } from 'lucide-react';

const UNITS = [
    { label: 'MB', multiplier: 1024 * 1024 },
    { label: 'GB', multiplier: 1024 * 1024 * 1024 },
];

function bytesToDisplay(bytes: number): { value: number; unit: string } {
    if (bytes >= 1024 * 1024 * 1024 && bytes % (1024 * 1024 * 1024) === 0) {
        return { value: bytes / (1024 * 1024 * 1024), unit: 'GB' };
    }
    return { value: Math.round(bytes / (1024 * 1024)), unit: 'MB' };
}

function displayToBytes(value: number, unit: string): number {
    const u = UNITS.find(u => u.label === unit);
    return value * (u?.multiplier || 1);
}

interface LimitFieldProps {
    label: string;
    bytes: number;
    onChange: (bytes: number) => void;
}

function LimitField({ label, bytes, onChange }: LimitFieldProps) {
    const display = bytesToDisplay(bytes);
    const [value, setValue] = useState(display.value);
    const [unit, setUnit] = useState(display.unit);

    useEffect(() => {
        const d = bytesToDisplay(bytes);
        setValue(d.value);
        setUnit(d.unit);
    }, [bytes]);

    const handleValueChange = (v: number) => {
        setValue(v);
        onChange(displayToBytes(v, unit));
    };

    const handleUnitChange = (u: string) => {
        setUnit(u);
        onChange(displayToBytes(value, u));
    };

    return (
        <div className="flex flex-col gap-[5px]">
            <label className="input-label">{label}</label>
            <div className="flex gap-2">
                <input
                    type="number"
                    min={1}
                    value={value}
                    onChange={e => handleValueChange(Math.max(1, parseInt(e.target.value) || 1))}
                    className="input-field w-24 text-right"
                />
                <select
                    value={unit}
                    onChange={e => handleUnitChange(e.target.value)}
                    className="input-field w-20"
                >
                    {UNITS.map(u => (
                        <option key={u.label} value={u.label}>{u.label}</option>
                    ))}
                </select>
            </div>
        </div>
    );
}

export default function FileManagerTab() {
    const [settings, setSettings] = useState<FileManagerSettings>({
        adminUploadLimit: 2 * 1024 * 1024 * 1024,
        adminDownloadLimit: 5 * 1024 * 1024 * 1024,
        userUploadLimit: 500 * 1024 * 1024,
        userDownloadLimit: 1 * 1024 * 1024 * 1024,
    });
    const [loading, setLoading] = useState(true);
    const [saving, setSaving] = useState(false);
    const [toast, setToast] = useState<{ msg: string; ok: boolean } | null>(null);

    const showToast = (msg: string, ok = true) => {
        setToast({ msg, ok });
        setTimeout(() => setToast(null), 3500);
    };

    useEffect(() => {
        getFileManagerSettings().then(res => {
            if (res.success && res.settings) setSettings(res.settings);
            setLoading(false);
        });
    }, []);

    const handleSave = async () => {
        setSaving(true);
        const res = await saveFileManagerSettings(settings);
        if (res.success) {
            showToast('File manager settings saved.');
        } else {
            showToast(res.message || 'Save failed.', false);
        }
        setSaving(false);
    };

    const set = (key: keyof FileManagerSettings, value: number) =>
        setSettings(prev => ({ ...prev, [key]: value }));

    if (loading) return <div className="flex items-center justify-center h-40 text-(--base-07)"><RefreshCw size={30} className="animate-spin" /></div>;

    return (
        <div className="max-w-2xl space-y-6">
            <div>
                <h2 className="text-base font-display font-bold text-(--base-09) mb-1">Transfer Limits</h2>
                <p className="text-sm text-(--base-07)">Configure maximum file sizes for uploads and downloads. Separate limits for admins and regular users.</p>
            </div>

            {/* Admin Limits */}
            <div className="card p-5">
                <h3 className="text-sm font-display font-semibold text-(--accent-light) mb-4">Admin Limits</h3>
                <div className="grid grid-cols-2 gap-4">
                    <LimitField label="Upload Limit" bytes={settings.adminUploadLimit} onChange={v => set('adminUploadLimit', v)} />
                    <LimitField label="Download Limit" bytes={settings.adminDownloadLimit} onChange={v => set('adminDownloadLimit', v)} />
                </div>
            </div>

            {/* User Limits */}
            <div className="card p-5">
                <h3 className="text-sm font-display font-semibold text-(--base-08) mb-4">User Limits</h3>
                <div className="grid grid-cols-2 gap-4">
                    <LimitField label="Upload Limit" bytes={settings.userUploadLimit} onChange={v => set('userUploadLimit', v)} />
                    <LimitField label="Download Limit" bytes={settings.userDownloadLimit} onChange={v => set('userDownloadLimit', v)} />
                </div>
            </div>

            {/* Save */}
            <div className="flex gap-3 pt-2">
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
