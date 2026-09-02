"use client";

import { useState } from 'react';
import { Database, CheckCircle2, AlertTriangle, XCircle, Loader2 } from 'lucide-react';
import { useSettingsForm } from '@/lib/useSettingsForm';
import {
    getMetricsDB, saveMetricsDB, testMetricsDB,
    metricsDBIncomplete, metricsDBModeSummary,
    emptyMetricsDBTarget,
    type MetricsDBSettings, type MetricsDBRequest, type MetricsDBMode, type MetricsDBTestResult,
} from '@/lib/api/metricsDb';
import SettingsCard from '@/components/settings/SettingsCard';
import Segmented from '@/components/ui/Segmented';
import { SwitchRow } from '@/components/ui/Switch';
import Select from '@/components/ui/Select';
import HelpTip from '@/components/ui/HelpTip';
import Checkbox from '@/components/ui/Checkbox';

/**
 * Long-term statistics: whether to record, and where.
 *
 * One card and ONE save, because those are one decision. Recording begins the
 * instant the switch goes on, the first bucket lands at whatever resolution the
 * stored target implies, and nothing can be backfilled or converted afterwards -
 * so a switch that could be flipped on another screen was a way to spend the
 * only chance to choose without noticing.
 *
 * It is also why the card is not part of the feature-switch bundle above: that
 * card saves seven booleans through one endpoint, this one has a target to
 * validate and a database to reach first. Two save models inside one card is
 * the exact confusion this page was untangled to remove; two cards for one
 * decision was the same mistake wearing the other hat.
 */
