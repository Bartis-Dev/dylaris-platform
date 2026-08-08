"use client";

import React, { useEffect, useState } from 'react';
import {
    Users, ShieldCheck, Mail, KeyRound, Trash2, HelpCircle, Send, Loader2, CircleCheck, CircleAlert, Plus, X, FileText,
} from 'lucide-react';
import {
    AuthPolicy, SMTPConfig,
    getAuthPolicy, saveAuthPolicy,
    getSMTPConfig, saveSMTPConfig, testSendSMTP,
    getDemoAccount, setDemoAccount,
} from '@/lib/api/authSettings';
import { getAdminSecurityQuestionPool, setAdminSecurityQuestionPool } from '@/lib/api/securityQuestions';
import { getAuditPolicy, saveAuditPolicy, AuditPolicy } from '@/lib/api/serverAudit';
import { Skeleton, SkeletonText, SkeletonFormRow } from '@/components/Skeleton';
import { useAppData } from '@/lib/AppDataContext';

const ENCRYPTION_OPTIONS = [
    { value: 'starttls', label: 'STARTTLS (port 587)' },
    { value: 'tls', label: 'Implicit TLS (port 465)' },
    { value: 'none', label: 'None — plaintext (private network only)' },
];

export default function UserManagementTab() {
    // The demo-account feature only exists on the hosted (store-enabled) build.
    const { featureFlags } = useAppData();
    return (
        <div className="space-y-8 max-w-3xl">
            <header>
                <h2 className="text-lg font-display flex items-center gap-2">
                    <Users size={18} className="text-(--accent-light)" /> User Management
                </h2>
                <p className="text-sm text-(--base-06) mt-1">
                    Registration, authentication, password reset and lifecycle policies.
                </p>
            </header>

            <AuthPolicySection />
            <SecurityQuestionsPoolSection />
            {featureFlags.store && <DemoAccountSection />}
            <AutoDeleteSection />
            <AuditPolicySection />
            <SMTPSection />
        </div>
    );
}

// ── Demo account section ──────────────────────────────────────────────
// Designates a single read-only account that sees the demo servers and is
// forced GET-only server-side. Create a normal user first, then name it here.
function DemoAccountSection() {
    const [username, setUsername] = useState('');
    const [loading, setLoading] = useState(true);
    const [saving, setSaving] = useState(false);
    const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null);

    useEffect(() => {
        getDemoAccount().then(res => {
            if (res.success) setUsername(res.username || '');
        }).finally(() => setLoading(false));
    }, []);

    const save = async () => {
        setSaving(true);
        setMsg(null);
        const res = await setDemoAccount(username.trim());
        setSaving(false);
        if (res.success) {
            setUsername(res.username || '');
            setMsg({ ok: true, text: res.username ? `Demo account set to "${res.username}".` : 'Demo account cleared.' });
        } else {
            setMsg({ ok: false, text: res.message || res.error || 'Failed to save.' });
        }
    };

    return (
        <section className="card p-5 space-y-4">
            <div className="flex items-center gap-2">
                <ShieldCheck size={16} className="text-(--accent-light)" />
                <h3 className="font-medium text-sm text-(--base-09)">Public Demo Account</h3>
            </div>
            <p className="text-xs text-(--base-06)">
                A single read-only account that sees every server flagged as a demo. It is forced
                GET-only (cannot edit, power, create or download anything). Reachable with its own
                password, or one-click via the public demo session. Leave empty to disable.
            </p>
            {loading ? (
                <SkeletonFormRow />
            ) : (
                <div className="flex gap-2 items-end">
                    <div className="flex flex-col gap-[5px] flex-1">
                        <label className="input-label">Demo account username</label>
                        <input
                            type="text"
                            value={username}
                            onChange={e => setUsername(e.target.value)}
                            placeholder="demo"
                            className="input-field w-full"
                            spellCheck={false}
                        />
                    </div>
                    <button onClick={save} disabled={saving} className="btn btn-primary btn-sm shrink-0">
                        {saving ? <Loader2 size={14} className="animate-spin" /> : 'Save'}
                    </button>
                </div>
            )}
            {msg && (
                <p className={`text-xs flex items-center gap-1.5 ${msg.ok ? 'text-(--success-light)' : 'text-(--error-light)'}`}>
                    {msg.ok ? <CircleCheck size={13} /> : <CircleAlert size={13} />}
                    {msg.text}
                </p>
            )}
        </section>
    );
}

