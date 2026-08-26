"use client";

import { getAccountPolicy, setAccountPolicy, type AccountPolicy } from '@/lib/api/accountPolicy';
import { useSettingsForm } from '@/lib/useSettingsForm';
import { SwitchRow } from '@/components/ui/Switch';
import SettingsCard, { SettingsRow } from '@/components/settings/SettingsCard';
import { SkeletonFormRow } from '@/components/Skeleton';

// Platform-wide rules about accounts, not a property of any one of them. It
// lived above the user roster, which put a setting on a page that is otherwise
// a list of people - so it moved here, next to the other account policies,
// when the roster moved to /admin/users.
export default function AccountPolicyCard() {
    const form = useSettingsForm<AccountPolicy>({
        load: async () => {
            const res = await getAccountPolicy();
            return res.success && res.policy ? res.policy : null;
        },
        save: async policy => {
            const res = await setAccountPolicy(policy);
            return { ok: !!res.success, message: res.message };
        },
        successMessage: 'Account policy saved',
    });

    const policy = form.value;

    return (
        <SettingsCard title="Account policy" form={form}>
            {form.loading || !policy ? (
                <SkeletonFormRow />
            ) : (
                <>
                    <SwitchRow
                        label="Allow username changes"
                        description="When off, only admins can rename users."
                        checked={policy.allowNameChange}
                        onChange={v => form.patch({ allowNameChange: v })}
                    />
                    <SettingsRow
                        label="Cooldown between user renames"
                        htmlFor="account-rename-cooldown"
                    >
                        <input
                            id="account-rename-cooldown"
                            type="number"
                            min={0}
                            max={3650}
                            value={policy.nameChangeCooldownDays}
                            onChange={e => form.patch({ nameChangeCooldownDays: parseInt(e.target.value || '0', 10) })}
                            className="input-field input-mono w-24"
                        />
                        <span className="text-sm text-(--base-07)">days</span>
                    </SettingsRow>
                </>
            )}
        </SettingsCard>
    );
}