export default function MetricsDatabaseCard() {
    const [test, setTest] = useState<MetricsDBTestResult | null>(null);
    const [testing, setTesting] = useState(false);

    const form = useSettingsForm<MetricsDBSettings>({
        load: async () => {
            const res = await getMetricsDB();
            if (!res.success || !res.settings) return null;
            // The server reports whether one is STORED; the checkbox is the
            // inverse of that. Derived on load and again after each save, so
            // the box always describes what is actually saved rather than what
            // was last typed.
            return { ...res.settings, noPassword: !res.settings.passwordSet };
        },
        save: async value => {
            const res = await saveMetricsDB(value);
            if (!res.success) return { ok: false, message: res.message };
            // The server's own copy wins: it normalises the port and ssl mode,
            // and it blanks the password it just stored.
            const stored = res.settings ?? value;
            if (res.warning) setTest({ ok: true, severity: 'warning', message: res.warning });
            return { ok: true, value: { ...stored, password: '', noPassword: !stored.passwordSet } };
        },
        successMessage: 'Statistics database saved.',
    });

    const value = form.value;
    const target: MetricsDBRequest = value ?? emptyMetricsDBTarget;
    const enabled = !!value?.enabled;
    const incomplete = metricsDBIncomplete(target);

    // A field edit invalidates the last test result. Leaving a green banner
    // above a host that has since been retyped is a claim about a connection
    // nobody made.
    const set = (partial: Partial<MetricsDBRequest>) => {
        setTest(null);
        form.patch(partial);
    };

    const runTest = async () => {
        setTesting(true);
        setTest(null);
        try {
            const res = await testMetricsDB(target);
            if (!res.success) {
                setTest({ ok: false, severity: 'error', message: res.message || 'The test could not be run.' });
                return;
            }
            setTest({
                ok: !!res.ok,
                severity: res.severity ?? (res.ok ? 'ok' : 'error'),
                message: res.message ?? '',
                timescale: res.timescale,
                version: res.version,
            });
        } finally {
            setTesting(false);
        }
    };

    return (
        <SettingsCard
            title="Long-term statistics"
            icon={Database}
            description="Whether this platform keeps a record of what it handled, and where that record is written."
            help={
                <>
                    <p className="mb-2">
                        The <strong>Core database</strong> keeps hour buckets beside everything else
                        this platform stores. It needs no second service and no extension, and costs
                        a few hundred megabytes a year.
                    </p>
                    <p className="mb-2">
                        A <strong>separate database</strong> keeps minute buckets. That is roughly a
                        hundred million rows a year at a modest fleet size, which is a query problem
                        before it is a storage one - so it wants TimescaleDB, which chunks the table
                        and compresses anything older than a week.
                    </p>
                    <p>
                        There is no conversion between the two and no backfill: whatever is recorded
                        stays at the resolution it was recorded at. Choosing before you switch
                        recording on is the whole point of this card.
                    </p>
                </>
            }
            form={form}
            saveBlockedReason={incomplete ?? undefined}
            loadFailedMessage="The statistics database settings could not be loaded, so they are shown read-only. Saving now would write these defaults over the real configuration."
            actions={
                <button
                    type="button"
                    className="btn btn-secondary btn-sm"
                    onClick={runTest}
                    disabled={testing || form.loading || form.loadFailed || !!incomplete}
                    title={incomplete ?? 'Open a connection and report what it is'}
                >
                    {testing ? <Loader2 size={14} className="animate-spin" /> : null}
                    {testing ? 'Testing…' : 'Test connection'}
                </button>
            }
        >

            <SwitchRow
                label="Record long-term statistics"
                description="Keeps what this platform handles - players, traffic, CPU and RAM, uptime per component - in buckets that survive, so months of operation can be shown later. Off by default. Everything stays in this installation; nothing is sent anywhere."
                checked={enabled}
                disabled={form.loading || form.loadFailed}
                onChange={v => set({ enabled: v })}
            />

            {!enabled && (
                <p className="flex items-start gap-1.5 text-xs text-(--base-06) leading-relaxed">
                    <AlertTriangle size={12} className="mt-0.5 shrink-0 text-(--warning-light)" />
                    <span>
                        Nothing is being recorded. History starts when you switch this on and there
                        is no way to fill in what came before, so choose the database below in the
                        same save rather than turning it on first.
                    </span>
                </p>
            )}

            <div className="flex flex-col gap-2">
                <label className="input-label">Record into</label>
                <Segmented<MetricsDBMode>
                    ariaLabel="Statistics database"
                    value={target.mode}
                    onChange={mode => set({ mode })}
                    options={[
                        { id: 'core', label: 'Core database', hint: 'Hour buckets, no second service' },
                        { id: 'separate', label: 'Separate database', hint: 'Minute buckets, needs TimescaleDB' },
                    ]}
                />
                <p className="text-xs text-(--base-06) leading-relaxed max-w-2xl">
                    {metricsDBModeSummary(target.mode, !!value?.coreTimescale)}
                </p>
            </div>

            {target.mode === 'separate' && (
                <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
                    <Field label="Host" value={target.host} onChange={v => set({ host: v })}
                        placeholder="metricsdb" />
                    <Field label="Port" value={target.port} onChange={v => set({ port: v })}
                        placeholder="5432" />
                    <Field label="Database name" value={target.dbName} onChange={v => set({ dbName: v })}
                        placeholder="dylaris_metrics" />
                    <Field label="User" value={target.user} onChange={v => set({ user: v })}
                        placeholder="metrics" />
                    <div>
                        <label className="input-label flex items-center gap-1.5">
                            Password
                            <HelpTip label="About the password">
                                <p className="mb-2">
                                    A metrics database reachable only from Core - on its own Docker
                                    network, say - can legitimately run without one. Tick
                                    <strong> This database has no password</strong> to say so; that
                                    is also how a password already saved is REMOVED.
                                </p>
                                <p>
                                    With the box unticked, leaving the field blank keeps the stored
                                    password - but only while the host, port, database and user are
                                    unchanged. Change any of those and blank means blank, so the old
                                    credential is never sent to a different machine.
                                </p>
                            </HelpTip>
                        </label>
                        {/* Above the field, because it decides whether the field
                            means anything. Blank alone cannot say "there is
                            none": it already means "keep what is stored", which
                            left a saved password with no way back off. */}
                        <div className="mt-1.5 mb-1.5">
                            <Checkbox
                                checked={!!target.noPassword}
                                onChange={v => set({ noPassword: v, ...(v ? { password: '' } : {}) })}
                                label="This database has no password"
                                hint={value?.passwordSet
                                    ? 'One is stored. Ticking this removes it on save.'
                                    : 'None is stored.'}
                            />
                        </div>
                        <input
                            className="input-field input-mono w-full"
                            type="password"
                            value={target.password ?? ''}
                            disabled={!!target.noPassword}
                            placeholder={target.noPassword
                                ? 'No password'
                                : value?.passwordSet ? 'Stored - leave blank to keep' : 'Enter a password'}
                            onChange={e => set({ password: e.target.value })}
                            autoComplete="new-password"
                        />
                    </div>
                    <div>
                        <label className="input-label">SSL mode</label>
                        <div className="mt-1">
                            <Select
                                ariaLabel="SSL mode"
                                value={target.sslMode}
                                onChange={v => set({ sslMode: v })}
                                    options={[
                                    { value: 'disable', label: 'disable' },
                                    { value: 'require', label: 'require' },
                                    { value: 'verify-ca', label: 'verify-ca' },
                                    { value: 'verify-full', label: 'verify-full' },
                                ]}
                            />
                        </div>
                    </div>
                </div>
            )}

            {test && <TestResult result={test} />}

            {value?.active && <ActiveLine active={value.active} />}
        </SettingsCard>
    );
}