// ── Auth Policy section (registration + 2FA enforcement) ──────────────
// Renders one card so admin saves both groups in a single round-trip.

function AuthPolicySection() {
    const [policy, setPolicy] = useState<AuthPolicy | null>(null);
    const [loading, setLoading] = useState(true);
    const [saving, setSaving] = useState(false);
    const [toast, setToast] = useState<{ msg: string; ok: boolean } | null>(null);

    useEffect(() => {
        getAuthPolicy().then(res => {
            if (res.success) setPolicy(res.policy);
            setLoading(false);
        });
    }, []);

    const flash = (msg: string, ok = true) => {
        setToast({ msg, ok });
        setTimeout(() => setToast(null), 2800);
    };

    const handleSave = async () => {
        if (!policy) return;
        setSaving(true);
        const res = await saveAuthPolicy(policy);
        setSaving(false);
        if (res.success) {
            if (res.policy) setPolicy(res.policy);
            flash('Saved.');
        } else {
            flash(res.message || 'Save failed.', false);
        }
    };

    if (loading || !policy) {
        return (
            <section className="card p-5 border border-(--base-03) space-y-4">
                <h3 className="mono-label mb-3 flex items-center gap-2"><Mail size={14} /> Authentication policy</h3>
                <div className="space-y-3">
                    <SkeletonText width="w-1/3" className="h-3.5" />
                    <Skeleton className="h-10 w-full" />
                    <Skeleton className="h-10 w-full" />
                    <Skeleton className="h-10 w-full" />
                    <SkeletonFormRow />
                </div>
                <div className="pt-4 border-t border-(--base-03) space-y-3">
                    <SkeletonText width="w-1/3" className="h-3.5" />
                    <Skeleton className="h-10 w-full" />
                    <Skeleton className="h-10 w-full" />
                </div>
            </section>
        );
    }

    return (
        <section className="card p-5 border border-(--base-03) space-y-6 relative">
            <header className="flex items-center justify-between gap-3">
                <h3 className="mono-label flex items-center gap-2"><Mail size={14} /> Authentication policy</h3>
                {/* Always in force - these switches have no off state of their own. */}
                <StatusBadge active />
            </header>

            <div className="space-y-4">
                <h4 className="mono-label text-(--base-07)">Registration</h4>
                <ToggleRow
                    label="Allow self-registration"
                    description="When enabled, anyone with access to the panel can create an account at /register."
                    value={policy.registrationEnabled}
                    onChange={v => setPolicy({ ...policy, registrationEnabled: v })}
                />
                <ToggleRow
                    label="Require email verification"
                    description="New users must click a confirmation link before they can sign in. Requires SMTP to be configured below."
                    value={policy.emailVerifyRequired}
                    onChange={v => setPolicy({ ...policy, emailVerifyRequired: v })}
                />
                <ToggleRow
                    label="New users get all-regions access by default"
                    description="Off (default): self-registered users see no servers until an admin grants region access. Admin-created users still default to all-regions."
                    value={policy.defaultNewUserAllRegions}
                    onChange={v => setPolicy({ ...policy, defaultNewUserAllRegions: v })}
                />
                <div className="flex items-center gap-3">
                    <label className="input-label mb-0 shrink-0">Password min length</label>
                    <input
                        type="number"
                        min={4}
                        max={128}
                        value={policy.passwordMinLength}
                        onChange={e => setPolicy({ ...policy, passwordMinLength: Number(e.target.value) || 0 })}
                        className="input-field w-24"
                    />
                    <p className="text-xs text-(--base-06) leading-tight">Enforced on registration and password reset (4–128).</p>
                </div>
            </div>

            <div className="space-y-4 pt-4 border-t border-(--base-03)">
                <h4 className="mono-label text-(--base-07) flex items-center gap-2">
                    <ShieldCheck size={12} /> Two-factor enforcement
                </h4>
                <ToggleRow
                    label="Require 2FA for administrators"
                    description="Admin accounts that haven't enrolled get a short-lived enrollment token at login instead of a full session. They must complete 2FA setup before accessing the panel."
                    value={policy.require2FAForAdmins}
                    onChange={v => setPolicy({ ...policy, require2FAForAdmins: v })}
                />
                <ToggleRow
                    label="Require 2FA for all users"
                    description="Same enforcement as above, applied to every account on the platform — including regular users. Stronger guarantee, more friction at first login. Implies the admin toggle."
                    value={policy.require2FAForAllUsers}
                    onChange={v => setPolicy({ ...policy, require2FAForAllUsers: v })}
                />
                <p className="text-xs text-(--base-06)">
                    Existing sessions remain valid — enforcement triggers at the next login. Backup codes are generated automatically as part of enrollment.
                </p>
            </div>

            <div className="space-y-4 pt-4 border-t border-(--base-03)">
                <h4 className="mono-label text-(--base-07) flex items-center gap-2">
                    <HelpCircle size={12} /> Security questions
                </h4>
                <ToggleRow
                    label="Enable security questions"
                    description="Master toggle. When off, the picker is hidden everywhere — registration, profile, and reset all skip it."
                    value={policy.securityQuestionsEnabled}
                    onChange={v => setPolicy({ ...policy, securityQuestionsEnabled: v })}
                />
                <ToggleRow
                    label="Require at signup"
                    description="New users must pick + answer questions during registration. Existing users keep working without them."
                    value={policy.securityQuestionsRequiredAtSignup}
                    onChange={v => setPolicy({ ...policy, securityQuestionsRequiredAtSignup: v })}
                />
                <ToggleRow
                    label="Require during password reset"
                    description="Reset flow asks for the answers before consuming the email link. Users without questions stored skip this step — otherwise the policy would lock them out."
                    value={policy.securityQuestionsRequiredAtReset}
                    onChange={v => setPolicy({ ...policy, securityQuestionsRequiredAtReset: v })}
                />
                <div className="flex items-center gap-3">
                    <label className="input-label mb-0 shrink-0">Required answers</label>
                    <input
                        type="number"
                        min={1}
                        max={10}
                        value={policy.securityQuestionsCount}
                        onChange={e => setPolicy({ ...policy, securityQuestionsCount: Number(e.target.value) || 0 })}
                        className="input-field w-24"
                    />
                    <p className="text-xs text-(--base-06) leading-tight">How many questions a user must pick + answer (1–10).</p>
                </div>
            </div>

            <div className="space-y-4 pt-4 border-t border-(--base-03)">
                <h4 className="mono-label text-(--base-07) flex items-center gap-2">
                    <KeyRound size={12} /> Password reset
                </h4>
                <div className="flex items-center gap-3">
                    <label className="input-label mb-0 shrink-0">Reset link lifetime</label>
                    <input
                        type="number"
                        min={5}
                        max={1440}
                        value={policy.passwordResetLinkTTLMinutes}
                        onChange={e => setPolicy({ ...policy, passwordResetLinkTTLMinutes: Number(e.target.value) || 0 })}
                        className="input-field w-24"
                    />
                    <span className="text-sm text-(--base-07)">minutes</span>
                    <p className="text-xs text-(--base-06) leading-tight">Range 5–1440. Shorter is safer if links leak; longer is friendlier for users with slow inbox delivery.</p>
                </div>
                <p className="text-xs text-(--base-06)">
                    The reset link itself goes out via the SMTP profile configured below — the same one used for email verification.
                </p>
            </div>

            <div className="flex justify-end pt-2">
                <button
                    type="button"
                    onClick={handleSave}
                    disabled={saving}
                    className="btn btn-primary inline-flex items-center gap-2 disabled:opacity-40"
                >
                    {saving && <Loader2 size={14} className="animate-spin" />}
                    Save authentication policy
                </button>
            </div>

            {toast && (
                <p className={`text-xs ${toast.ok ? 'text-(--success-light)' : 'text-(--error-light)'} flex items-center gap-1`}>
                    {toast.ok ? <CircleCheck size={12} /> : <CircleAlert size={12} />}
                    {toast.msg}
                </p>
            )}
        </section>
    );
}

