"use client";

import { useState } from 'react';
import { Database, CheckCircle2, AlertTriangle, XCircle, Loader2, Lock } from 'lucide-react';
import { useSettingsForm } from '@/lib/useSettingsForm';
import {
    getMetricsDB, saveMetricsDB, testMetricsDB,
    metricsDBIncomplete, metricsDBModeSummary,
    emptyMetricsDBTarget,
    type MetricsDBSettings, type MetricsDBTarget, type MetricsDBMode, type MetricsDBTestResult,
} from '@/lib/api/metricsDb';
import SettingsCard from '@/components/settings/SettingsCard';
import Segmented from '@/components/ui/Segmented';
import Select from '@/components/ui/Select';
import HelpTip from '@/components/ui/HelpTip';

/**
 * Where the long-term statistics are written.
 *
 * Its own card rather than a block inside the feature switches, because it is a
 * different endpoint with its own validation and its own reachability test, and
 * a second save model inside one card is the exact confusion this page was
 * untangled to remove.
 *
 * The choice is one-way in practice: there is no backfill and no conversion
 * between resolutions, so what is picked here is what the history looks like
 * for as long as it is kept. That is why the form states the consequence of
 * each mode next to the mode, rather than only after a test.
 */
export default function MetricsDatabaseCard({ enabled }: { enabled: boolean }) {
    const [test, setTest] = useState<MetricsDBTestResult | null>(null);
    const [testing, setTesting] = useState(false);

    const form = useSettingsForm<MetricsDBSettings>({
        load: async () => {
            const res = await getMetricsDB();
            return res.success && res.settings ? res.settings : null;
        },
        save: async value => {
            const res = await saveMetricsDB(value);
            if (!res.success) return { ok: false, message: res.message };
            // The server's own copy wins: it normalises the port and ssl mode,
            // and it blanks the password it just stored.
            const stored = res.settings ?? value;
            if (res.warning) setTest({ ok: true, severity: 'warning', message: res.warning });
            return { ok: true, value: { ...stored, password: '' } };
        },
        successMessage: 'Statistics database saved.',
    });

    const value = form.value;
    const target: MetricsDBTarget = value ?? emptyMetricsDBTarget;
    const locked = !!value?.managedByEnv;
    const incomplete = metricsDBIncomplete(target);

    // A field edit invalidates the last test result. Leaving a green banner
    // above a host that has since been retyped is a claim about a connection
    // nobody made.
    const set = (partial: Partial<MetricsDBTarget>) => {
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
            title="Statistics database"
            icon={Database}
            description="Where the long-term record is written, and at what resolution."
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
            form={locked ? undefined : form}
            saveBlockedReason={incomplete ?? undefined}
            loadFailedMessage="The statistics database settings could not be loaded, so they are shown read-only. Saving now would write these defaults over the real configuration."
            actions={
                !locked && (
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
                )
            }
        >
            {locked && (
                <div className="flex items-start gap-2 rounded-md border border-(--base-04) bg-(--base-02) p-3">
                    <Lock size={14} className="mt-0.5 shrink-0 text-(--base-06)" />
                    <p className="text-xs text-(--base-07) leading-relaxed">
                        <span className="text-(--base-09) font-medium">Set by this deployment.</span>{' '}
                        <code>METRICS_DB_URL</code> is configured in the environment and takes
                        precedence over anything set here, so this form is read-only. Change it where
                        the stack is defined, or unset it to configure the target from the panel.
                    </p>
                </div>
            )}

            {!enabled && (
                <p className="text-xs text-(--base-06) leading-relaxed">
                    Long-term statistics are switched off, so nothing is being recorded yet. This
                    setting still applies the moment you turn them on - and history starts there,
                    with nothing before it.
                </p>
            )}

            <div className="flex flex-col gap-2">
                <label className="input-label">Record into</label>
                <Segmented<MetricsDBMode>
                    ariaLabel="Statistics database"
                    value={target.mode}
                    onChange={mode => set({ mode })}
                    options={[
                        { id: 'core', label: 'Core database', hint: 'Hour buckets, no second service', disabled: locked },
                        { id: 'separate', label: 'Separate database', hint: 'Minute buckets, needs TimescaleDB', disabled: locked },
                    ]}
                />
                <p className="text-xs text-(--base-06) leading-relaxed max-w-2xl">
                    {metricsDBModeSummary(target.mode, !!value?.coreTimescale)}
                </p>
            </div>

            {target.mode === 'separate' && (
                <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
                    <Field label="Host" value={target.host} onChange={v => set({ host: v })}
                        disabled={locked} placeholder="metricsdb" />
                    <Field label="Port" value={target.port} onChange={v => set({ port: v })}
                        disabled={locked} placeholder="5432" />
                    <Field label="Database name" value={target.dbName} onChange={v => set({ dbName: v })}
                        disabled={locked} placeholder="dylaris_metrics" />
                    <Field label="User" value={target.user} onChange={v => set({ user: v })}
                        disabled={locked} placeholder="metrics" />
                    <div>
                        <label className="input-label flex items-center gap-1.5">
                            Password
                            <HelpTip label="About the password">
                                <p className="mb-2">
                                    Optional. A metrics database reachable only from Core - on its
                                    own Docker network, say - can legitimately run without one, and
                                    an empty field here means exactly that.
                                </p>
                                <p>
                                    Leave it blank to keep the stored password while the host, port,
                                    database and user are unchanged. Change any of those and the
                                    blank field means blank, so the old credential is never sent to
                                    a different machine.
                                </p>
                            </HelpTip>
                        </label>
                        <input
                            className="input-field input-mono w-full mt-1"
                            type="password"
                            value={target.password ?? ''}
                            disabled={locked}
                            placeholder={value?.passwordSet ? 'Stored - leave blank to keep' : 'None'}
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
                                disabled={locked}
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