/**
 * The last test result.
 *
 * Three severities, not two. "Connected, but there is no TimescaleDB here" is
 * neither a failure nor a clean pass: it works and it will hurt later, which is
 * precisely the state a green tick would hide.
 */
function TestResult({ result }: { result: MetricsDBTestResult }) {
    const tone = {
        ok: { Icon: CheckCircle2, cls: 'border-(--success-border) bg-(--success-ghost) text-(--success-light)' },
        warning: { Icon: AlertTriangle, cls: 'border-(--warning-border) bg-(--warning-ghost) text-(--warning-light)' },
        error: { Icon: XCircle, cls: 'border-(--error-border) bg-(--error-ghost) text-(--error-light)' },
    }[result.severity];
    const { Icon, cls } = tone;
    return (
        <div role="status" className={`flex items-start gap-2 rounded-md border p-3 ${cls}`}>
            <Icon size={14} className="mt-0.5 shrink-0" />
            <p className="text-xs leading-relaxed">{result.message}</p>
        </div>
    );
}

/**
 * What is being written right now.
 *
 * Separate from the form on purpose: a target that could not be opened leaves
 * the previous one recording, so "what is configured" and "what is running" can
 * legitimately differ, and only one of them is on this screen already.
 */
function ActiveLine({ active }: { active: { recording: boolean; separate: boolean; resolution?: string } }) {
    if (!active.recording) {
        return (
            <p className="text-[11px] font-mono text-(--base-06)">
                Not recording: no metrics database is open.
            </p>
        );
    }
    return (
        <p className="text-[11px] font-mono text-(--base-06)">
            Recording now into the {active.separate ? 'separate' : 'Core'} database,
            {' '}{active.resolution} buckets.
        </p>
    );
}

function Field({ label, value, onChange, disabled, type = 'text', placeholder }: {
    label: string; value: string; onChange: (v: string) => void;
    disabled?: boolean; type?: string; placeholder?: string;
}) {
    return (
        <div>
            <label className="input-label">{label}</label>
            <input
                className="input-field input-mono w-full mt-1"
                type={type}
                value={value}
                disabled={disabled}
                placeholder={placeholder}
                onChange={e => onChange(e.target.value)}
                autoComplete="off"
            />
        </div>
    );
}