// ── SMTP section ──────────────────────────────────────────────────────

function SMTPSection() {
    const [cfg, setCfg] = useState<SMTPConfig | null>(null);
    const [loading, setLoading] = useState(true);
    const [saving, setSaving] = useState(false);
    const [testing, setTesting] = useState(false);
    const [testTo, setTestTo] = useState('');
    const [toast, setToast] = useState<{ msg: string; ok: boolean } | null>(null);
    // What the mailer would actually use right now, so the header badge reports
    // the stored profile rather than the form being typed into. mailer.LoadConfig
    // has no env fallback and refuses on an empty host or from-address, so those
    // two are exactly the line between "sends mail" and "every mail fails".
    const [sending, setSending] = useState(false);
    const smtpUsable = (c: SMTPConfig | null) => !!c && !!c.host.trim() && !!c.fromEmail.trim();

    useEffect(() => {
        getSMTPConfig().then(res => {
            if (res.success) {
                setCfg(res.config);
                setSending(smtpUsable(res.config));
            }
            setLoading(false);
        });
    }, []);

    const flash = (msg: string, ok = true) => {
        setToast({ msg, ok });
        setTimeout(() => setToast(null), 4000);
    };

    const handleSave = async () => {
        if (!cfg) return;
        setSaving(true);
        const res = await saveSMTPConfig(cfg);
        setSaving(false);
        if (res.success) {
            if (res.config) setCfg(res.config);
            setSending(smtpUsable(res.config ?? cfg));
            flash('Saved.');
        } else {
            flash(res.message || 'Save failed.', false);
        }
    };

    const handleTest = async () => {
        setTesting(true);
        const res = await testSendSMTP(testTo.trim() || undefined);
        setTesting(false);
        flash(res.message || (res.success ? 'Test sent.' : 'Test failed.'), !!res.success);
    };

    if (loading || !cfg) {
        return (
            <section className="card p-5 border border-(--base-03) space-y-4">
                <h3 className="mono-label mb-3 flex items-center gap-2"><Send size={14} /> SMTP — Outgoing Email</h3>
                <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
                    <SkeletonFormRow />
                    <SkeletonFormRow />
                    <SkeletonFormRow />
                    <SkeletonFormRow />
                    <SkeletonFormRow />
                    <SkeletonFormRow />
                </div>
                <SkeletonFormRow />
            </section>
        );
    }

    return (
        <section className="card p-5 border border-(--base-03) space-y-4 relative">
            <header className="flex items-center justify-between gap-3">
                <h3 className="mono-label flex items-center gap-2"><Send size={14} /> SMTP — Outgoing Email</h3>
                <StatusBadge active={sending} inactiveLabel="Not configured" />
            </header>
            <p className="text-xs text-(--base-06)">
                The default SMTP profile is used for verification, password reset and ticket notifications. Per-purpose
                overrides ship in later sub-phases.
            </p>

            <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
                <Field label="Host" value={cfg.host} onChange={v => setCfg({ ...cfg, host: v })} placeholder="smtp.example.com" />
                <Field label="Port" type="number" value={String(cfg.port || '')} onChange={v => setCfg({ ...cfg, port: Number(v) || 0 })} placeholder="587 / 465 / 25" />
                <Field label="Username" value={cfg.username} onChange={v => setCfg({ ...cfg, username: v })} autoComplete="off" />
                <PasswordField
                    label="Password"
                    value={cfg.password || ''}
                    placeholder={cfg.passwordSet ? '(stored — leave blank to keep)' : ''}
                    onChange={v => setCfg({ ...cfg, password: v })}
                />
                <Field label="From email" value={cfg.fromEmail} onChange={v => setCfg({ ...cfg, fromEmail: v })} placeholder="noreply@example.com" />
                <Field label="From name" value={cfg.fromName} onChange={v => setCfg({ ...cfg, fromName: v })} placeholder="Dylaris" />
                <div className="flex flex-col gap-[5px] sm:col-span-2">
                    <label className="input-label">Encryption</label>
                    <select
                        value={cfg.encryption || 'starttls'}
                        onChange={e => setCfg({ ...cfg, encryption: e.target.value })}
                        className="input-field w-full"
                    >
                        {ENCRYPTION_OPTIONS.map(o => <option key={o.value} value={o.value}>{o.label}</option>)}
                    </select>
                </div>
            </div>

            <div className="flex flex-col sm:flex-row sm:items-center gap-3 pt-2 border-t border-(--base-03)">
                <input
                    type="email"
                    placeholder="Send test to (defaults to your own email)"
                    value={testTo}
                    onChange={e => setTestTo(e.target.value)}
                    className="input-field flex-1"
                />
                <button
                    type="button"
                    onClick={handleTest}
                    disabled={testing}
                    className="btn btn-secondary inline-flex items-center gap-2 disabled:opacity-40"
                >
                    {testing && <Loader2 size={14} className="animate-spin" />}
                    Send test email
                </button>
                <button
                    type="button"
                    onClick={handleSave}
                    disabled={saving}
                    className="btn btn-primary inline-flex items-center gap-2 disabled:opacity-40"
                >
                    {saving && <Loader2 size={14} className="animate-spin" />}
                    Save SMTP
                </button>
            </div>

            {toast && (
                <p className={`text-xs ${toast.ok ? 'text-(--success-light)' : 'text-(--error-light)'} flex items-center gap-1`}>
                    {toast.ok ? <CircleCheck size={12} /> : <CircleAlert size={12} />}
                    {toast.msg}
                </p>
            )}
        </section>
    );
}

