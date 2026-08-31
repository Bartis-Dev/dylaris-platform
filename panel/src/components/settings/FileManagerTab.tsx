"use client";

import { useState, useEffect } from 'react';
import { FolderOpen } from 'lucide-react';
import { getFileManagerSettings, saveFileManagerSettings, FileManagerSettings } from '@/lib/api';
import { useSettingsForm } from '@/lib/useSettingsForm';
import SettingsPage from '@/components/settings/SettingsPage';
import SettingsCard, { SettingsGroup } from '@/components/settings/SettingsCard';

const UNITS = [
    { label: 'MB', multiplier: 1024 * 1024 },
    { label: 'GB', multiplier: 1024 * 1024 * 1024 },
];

const DEFAULTS: FileManagerSettings = {
    adminUploadLimit: 2 * 1024 * 1024 * 1024,
    adminDownloadLimit: 5 * 1024 * 1024 * 1024,
    userUploadLimit: 500 * 1024 * 1024,
    userDownloadLimit: 1 * 1024 * 1024 * 1024,
};

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
    id: string;
    label: string;
    bytes: number;
    onChange: (bytes: number) => void;
}

function LimitField({ id, label, bytes, onChange }: LimitFieldProps) {
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
            <label className="input-label" htmlFor={id}>{label}</label>
            <div className="flex gap-2">
                <input
                    id={id}
                    type="number"
                    min={1}
                    value={value}
                    onChange={e => handleValueChange(Math.max(1, parseInt(e.target.value) || 1))}
                    className="input-field w-24 text-right"
                />
                <select
                    value={unit}
                    onChange={e => handleUnitChange(e.target.value)}
                    aria-label={`${label} unit`}
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
    const form = useSettingsForm<FileManagerSettings>({
        load: async () => {
            const res = await getFileManagerSettings();
            return res.success && res.settings ? res.settings : null;
        },
        save: async value => {
            const res = await saveFileManagerSettings(value);
            return { ok: res.success, message: res.message };
        },
        successMessage: 'File manager settings saved.',
    });

    const s = form.value ?? DEFAULTS;
    const set = (key: keyof FileManagerSettings, value: number) => form.patch({ [key]: value });

    return (
        <SettingsPage
            title="Transfer limits"
            icon={FolderOpen}
            description="Maximum file sizes for uploads and downloads in the browser file manager only. The Beam desktop app is not governed by these; it enforces its own upload limits."
            width="2xl"
            loading={form.loading}
        >
            <SettingsCard
                title="File manager"
                description="Separate ceilings for admins and regular users."
                form={form}
            >
                <SettingsGroup
                    title="Admin limits"
                    first
                    help={
                        <>
                            <p className="mb-2">
                                The largest single file an admin may move through the browser file
                                manager. Uploads and downloads are capped separately.
                            </p>
                            <p className="mb-2">
                                This is always a ceiling: there is no &quot;unlimited&quot; here and no
                                &quot;none&quot;. The smallest you can set is 1, and a value that never
                                got saved falls back to the built-in default rather than to zero.
                            </p>
                            <p>
                                It governs the browser only. The Beam desktop app has its own upload
                                limits under Beam, and SFTP has none of these at all.
                            </p>
                        </>
                    }
                >
                    <div className="grid grid-cols-2 gap-4">
                        <LimitField id="fm-admin-up" label="Upload limit" bytes={s.adminUploadLimit} onChange={v => set('adminUploadLimit', v)} />
                        <LimitField id="fm-admin-down" label="Download limit" bytes={s.adminDownloadLimit} onChange={v => set('adminDownloadLimit', v)} />
                    </div>
                </SettingsGroup>

                <SettingsGroup
                    title="User limits"
                    help={
                        <>
                            <p className="mb-2">
                                The same two ceilings for everyone who is not an admin. They are read
                                per request from the caller&apos;s own role, so a user promoted to admin
                                gets the admin ceiling on their next transfer, with nothing to
                                re-save.
                            </p>
                            <p>
                                Set these below the admin pair, not above: nothing stops you, and the
                                result is a support ticket that reads like a permissions bug.
                            </p>
                        </>
                    }
                >
                    <div className="grid grid-cols-2 gap-4">
                        <LimitField id="fm-user-up" label="Upload limit" bytes={s.userUploadLimit} onChange={v => set('userUploadLimit', v)} />
                        <LimitField id="fm-user-down" label="Download limit" bytes={s.userDownloadLimit} onChange={v => set('userDownloadLimit', v)} />
                    </div>
                </SettingsGroup>
            </SettingsCard>
        </SettingsPage>
    );
}
