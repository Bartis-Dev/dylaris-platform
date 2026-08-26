"use client";

import { getAccountPolicy, setAccountPolicy, type AccountPolicy } from '@/lib/api/accountPolicy';
import { useSettingsForm } from '@/lib/useSettingsForm';
import Switch from '@/components/ui/Switch';
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
        <section className="card p-5 space-y-4">
            <h2 className="text-base font-display font-semibold text-(--base-09)">Account policy</h2>
            {form.loading || !policy ? (
                <SkeletonFormRow />
            ) : (
                <>
                    <div className="flex items-center justify-between gap-4">
                        <div>
                            <div className="text-sm font-medium text-(--base-09)">Allow username changes</div>
                            <p className="text-xs text-(--base-06)">When off, only admins can rename users.</p>
                        </div>
                        <Switch
                            checked={policy.allowNameChange}
                            onChange={v => form.patch({ allowNameChange: v })}
                            ariaLabel="Allow username changes"
                        />
                    </div>
                    <div className="flex items-center gap-3">
                        <label className="input-label mb-0 shrink-0">Cooldown between user renames (days)</label>
                        <input
                            type="number"
                            min={0}
                            max={3650}
                            value={policy.nameChangeCooldownDays}
                            onChange={e => form.patch({ nameChangeCooldownDays: parseInt(e.target.value || '0', 10) })}
                            className="input-field input-mono w-24"
                        />
                    </div>
                </>
            )}
        </section>
    );
}