// ── Small UI helpers ──────────────────────────────────────────────────

function ToggleRow({ label, description, value, onChange }: { label: string; description: string; value: boolean; onChange: (v: boolean) => void }) {
    return (
        <label className="flex items-start justify-between gap-4 cursor-pointer">
            <div>
                <p className="text-sm font-medium">{label}</p>
                <p className="text-xs text-(--base-06) leading-snug mt-0.5">{description}</p>
            </div>
            <input
                type="checkbox"
                checked={value}
                onChange={e => onChange(e.target.checked)}
                className="w-4 h-4 accent-(--accent) shrink-0 mt-1"
            />
        </label>
    );
}

// Every card header used to carry the literal string "Active" in a green pill.
// On the cards that are always in force that is true; on three of them it was
// not. It sat green next to an auto-delete job whose master toggle was off (the
// card's own copy says "when off, the daily job is a no-op"), next to an SMTP
// profile with no host, where every verification mail fails, and next to a
// retention horizon that no sweep would ever apply.
//
// `active` must be the state IN FORCE, not the half-edited form: a badge that
// turns green while you type has only moved the lie earlier.
function StatusBadge({ active, activeLabel = 'Active', inactiveLabel = 'Off' }: { active: boolean; activeLabel?: string; inactiveLabel?: string }) {
    return (
        <span
            className={`text-[9px] font-mono uppercase tracking-[0.08em] px-1.5 py-0.5 rounded-sm ${
                active ? 'text-(--success-light) bg-(--success-ghost)' : 'text-(--base-06) bg-(--base-03)'
            }`}
        >
            {active ? activeLabel : inactiveLabel}
        </span>
    );
}

