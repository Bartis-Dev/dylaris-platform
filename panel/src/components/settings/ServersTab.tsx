"use client";

import { getServerSettings, saveServerSettings, ServerLimitSettings } from '@/lib/api';
import { Server } from 'lucide-react';
import { useSettingsForm } from '@/lib/useSettingsForm';
import SettingsPage from '@/components/settings/SettingsPage';
import SettingsCard, { SettingsRow } from '@/components/settings/SettingsCard';

const DEFAULTS: ServerLimitSettings = { maxSubServers: 3 };

export default function ServersTab() {
    const form = useSettingsForm<ServerLimitSettings>({
        load: async () => {
            const res = await getServerSettings();
            return res.success && res.settings ? res.settings : null;
        },
        save: async value => {
            const res = await saveServerSettings(value);
            return { ok: res.success, message: res.message };
        },
        successMessage: 'Server settings saved.',
    });

    const s = form.value ?? DEFAULTS;

    return (
        <SettingsPage
            title="Server limits"
            icon={Server}
            description="Defaults that apply to every server on the platform."
            width="2xl"
            loading={form.loading}
        >
            <SettingsCard title="Sub-servers" form={form}>
                <SettingsRow
                    label="Max sub-servers per server"
                    htmlFor="max-sub-servers"
                    description="How many sub-servers a user may create inside one server. 0 means no cap."
                >
                    <input
                        id="max-sub-servers"
                        type="number"
                        min={0}
                        max={100}
                        value={s.maxSubServers}
                        onChange={e => form.patch({ maxSubServers: Math.max(0, parseInt(e.target.value) || 0) })}
                        className="input-field input-mono w-28 text-center [appearance:textfield] [&::-webkit-outer-spin-button]:appearance-none [&::-webkit-inner-spin-button]:appearance-none"
                    />
                    <span className="text-xs text-(--base-06) w-24">
                        {s.maxSubServers === 0 ? 'Unlimited' : 'per server'}
                    </span>
                </SettingsRow>
            </SettingsCard>
        </SettingsPage>
    );
}
