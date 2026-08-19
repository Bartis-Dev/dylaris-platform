"use client";

import { useEffect, useState } from 'react';
import { getAccountPolicy, setAccountPolicy, type AccountPolicy } from '@/lib/api/accountPolicy';

// Platform-wide rules about accounts, not a property of any one of them. It
// lived above the user roster, which put a setting on a page that is otherwise
// a list of people - so it moved here, next to the other account policies,
// when the roster moved to /admin/users.
export default function AccountPolicyCard() {
    const [policy, setPolicy] = useState<AccountPolicy | null>(null);
    const [saving, setSaving] = useState(false);
    const [toast, setToast] = useState<{ msg: string; ok: boolean } | null>(null);

    useEffect(() => {
        getAccountPolicy().then(res => {
            if (res.success && res.policy) setPolicy(res.policy);
        });
    }, []);

    if (!policy) return null;

    const save = async () => {
        setSaving(true);
        const res = await setAccountPolicy(policy);
        setSaving(false);
        setToast({ msg: res.success ? 'Saved.' : (res.message || 'Save failed'), ok: !!res.success });
        setTimeout(() => setToast(null), 3000);
    };

    return (
        <section className="card p-5 space-y-4">
            <h2 className="text-base font-display font-semibold text-(--base-09)">Account Policy</h2>
            <div className="flex items-center justify-between">
                <div>
                    <div className="text-sm font-medium text-(--base-09)">Allow username changes</div>
                    <p className="text-xs text-(--base-06)">When off, only admins can rename users.</p>
                </div>
                <button
                    type="button"
                    role="switch"
                    aria-checked={policy.allowNameChange}
                    onClick={() => setPolicy({ ...policy, allowNameChange: !policy.allowNameChange })}
                    className={`toggle-track ${policy.allowNameChange ? 'toggle-track-on' : 'toggle-track-off'}`}
                >
                    <span className={`toggle-knob ${policy.allowNameChange ? 'toggle-knob-on' : 'toggle-knob-off'}`} />
                </button>
            </div>
            <div className="flex items-center gap-3">
                <label className="input-label">Cooldown between user renames (days)</label>
                <input
                    type="number"
                    min={0}
                    max={3650}
                    value={policy.nameChangeCooldownDays}
                    onChange={e => setPolicy({ ...policy, nameChangeCooldownDays: parseInt(e.target.value || '0', 10) })}
                    className="input-field input-mono w-24"
                />
            </div>
            <div className="flex items-center justify-end">
                <button type="button" onClick={save} className="btn btn-primary btn-sm disabled:opacity-40" disabled={saving}>
                    {saving ? 'Saving…' : 'Save'}
                </button>
            </div>
            {toast && (
                <div className={`text-xs ${toast.ok ? 'text-(--success-light)' : 'text-(--error-light)'}`}>{toast.msg}</div>
            )}
        </section>
    );
}