// ── Auto-delete inactive users section ────────────────────────────────
// Mirrors the auth-policy save pattern: one card, loads/saves the whole
// AuthPolicy DTO. Lives outside AuthPolicySection because it has a
// distinct concern (lifecycle vs gate) and would crowd that card otherwise.

function AutoDeleteSection() {
    const [policy, setPolicy] = useState<AuthPolicy | null>(null);
    const [loading, setLoading] = useState(true);
    const [saving, setSaving] = useState(false);
    const [toast, setToast] = useState<{ msg: string; ok: boolean } | null>(null);
    // The stored master toggle, not the checkbox: flipping it off and walking
    // away without saving leaves the daily job running.
    const [running, setRunning] = useState(false);

    useEffect(() => {
        getAuthPolicy().then(res => {
            if (res.success) {
                setPolicy(res.policy);
                setRunning(!!res.policy?.inactiveDeleteEnabled);
            }
            setLoading(false);
        });
    }, []);

    const flash = (msg: string, ok = true) => {
        setToast({ msg, ok });
        setTimeout(() => setToast(null), 2800);
    };

    const handleSave = async () => {
        if (!policy) return;
        setSaving(true);
        const res = await saveAuthPolicy(policy);
        setSaving(false);
        if (res.success) {
            if (res.policy) setPolicy(res.policy);
            setRunning(!!(res.policy ?? policy).inactiveDeleteEnabled);
            flash('Saved.');
        } else {
            flash(res.message || 'Save failed.', false);
        }
    };

    if (loading || !policy) {
        return (
            <section className="card p-5 border border-(--base-03) space-y-4">
                <h3 className="mono-label mb-3 flex items-center gap-2"><Trash2 size={14} /> Auto-delete inactive users</h3>
                <SkeletonText width="w-3/4" />
                <Skeleton className="h-10 w-full" />
                <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
                    <SkeletonFormRow />
                    <SkeletonFormRow />
                    <SkeletonFormRow />
                    <SkeletonFormRow />
                </div>
            </section>
        );
    }

    return (
        <section className="card p-5 border border-(--base-03) space-y-4">
            <header className="flex items-center justify-between gap-3">
                <h3 className="mono-label flex items-center gap-2"><Trash2 size={14} /> Auto-delete inactive users</h3>
                <StatusBadge active={running} />
            </header>
            <p className="text-xs text-(--base-06)">
                Daily job that warns dormant accounts by email and then removes them. Admins are never affected. Users with server history get an additional grace window. Signing in at any point cancels the scheduled deletion automatically.
            </p>

            <ToggleRow
                label="Enable auto-delete"
                description="Master toggle. When off, the daily job is a no-op."
                value={policy.inactiveDeleteEnabled}
                onChange={v => setPolicy({ ...policy, inactiveDeleteEnabled: v })}
            />

            <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
                <div className="flex flex-col gap-[5px]">
                    <label className="input-label">Inactive days before action</label>
                    <input
                        type="number"
                        min={7}
                        max={3650}
                        value={policy.inactiveDaysBeforeDelete}
                        onChange={e => setPolicy({ ...policy, inactiveDaysBeforeDelete: Number(e.target.value) || 0 })}
                        className="input-field w-full"
                    />
                    <p className="text-xs text-(--base-06)">Days since last login (or account creation when never logged in). 7–3650.</p>
                </div>
                <div className="flex flex-col gap-[5px]">
                    <label className="input-label">History grace extra days</label>
                    <input
                        type="number"
                        min={0}
                        max={3650}
                        value={policy.historyGraceExtraDays}
                        onChange={e => setPolicy({ ...policy, historyGraceExtraDays: Number(e.target.value) || 0 })}
                        className="input-field w-full"
                    />
                    <p className="text-xs text-(--base-06)">Additional days for accounts with server history (owns or invited).</p>
                </div>
                <div className="flex flex-col gap-[5px]">
                    <label className="input-label">Warning lead time (days)</label>
                    <input
                        type="number"
                        min={0}
                        max={30}
                        value={policy.deleteEmailWarningDays}
                        onChange={e => setPolicy({ ...policy, deleteEmailWarningDays: Number(e.target.value) || 0 })}
                        className="input-field w-full"
                    />
                    <p className="text-xs text-(--base-06)">Days between warning email and execution. 0 = no warning, immediate.</p>
                </div>
                <div className="flex flex-col gap-[5px]">
                    <label className="input-label">Deletion mode</label>
                    <select
                        value={policy.deletionMode}
                        onChange={e => setPolicy({ ...policy, deletionMode: e.target.value as 'anonymize' | 'hard_delete' })}
                        className="input-field w-full"
                    >
                        <option value="anonymize">Anonymize (DSGVO-friendly, keeps row + id)</option>
                        <option value="hard_delete">Hard delete (permanent, cascades)</option>
                    </select>
                    <p className="text-xs text-(--base-06)">Anonymize wipes PII but preserves audit references. Hard delete removes the row.</p>
                </div>
            </div>

            <div className="flex justify-end pt-1">
                <button
                    type="button"
                    onClick={handleSave}
                    disabled={saving}
                    className="btn btn-primary inline-flex items-center gap-2 disabled:opacity-40"
                >
                    {saving && <Loader2 size={14} className="animate-spin" />}
                    Save auto-delete settings
                </button>
            </div>

            {toast && (
                <p className={`text-xs ${toast.ok ? 'text-(--success-light)' : 'text-(--error-light)'} flex items-center gap-1`}>
                    {toast.ok ? <CircleCheck size={12} /> : <CircleAlert size={12} />}
                    {toast.msg}
                </p>
            )}
        </section>
    );
}

