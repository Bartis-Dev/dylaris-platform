"use client";

import { getServerSettings, saveServerSettings, ServerLimitSettings } from '@/lib/api';
import { Server } from 'lucide-react';
import { useSettingsForm } from '@/lib/useSettingsForm';
import SettingsPage from '@/components/settings/SettingsPage';
import SettingsCard, { SettingsRow } from '@/components/settings/SettingsCard';
import { LimitField } from '@/components/settings/LimitField';

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
                    description="How many sub-servers a user may create inside one server. 0 means none may be created."
                >
                    <LimitField
                        id="max-sub-servers"
                        value={s.maxSubServers}
                        onChange={maxSubServers => form.patch({ maxSubServers })}
                        unit="per server"
                    />
                </SettingsRow>
            </SettingsCard>
        </SettingsPage>
    );
}