// ── Audit retention section ───────────────────────────────────────────
// Platform-wide server-audit retention. Per-server audit toggles live on
// each server's Audit tab so admins can flip them in context.

function AuditPolicySection() {
    const [policy, setPolicy] = useState<AuditPolicy | null>(null);
    const [loading, setLoading] = useState(true);
    const [saving, setSaving] = useState(false);
    const [toast, setToast] = useState<{ msg: string; ok: boolean } | null>(null);
    // The horizon the sweep is actually applying. 0 means it deletes nothing,
    // which is also what an install that never saved this card gets.
    const [inForceDays, setInForceDays] = useState(0);

    useEffect(() => {
        getAuditPolicy().then(res => {
            if (res.success) {
                setPolicy(res.policy);
                setInForceDays(res.policy?.serverRetentionDays ?? 0);
            }
            setLoading(false);
        });
    }, []);

    const flash = (msg: string, ok = true) => {
        setToast({ msg, ok });
        setTimeout(() => setToast(null), 2800);
    };

    const handleSave = async () => {
        if (!policy) return;
        setSaving(true);
        const res = await saveAuditPolicy(policy);
        setSaving(false);
        if (res.success) {
            if (res.policy) setPolicy(res.policy);
            setInForceDays((res.policy ?? policy).serverRetentionDays);
            flash('Saved.');
        } else {
            flash(res.message || 'Save failed.', false);
        }
    };

    if (loading || !policy) {
        return (
            <section className="card p-5 border border-(--base-03) space-y-4">
                <h3 className="mono-label mb-3 flex items-center gap-2"><FileText size={14} /> Audit retention</h3>
                <SkeletonText width="w-3/4" />
                <div className="flex items-center gap-3">
                    <SkeletonText width="w-32" />
                    <Skeleton className="h-9 w-24" />
                </div>
            </section>
        );
    }

    return (
        <section className="card p-5 border border-(--base-03) space-y-4">
            <header className="flex items-center justify-between gap-3">
                <h3 className="mono-label flex items-center gap-2"><FileText size={14} /> Server audit retention</h3>
                <StatusBadge active={inForceDays > 0} inactiveLabel="Keeping forever" />
            </header>
            <p className="text-xs text-(--base-06)">
                How long server-audit events are kept before the daily sweep deletes them. Set to 0 to keep them forever.
                That is the starting point: the sweep only applies a horizon you saved here, and 365 is a good one.
                Per-server audit is auto-enabled on first member invite; admins can force it on from each server&apos;s Audit tab.
            </p>
            <div className="flex items-center gap-3">
                <label className="input-label mb-0 shrink-0">Server-event retention</label>
                <input
                    type="number"
                    min={0}
                    max={3650}
                    value={policy.serverRetentionDays}
                    onChange={e => setPolicy({ ...policy, serverRetentionDays: Number(e.target.value) || 0 })}
                    className="input-field w-24"
                />
                <span className="text-sm text-(--base-07)">days</span>
                <p className="text-xs text-(--base-06) leading-tight">0 = unlimited. Range 0–3650.</p>
            </div>
            <div className="flex justify-end pt-1">
                <button
                    type="button"
                    onClick={handleSave}
                    disabled={saving}
                    className="btn btn-primary inline-flex items-center gap-2 disabled:opacity-40"
                >
                    {saving && <Loader2 size={14} className="animate-spin" />}
                    Save audit policy
                </button>
            </div>
            {toast && (
                <p className={`text-xs ${toast.ok ? 'text-(--success-light)' : 'text-(--error-light)'} flex items-center gap-1`}>
                    {toast.ok ? <CircleCheck size={12} /> : <CircleAlert size={12} />}
                    {toast.msg}
                </p>
            )}
        </section>
    );
}

// ── Security Questions Pool section ───────────────────────────────────
// Editor for the admin-curated list of available questions. Lives in its
// own card so the AuthPolicySection stays focused on toggles, and so the
// editor's add/remove rows don't clutter the policy form.

function SecurityQuestionsPoolSection() {
    const [pool, setPool] = useState<string[]>([]);
    const [loading, setLoading] = useState(true);
    const [saving, setSaving] = useState(false);
    const [newQuestion, setNewQuestion] = useState('');
    const [toast, setToast] = useState<{ msg: string; ok: boolean } | null>(null);

    useEffect(() => {
        getAdminSecurityQuestionPool().then(res => {
            if (res.success && Array.isArray(res.pool)) setPool(res.pool);
            setLoading(false);
        });
    }, []);

    const flash = (msg: string, ok = true) => {
        setToast({ msg, ok });
        setTimeout(() => setToast(null), 2800);
    };

    const handleAdd = () => {
        const q = newQuestion.trim();
        if (!q) return;
        if (pool.some(p => p.toLowerCase() === q.toLowerCase())) {
            flash('That question is already in the pool.', false);
            return;
        }
        setPool([...pool, q]);
        setNewQuestion('');
    };

    const handleRemove = (idx: number) => {
        setPool(pool.filter((_, i) => i !== idx));
    };

    const handleSave = async () => {
        if (pool.length < 3) {
            flash('Pool must contain at least 3 questions.', false);
            return;
        }
        setSaving(true);
        const res = await setAdminSecurityQuestionPool(pool);
        setSaving(false);
        if (res.success) {
            if (Array.isArray(res.pool)) setPool(res.pool);
            flash('Pool saved.');
        } else {
            flash(res.message || 'Save failed.', false);
        }
    };

    if (loading) {
        return (
            <section className="card p-5 border border-(--base-03) space-y-4">
                <h3 className="mono-label mb-3 flex items-center gap-2"><HelpCircle size={14} /> Security questions pool</h3>
                <SkeletonText width="w-3/4" />
                <div className="space-y-1.5">
                    {Array.from({ length: 5 }).map((_, i) => (
                        <Skeleton key={i} className="h-9 w-full" />
                    ))}
                </div>
                <div className="flex gap-2">
                    <Skeleton className="h-9 flex-1" />
                    <Skeleton className="h-9 w-20" />
                </div>
            </section>
        );
    }

    return (
        <section className="card p-5 border border-(--base-03) space-y-4">
            <header className="flex items-center justify-between gap-3">
                <h3 className="mono-label flex items-center gap-2"><HelpCircle size={14} /> Security questions pool</h3>
                {/* Always in force - the pool is whatever it contains. */}
                <StatusBadge active />
            </header>
            <p className="text-xs text-(--base-06)">
                The list of questions users can choose from. Removing a question doesn&apos;t affect users who already picked it — their chosen wording is stored inline.
            </p>

            <div className="space-y-1.5 max-h-72 overflow-y-auto pr-1">
                {pool.length === 0 && (
                    <p className="text-sm text-(--base-06) italic text-center py-4">Pool is empty. Add some questions below.</p>
                )}
                {pool.map((q, idx) => (
                    <div key={idx} className="flex items-center gap-2 p-2 rounded-md bg-(--base-02) border border-(--base-03)">
                        <span className="font-mono text-xs text-(--base-06) w-6 text-center">{idx + 1}</span>
                        <span className="flex-1 text-sm text-(--base-09)">{q}</span>
                        <button
                            type="button"
                            onClick={() => handleRemove(idx)}
                            disabled={saving}
                            title="Remove"
                            aria-label={`Remove question ${idx + 1}`}
                            className="p-1.5 rounded text-(--error-light) hover:bg-(--error-ghost) disabled:opacity-40"
                        >
                            <X size={13} />
                        </button>
                    </div>
                ))}
            </div>

            <div className="flex gap-2 items-stretch">
                <input
                    type="text"
                    value={newQuestion}
                    onChange={e => setNewQuestion(e.target.value)}
                    onKeyDown={e => { if (e.key === 'Enter') { e.preventDefault(); handleAdd(); } }}
                    placeholder="Add a new question…"
                    maxLength={200}
                    className="input-field flex-1"
                />
                <button
                    type="button"
                    onClick={handleAdd}
                    disabled={!newQuestion.trim() || saving}
                    className="btn btn-secondary inline-flex items-center gap-1.5 disabled:opacity-40"
                >
                    <Plus size={14} /> Add
                </button>
            </div>

            <div className="flex justify-end pt-1">
                <button
                    type="button"
                    onClick={handleSave}
                    disabled={saving}
                    className="btn btn-primary inline-flex items-center gap-2 disabled:opacity-40"
                >
                    {saving && <Loader2 size={14} className="animate-spin" />}
                    Save pool
                </button>
            </div>

            {toast && (
                <p className={`text-xs ${toast.ok ? 'text-(--success-light)' : 'text-(--error-light)'} flex items-center gap-1`}>
                    {toast.ok ? <CircleCheck size={12} /> : <CircleAlert size={12} />}
                    {toast.msg}
                </p>
            )}
        </section>
    );
}

function Field({ label, value, onChange, type, placeholder, autoComplete }: { label: string; value: string; onChange: (v: string) => void; type?: string; placeholder?: string; autoComplete?: string }) {
    return (
        <div className="flex flex-col gap-[5px]">
            <label className="input-label">{label}</label>
            <input
                type={type || 'text'}
                value={value}
                onChange={e => onChange(e.target.value)}
                placeholder={placeholder}
                autoComplete={autoComplete}
                className="input-field w-full"
            />
        </div>
    );
}

function PasswordField({ label, value, onChange, placeholder }: { label: string; value: string; onChange: (v: string) => void; placeholder?: string }) {
    return (
        <div className="flex flex-col gap-[5px]">
            <label className="input-label">{label}</label>
            <input
                type="password"
                value={value}
                onChange={e => onChange(e.target.value)}
                placeholder={placeholder}
                autoComplete="new-password"
                className="input-field w-full"
            />
        </div>
    );
}
