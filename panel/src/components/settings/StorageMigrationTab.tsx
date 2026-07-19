"use client";

import React, { useCallback, useEffect, useMemo, useState } from 'react';
import {
    HardDrive, Loader2, CircleCheck, CircleAlert, Play, ShieldAlert,
    Download, FileSearch, X, Trash2, ArrowRight, Info,
} from 'lucide-react';
import {
    getStorageOverview, getStorageMigration, startStorageMigration,
    cancelStorageMigration, downloadManifestCSV, startMigrateFromForm,
} from '@/lib/api/storageMigration';
import {
    storageMigrationInProgress, isCancellablePhase, deleteSourceAllowed,
    canStartMigration, formatPercent, formatBytes, verifyVerdictLabel,
    progressPercent, EMPTY_MIGRATION_FORM, EMPTY_TARGET_CONFIG,
    type MigrationForm, type StorageDataSet, type StorageMigrationJob,
    type StorageMigrationPhase, type StorageVerifyReport, type TargetConfigForm,
    type VerifyMode,
} from '@/lib/storageMigration';
import { systemEvents } from '@/lib/systemEvents';

const PHASE_LABEL: Record<StorageMigrationPhase, string> = {
    preparing: 'Preparing',
    manifesting: 'Inventorying',
    copying: 'Copying',
    verifying: 'Verifying',
    switching_config: 'Switching config',
    deleting_source: 'Deleting source',
    done: 'Done',
    failed: 'Failed',
    cancelled: 'Cancelled',
};

// MODPACKS_PREFIX matches both modpack-flavored data set ids ("modpacks" and
// "modpacks@core-storage"); their verification covers database-referenced
// objects only, so the report renders an extra note for both.
const MODPACKS_PREFIX = 'modpacks';

export default function StorageMigrationTab() {
    const [dataSets, setDataSets] = useState<StorageDataSet[]>([]);
    const [job, setJob] = useState<StorageMigrationJob | null>(null);
    const [loading, setLoading] = useState(true);

    const [wizardOpen, setWizardOpen] = useState(false);
    const [form, setForm] = useState<MigrationForm>(EMPTY_MIGRATION_FORM);
    const [submitting, setSubmitting] = useState(false);

    const [cancelling, setCancelling] = useState(false);
    const [busyDataSet, setBusyDataSet] = useState('');
    const [downloading, setDownloading] = useState(0);

    const [toast, setToast] = useState<{ msg: string; ok: boolean } | null>(null);
    const flash = (msg: string, ok = true) => {
        setToast({ msg, ok });
        setTimeout(() => setToast(null), 4000);
    };

    const load = useCallback(async () => {
        const [ov, jb] = await Promise.all([getStorageOverview(), getStorageMigration()]);
        if (ov.success && ov.dataSets) setDataSets(ov.dataSets);
        if (jb.success) setJob(jb.hasJob && jb.job ? jb.job : null);
        setLoading(false);
    }, []);

    useEffect(() => { load(); }, [load]);

    // Live refresh, mirroring DatabaseTab exactly: the SSE event covers the
    // start/finish transitions, and the poll covers in-phase progress, which
    // advances continuously (bytes/objects) without any handler firing.
    useEffect(() => {
        const unsub = systemEvents.on('storagemigration.changed', () => load());
        return () => unsub();
    }, [load]);

    useEffect(() => {
        if (!job || !storageMigrationInProgress(job.phase)) return;
        const t = setInterval(load, 2500);
        return () => clearInterval(t);
    }, [job, load]);

    const busy = !!job && storageMigrationInProgress(job.phase);
    const migratable = useMemo(() => dataSets.filter(d => d.migratable), [dataSets]);
    const sourceSet = useMemo(() => dataSets.find(d => d.id === form.dataSet), [dataSets, form.dataSet]);
    // Every migratable data set except the source itself is offerable as a
    // row-to-row target. Do NOT additionally filter on supportsTargetConfig:
    // that field says how a data set names ITS OWN target when acting as a
    // source, and Start() never consults it on the target side (it resolves
    // TargetDataSet through plain Resolve and checks only that it resolves and
    // differs from the source). Filtering it out here would make the panel
    // stricter than the API and would hide the legitimate
    // modpacks@core-storage -> modpacks pairing, which is how an operator
    // catches a leftover Core-storage copy up to an already-repointed
    // dedicated modpack backend.
    const targets = useMemo(
        () => migratable.filter(d => d.id !== form.dataSet),
        [migratable, form.dataSet],
    );
    const formValid = canStartMigration(form, dataSets);
    // The API refuses deleteSource unless the target is an ad-hoc config: with
    // a row-to-row target nothing repoints the consuming subsystem, so the
    // delete would orphan live references. The checkbox is not rendered at all
    // for that shape - a disabled-but-visible control with a 400 behind it is
    // worse than an explanation.
    const canOfferDelete = form.targetKind === 'config';

    // pickSource keeps targetKind consistent with what the chosen data set
    // actually accepts, and drops the other target field so a stale value can
    // never travel with the request. deleteSource resets too: leaving it set
    // while switching to a row-to-row source would silently disable the submit
    // button via canStartMigration with nothing on screen explaining why.
    const pickSource = (d: StorageDataSet) => setForm(f => ({
        ...f,
        dataSet: d.id,
        targetKind: d.supportsTargetConfig ? 'config' : 'dataset',
        targetDataSet: '',
        targetConfig: EMPTY_TARGET_CONFIG,
        deleteSource: false,
    }));

    const openWizard = () => {
        const first = migratable[0];
        setForm(first
            ? {
                ...EMPTY_MIGRATION_FORM,
                dataSet: first.id,
                targetKind: first.supportsTargetConfig ? 'config' : 'dataset',
            }
            : EMPTY_MIGRATION_FORM);
        setWizardOpen(true);
    };

    const submitMigration = async () => {
        if (!formValid) return;
        setSubmitting(true);
        const res = await startStorageMigration(startMigrateFromForm(form));
        setSubmitting(false);
        if (!res.success) {
            flash(res.message || 'Could not start the migration.', false);
            return;
        }
        setWizardOpen(false);
        flash('Migration started.');
        load();
    };

    const captureManifest = async (dataSet: string) => {
        setBusyDataSet(dataSet);
        const res = await startStorageMigration({ kind: 'manifest', dataSet });
        setBusyDataSet('');
        if (!res.success) {
            flash(res.message || 'Could not start the inventory.', false);
            return;
        }
        flash('Inventory started. Every object is read once and checksummed.');
        load();
    };

    const verifyAgainstLatest = async (ds: StorageDataSet, mode: VerifyMode) => {
        if (!ds.latestManifest) {
            flash('Capture a manifest first: there is nothing to verify against.', false);
            return;
        }
        setBusyDataSet(ds.id);
        const res = await startStorageMigration({
            kind: 'verify', dataSet: ds.id, verifyMode: mode, manifestId: ds.latestManifest.id,
        });
        setBusyDataSet('');
        if (!res.success) {
            flash(res.message || 'Could not start the verification.', false);
            return;
        }
        flash(`Verification started (${mode}).`);
        load();
    };

    const doCancel = async () => {
        setCancelling(true);
        const res = await cancelStorageMigration();
        setCancelling(false);
        if (!res.success) {
            flash(res.message || 'Could not cancel.', false);
            return;
        }
        flash('Cancellation requested; it takes effect at the next object boundary.');
        load();
    };

    const doDownload = async (manifestId: number, dataSet: string) => {
        setDownloading(manifestId);
        const res = await downloadManifestCSV(manifestId, dataSet);
        setDownloading(0);
        if (!res.success) flash(res.message || 'Download failed.', false);
    };

    if (loading) {
        return (
            <div className="space-y-6">
                <div className="card p-5 flex items-center gap-2 text-sm text-(--base-06)">
                    <Loader2 size={14} className="animate-spin" /> Loading storage overview...
                </div>
            </div>
        );
    }

    return (
        <div className="space-y-6">
            <div className="flex items-start justify-between gap-4">
                <div>
                    <h2 className="text-lg font-medium text-(--base-09) flex items-center gap-2">
                        <HardDrive size={18} className="text-(--accent-light)" /> Storage
                    </h2>
                    <p className="text-xs text-(--base-06) mt-1 max-w-2xl">
                        Inventory, move and verify the blob data sets. Every migration is manifest-based:
                        the source is read once and checksummed, the copy skips only objects that are already
                        byte-identical, and verification compares the target against that manifest.
                        The source is never modified unless you explicitly opt in to deleting it.
                    </p>
                </div>
                <button className="btn btn-primary shrink-0" onClick={openWizard} disabled={busy || migratable.length === 0}>
                    <Play size={14} /> New migration
                </button>
            </div>

            {job && (
                <JobPanel job={job} onCancel={doCancel} cancelling={cancelling} />
            )}

            <div className="card p-5 space-y-3">
                <div className="mono-label">Data sets</div>
                {dataSets.length === 0 ? (
                    <p className="alert alert-info text-xs">
                        No storage data sets are configured yet. Configure Core file storage first.
                    </p>
                ) : (
                    <div className="space-y-2">
                        {dataSets.map(ds => (
                            <DataSetRow
                                key={ds.id}
                                ds={ds}
                                busy={busy || busyDataSet === ds.id}
                                downloading={downloading}
                                onManifest={() => captureManifest(ds.id)}
                                onVerifyFull={() => verifyAgainstLatest(ds, 'full')}
                                onVerifySample={() => verifyAgainstLatest(ds, 'sample')}
                                onDownload={() => ds.latestManifest && doDownload(ds.latestManifest.id, ds.id)}
                            />
                        ))}
                    </div>
                )}
            </div>

            <div className="card p-5 space-y-3">
                <div className="mono-label">Manual migration (three steps, in this order)</div>
                <ol className="space-y-2 text-xs text-(--base-07)">
                    <li className="flex gap-3">
                        <span className="mono-label shrink-0 pt-0.5">1</span>
                        <span>
                            <strong className="text-(--base-09)">Capture a manifest.</strong>{' '}
                            Use <em>Inventory</em> on the data set below. This reads every object once and
                            records its SHA-256, so it costs a full pass over the data. Then download the CSV.
                        </span>
                    </li>
                    <li className="flex gap-3">
                        <span className="mono-label shrink-0 pt-0.5">2</span>
                        <span>
                            <strong className="text-(--base-09)">Move the data and reconfigure.</strong>{' '}
                            Copy the objects to the new backend with whatever tool you like (rclone, aws s3 sync, rsync),
                            then point Core file storage at it under Settings &gt; Core Storage. The panel does nothing in this step.
                        </span>
                    </li>
                    <li className="flex gap-3">
                        <span className="mono-label shrink-0 pt-0.5">3</span>
                        <span>
                            <strong className="text-(--base-09)">Verify.</strong>{' '}
                            Run <em>Verify (full)</em> against the manifest you captured. A full verification hashes
                            every object; a sample hashes a bounded subset and can never authorize deleting a source.
                        </span>
                    </li>
                </ol>
            </div>

            {wizardOpen && (
                <div className="modal-overlay animate-fade-in" onClick={() => !submitting && setWizardOpen(false)}>
                    <div className="modal-panel w-full max-w-lg" onClick={e => e.stopPropagation()}>
                        <div className="modal-header flex items-center justify-between">
                            <h3 className="modal-title">New migration</h3>
                            <button onClick={() => setWizardOpen(false)} className="p-1 text-(--base-06) hover:text-(--base-09)" disabled={submitting}>
                                <X size={16} />
                            </button>
                        </div>
                        <div className="modal-body space-y-5">
                            <div className="space-y-2">
                                <div className="mono-label">1. Source</div>
                                {migratable.map(d => (
                                    <ModeOption
                                        key={d.id}
                                        selected={form.dataSet === d.id}
                                        onClick={() => pickSource(d)}
                                        title={d.label}
                                        desc={d.backendLabel}
                                    />
                                ))}
                            </div>

                            <div className="space-y-2">
                                <div className="mono-label">2. Target</div>
                                {/* The source's own note is the only place the operator learns
                                    WHY a namespace inside the shared Core file storage cannot
                                    name a new backend and should be migrated as "core-storage"
                                    instead. Rendering it only in the data-set list would put it
                                    on a screen the operator has already left. */}
                                {sourceSet?.note && !sourceSet.supportsTargetConfig && (
                                    <p className="alert alert-info text-xs flex items-start gap-1.5">
                                        <Info size={12} className="mt-0.5 shrink-0" /> {sourceSet.note}
                                    </p>
                                )}
                                {sourceSet?.supportsTargetConfig ? (
                                    <TargetConfigFields
                                        cfg={form.targetConfig}
                                        onChange={patch => setForm(f => ({ ...f, targetConfig: { ...f.targetConfig, ...patch } }))}
                                    />
                                ) : targets.length === 0 ? (
                                    <p className="alert alert-info text-xs">No other migratable data set is available as a target.</p>
                                ) : targets.map(d => (
                                    <ModeOption
                                        key={d.id}
                                        selected={form.targetDataSet === d.id}
                                        onClick={() => setForm(f => ({ ...f, targetDataSet: d.id }))}
                                        title={d.label}
                                        desc={d.backendLabel}
                                    />
                                ))}
                            </div>

                            <p className="alert alert-info text-xs">
                                Order: copy, verify, switch the active config to the target, then delete the old copy
                                if you opt in. The switch happens only after a passing verification, and if it fails
                                nothing is deleted - the data stays in both places and the wizard tells you which
                                config is live.
                            </p>

                            <div className="space-y-2">
                                <div className="mono-label">3. Verification scope</div>
                                <ModeOption
                                    selected={form.verifyMode === 'full'}
                                    onClick={() => setForm(f => ({ ...f, verifyMode: 'full' }))}
                                    title="Full"
                                    desc="Hash every object on the target and compare it to the manifest. Slower, and the only scope that can authorize deleting the source."
                                />
                                <ModeOption
                                    selected={form.verifyMode === 'sample'}
                                    onClick={() => setForm(f => ({ ...f, verifyMode: 'sample', deleteSource: false }))}
                                    title="Sample"
                                    desc="Hash every small object plus a bounded draw of the large ones. Missing and extra objects are still detected in full."
                                />
                                {form.verifyMode === 'sample' && (
                                    <p className="alert alert-warning text-xs">
                                        A sampled verification does not check every object, so it can never authorize deleting the source.
                                    </p>
                                )}
                            </div>

                            <div className="space-y-2">
                                <div className="mono-label">4. After a passing verification</div>
                                {!canOfferDelete ? (
                                    <p className="alert alert-info text-xs">
                                        Moving into another data set leaves the active config pointing at the source, so this
                                        job cannot delete it: nothing would repoint the subsystem that reads it. Migrate,
                                        verify, repoint the storage yourself, and remove the old backend afterwards.
                                    </p>
                                ) : (
                                    <>
                                        <label className={`flex items-start gap-2 text-xs ${deleteSourceAllowed(form.verifyMode) ? 'text-(--base-07)' : 'text-(--base-05) cursor-not-allowed'}`}>
                                            <input
                                                type="checkbox"
                                                className="mt-0.5"
                                                checked={form.deleteSource}
                                                disabled={!deleteSourceAllowed(form.verifyMode)}
                                                onChange={e => setForm(f => ({ ...f, deleteSource: e.target.checked }))}
                                            />
                                            <span>Delete the source after a passing verification</span>
                                        </label>
                                        <p className="alert alert-warning text-xs">
                                            This is irreversible and cannot be cancelled once it starts. The source should be quiet for a
                                            delete run: objects written after the manifest was captured are not in it, will show up as
                                            &quot;extra&quot; at verification, and will NOT be deleted.
                                        </p>
                                    </>
                                )}
                            </div>
                        </div>
                        <div className="modal-footer">
                            <button onClick={() => setWizardOpen(false)} className="btn btn-secondary" disabled={submitting}>Cancel</button>
                            <button
                                onClick={submitMigration}
                                className="btn btn-primary disabled:opacity-40 disabled:cursor-not-allowed"
                                disabled={!formValid || submitting}
                            >
                                {submitting ? <Loader2 size={14} className="animate-spin" /> : <ArrowRight size={14} />} Start migration
                            </button>
                        </div>
                    </div>
                </div>
            )}

            {toast && (
                <div className="toast-container">
                    <div className="toast">
                        <span className={`toast-bar ${toast.ok ? 'bg-(--success-light)' : 'bg-(--error-light)'}`} />
                        <span className="text-xs">{toast.msg}</span>
                    </div>
                </div>
            )}
        </div>
    );
}

function DataSetRow({
    ds, busy, downloading, onManifest, onVerifyFull, onVerifySample, onDownload,
}: {
    ds: StorageDataSet;
    busy: boolean;
    downloading: number;
    onManifest: () => void;
    onVerifyFull: () => void;
    onVerifySample: () => void;
    onDownload: () => void;
}) {
    const m = ds.latestManifest;
    return (
        <div className="flex items-center justify-between gap-3 bg-(--base-03) border border-(--base-04) rounded-md px-3 py-2.5">
            <div className="flex items-center gap-3 min-w-0">
                <HardDrive size={16} className="text-(--primary-light) shrink-0" />
                <div className="min-w-0">
                    <div className="flex items-center gap-2 flex-wrap">
                        <span className="text-sm font-medium text-(--base-09)">{ds.label}</span>
                        {ds.migratable
                            ? <span className="badge badge-accent">migratable</span>
                            : <span className="badge badge-neutral">manual only</span>}
                    </div>
                    <div className="mono-label truncate">{ds.backendLabel}</div>
                    <div className="text-[11px] text-(--base-06) mt-0.5">
                        {m
                            ? <>Last inventory: {m.objectCount.toLocaleString()} objects, {formatBytes(m.totalBytes)} ({new Date(m.capturedAt).toLocaleString()})</>
                            : <>Not yet inventoried</>}
                    </div>
                    {ds.note && (
                        <div className="text-[11px] text-(--base-05) mt-0.5 flex items-start gap-1">
                            <Info size={11} className="mt-0.5 shrink-0" /> {ds.note}
                        </div>
                    )}
                </div>
            </div>
            <div className="flex items-center gap-1.5 shrink-0">
                <button onClick={onManifest} className="btn btn-secondary btn-sm" disabled={busy} title="Read every object once and record its checksum">
                    <FileSearch size={12} /> Inventory
                </button>
                <button onClick={onVerifyFull} className="btn btn-secondary btn-sm" disabled={busy || !m} title="Hash every object against the last manifest">
                    Verify (full)
                </button>
                <button onClick={onVerifySample} className="btn btn-secondary btn-sm" disabled={busy || !m} title="Hash a bounded sample against the last manifest">
                    Verify (sample)
                </button>
                <button onClick={onDownload} className="btn btn-secondary btn-sm" disabled={!m || downloading === m?.id} title="Download the manifest as CSV">
                    {m && downloading === m.id ? <Loader2 size={12} className="animate-spin" /> : <Download size={12} />}
                </button>
            </div>
        </div>
    );
}

function PhaseBadge({ phase, stale }: { phase: StorageMigrationPhase; stale: boolean }) {
    const cls = phase === 'done'
        ? 'bg-(--success-ghost) text-(--success-light) border-(--success-border)'
        : phase === 'failed'
            ? 'bg-(--error-ghost) text-(--error-light) border-(--error-border)'
            : phase === 'cancelled'
                ? 'bg-(--base-03) text-(--base-06) border-(--base-04)'
                : phase === 'deleting_source' || phase === 'switching_config'
                    ? 'bg-(--warning-ghost) text-(--warning-light) border-(--warning-border)'
                    : stale
                        ? 'bg-(--error-ghost) text-(--error-light) border-(--error-border)'
                        : 'bg-(--accent-ghost) text-(--accent-light) border-(--accent)';
    return (
        <span className={`inline-flex items-center gap-1 px-2 py-0.5 rounded text-[10px] font-mono uppercase tracking-wide border ${cls}`}>
            {storageMigrationInProgress(phase) && !stale && <Loader2 size={10} className="animate-spin" />}
            {PHASE_LABEL[phase]}
        </span>
    );
}

function JobPanel({ job, onCancel, cancelling }: { job: StorageMigrationJob; onCancel: () => void; cancelling: boolean }) {
    const [confirmCancel, setConfirmCancel] = useState(false);
    const pct = progressPercent(job);
    const inProgress = storageMigrationInProgress(job.phase);

    return (
        <div className="card p-5 space-y-4">
            <div className="flex items-center justify-between">
                <div className="mono-label">{job.kind} job</div>
                <PhaseBadge phase={job.phase} stale={job.stale} />
            </div>

            <div className="text-xs text-(--base-06) space-y-0.5">
                <div>
                    Started by <span className="text-(--base-08)">{job.startedByName || job.startedBy || 'unknown'}</span>
                    {' '}&middot; {new Date(job.startedAt).toLocaleString()}
                </div>
                <div>Source: <span className="font-mono">{job.sourceLabel || job.dataSet}</span></div>
                {job.targetLabel && <div>Target: <span className="font-mono">{job.targetLabel}</span></div>}
                {job.deleteSource && (
                    <div className="text-(--warning-light) flex items-center gap-1">
                        <ShieldAlert size={12} /> The source will be deleted, but only after a passing full verification AND a successful config switch.
                    </div>
                )}
                {job.phase === 'switching_config' && (
                    <div className="text-(--warning-light) flex items-center gap-1">
                        <ArrowRight size={12} /> Pointing the active storage config at the target. This step is not cancellable.
                    </div>
                )}
                {job.configSwitched && job.phase !== 'switching_config' && (
                    <div className="text-(--success-light) flex items-center gap-1">
                        <CircleCheck size={12} /> The active config now points at the target; the system is reading from it.
                    </div>
                )}
                {job.stale && (
                    <div className="text-(--error-light) flex items-center gap-1">
                        <CircleAlert size={12} /> No heartbeat - the Core running this job may have stopped.
                    </div>
                )}
            </div>

            {(inProgress || job.phase === 'done') && (
                <div>
                    <div className="flex justify-between text-[11px] text-(--base-06) mb-1">
                        <span className="truncate pr-2">{job.currentKey ? `current: ${job.currentKey}` : PHASE_LABEL[job.phase]}</span>
                        <span className="shrink-0">
                            {formatBytes(job.bytesDone)} / {formatBytes(job.bytesTotal)}
                            {' '}&middot; {job.objectsDone.toLocaleString()}/{job.objectsTotal.toLocaleString()} objects
                            {job.objectsSkipped > 0 && <> &middot; {job.objectsSkipped.toLocaleString()} skipped</>}
                        </span>
                    </div>
                    <div className="h-2 rounded-full bg-(--base-03) overflow-hidden">
                        <div
                            className={`h-full transition-all ${job.phase === 'done' ? 'bg-(--success-light)' : 'bg-(--accent)'}`}
                            style={{ width: `${pct}%` }}
                        />
                    </div>
                </div>
            )}

            {job.error && (
                <div className="text-xs text-(--error-light) bg-(--error-ghost) border border-(--error-border) rounded-md p-3">
                    {job.error}
                </div>
            )}

            {/* A manifest job carries no verification report, so it must not be
                labelled NOT VERIFIED and must not point at a report that is not
                there. Only a run that actually produced one gets a verdict. */}
            {job.phase === 'done' && (
                <div className="text-xs text-(--success-light) bg-(--success-ghost) border border-(--success-border) rounded-md p-3 flex items-center gap-1">
                    <CircleCheck size={12} />
                    {job.verify
                        ? `${verifyVerdictLabel(job.verify)} - see the report below.`
                        : 'Finished.'}
                </div>
            )}

            {job.verify && <VerifyView report={job.verify} dataSet={job.dataSet} />}

            {job.log?.length > 0 && (
                <details className="text-xs" open={inProgress}>
                    <summary className="cursor-pointer text-(--base-06) mono-label">Log ({job.log.length})</summary>
                    <pre className="mt-2 max-h-64 overflow-auto bg-(--base-01) border border-(--base-03) rounded-md p-3 font-mono text-[11px] text-(--base-06) whitespace-pre-wrap">
{job.log.join('\n')}
                    </pre>
                </details>
            )}

            {isCancellablePhase(job.phase) && (
                !confirmCancel ? (
                    <button className="btn btn-secondary btn-sm" onClick={() => setConfirmCancel(true)} disabled={cancelling}>
                        Cancel job
                    </button>
                ) : (
                    <div className="flex flex-wrap items-center gap-2 bg-(--warning-ghost) border border-(--warning-border) rounded-md px-3 py-2">
                        <ShieldAlert size={14} className="text-(--warning-light)" />
                        <span className="text-xs text-(--base-07)">
                            Cancelling stops at the next object boundary. Nothing half-written is left behind and the source is untouched. Continue?
                        </span>
                        <button className="btn btn-primary btn-sm" onClick={onCancel} disabled={cancelling}>
                            {cancelling ? <Loader2 size={14} className="animate-spin" /> : null} Yes, cancel
                        </button>
                        <button className="btn btn-secondary btn-sm" onClick={() => setConfirmCancel(false)} disabled={cancelling}>Keep running</button>
                    </div>
                )
            )}
        </div>
    );
}

function VerifyView({ report, dataSet }: { report: StorageVerifyReport; dataSet: string }) {
    return (
        <div className="space-y-2">
            <div className={`text-xs ${report.mode === 'sample' ? 'alert alert-info' : 'text-(--base-06)'}`}>
                <span className="font-mono uppercase tracking-wide mr-2">{verifyVerdictLabel(report)}</span>
                Checked {report.objectsChecked.toLocaleString()} of {report.objectsInManifest.toLocaleString()} objects
                ({formatPercent(report.checkedFraction)}) and {formatBytes(report.bytesChecked)} of {formatBytes(report.bytesInManifest)}
                {' '}({formatPercent(report.bytesCheckedFraction)}).
                {report.mode === 'sample' && ' Missing and extra objects were still detected in full.'}
            </div>

            {dataSet.startsWith(MODPACKS_PREFIX) && (
                <div className="alert alert-info text-xs">
                    Modpack keys come from the database, so this verification covers database-referenced objects only.
                    An object in storage that no row points at is invisible to it.
                </div>
            )}

            {report.problemsTotal > 0 && (
                <details className="text-xs" open>
                    <summary className="cursor-pointer text-(--base-06) mono-label">
                        Problems ({report.problemsTotal.toLocaleString()}
                        {report.problems.length < report.problemsTotal && `, showing the first ${report.problems.length.toLocaleString()}`})
                    </summary>
                    <div className="mt-2 overflow-auto">
                        <table className="w-full text-xs font-mono">
                            <thead>
                                <tr className="text-(--base-05) text-left">
                                    <th className="py-1 pr-4 font-normal">Key</th>
                                    <th className="py-1 pr-4 font-normal text-right">Expected</th>
                                    <th className="py-1 pr-4 font-normal text-right">Actual</th>
                                    <th className="py-1 font-normal">Status</th>
                                </tr>
                            </thead>
                            <tbody>
                                {report.problems.map(p => (
                                    <tr key={`${p.key}-${p.status}`} className="border-t border-(--base-03)">
                                        <td className="py-1 pr-4 text-(--base-08) break-all">{p.key}</td>
                                        <td className="py-1 pr-4 text-right text-(--base-06)">{p.expectedSize > 0 ? formatBytes(p.expectedSize) : '-'}</td>
                                        <td className="py-1 pr-4 text-right text-(--base-06)">{p.actualSize > 0 ? formatBytes(p.actualSize) : '-'}</td>
                                        <td className="py-1">
                                            <span className={
                                                p.status === 'extra'
                                                    ? 'text-(--warning-light)'
                                                    : 'text-(--error-light)'
                                            }>{p.status}</span>
                                        </td>
                                    </tr>
                                ))}
                            </tbody>
                        </table>
                        {report.problems.length < report.problemsTotal && (
                            <p className="text-[11px] text-(--base-05) mt-2">
                                The full list is in the manifest CSV export and in the job log.
                            </p>
                        )}
                    </div>
                </details>
            )}
        </div>
    );
}

// TargetConfigFields is the ad-hoc target storage form. It deliberately mirrors
// the Core file storage settings form field-for-field, because it IS the same
// config shape - the operator is describing a second instance of the thing they
// already configured once, so the two screens must not look like different
// concepts.
//
// The secret follows that form's UX exactly: type="password", never prefilled,
// never read back. No endpoint returns a stored S3 secret, and this one is for a
// backend that has not been saved yet, so there is nothing to prefill from and
// the operator always enters it fresh.
function TargetConfigFields({
    cfg, onChange,
}: {
    cfg: TargetConfigForm;
    onChange: (patch: Partial<TargetConfigForm>) => void;
}) {
    return (
        <div className="space-y-3">
            <ModeOption
                selected={cfg.backend === 's3'}
                onClick={() => onChange({ backend: 's3' })}
                title="S3-compatible"
                desc="Another bucket, another provider, or the same bucket under a different prefix."
            />
            <ModeOption
                selected={cfg.backend === 'path'}
                onClick={() => onChange({ backend: 'path' })}
                title="Filesystem path"
                desc="A directory inside the Core container: an NFS, WebDAV or SMB mount, or a local volume."
            />

            {cfg.backend === 's3' && (
                <div className="space-y-2">
                    <Field label="Endpoint" value={cfg.s3Endpoint} onChange={v => onChange({ s3Endpoint: v })} placeholder="https://s3.example.com" />
                    <Field label="Bucket" value={cfg.s3Bucket} onChange={v => onChange({ s3Bucket: v })} />
                    <Field label="Region" value={cfg.s3Region} onChange={v => onChange({ s3Region: v })} placeholder="auto" />
                    <Field label="Access key" value={cfg.s3AccessKey} onChange={v => onChange({ s3AccessKey: v })} />
                    <Field label="Secret key" value={cfg.s3SecretKey} onChange={v => onChange({ s3SecretKey: v })} type="password" autoComplete="new-password" />
                    <Field label="Prefix (optional)" value={cfg.s3Prefix} onChange={v => onChange({ s3Prefix: v })} placeholder="prod" />
                    <label className="flex items-center gap-2 text-xs text-(--base-07)">
                        <input type="checkbox" checked={cfg.s3PathStyle} onChange={e => onChange({ s3PathStyle: e.target.checked })} />
                        <span>Use path-style addressing (required by MinIO and some S3-compatible providers)</span>
                    </label>
                </div>
            )}

            {cfg.backend === 'path' && (
                <div className="space-y-2">
                    <Field label="Absolute path" value={cfg.path} onChange={v => onChange({ path: v })} placeholder="/mnt/new-storage" />
                    <label className="flex items-start gap-2 text-xs text-(--base-07)">
                        <input type="checkbox" className="mt-0.5" checked={cfg.pathConfirmed} onChange={e => onChange({ pathConfirmed: e.target.checked })} />
                        <span>
                            I confirm this path is reachable from every Core instance and is not the same directory
                            as the current one. The server checks that too, by device and inode rather than by name,
                            so a symlink or a bind mount to the current location is refused.
                        </span>
                    </label>
                </div>
            )}
        </div>
    );
}

// Field is the shared labelled input for the target config form, using the
// established token classes so it matches the Core file storage settings form.
function Field({
    label, value, onChange, placeholder, type = 'text', autoComplete,
}: {
    label: string;
    value: string;
    onChange: (v: string) => void;
    placeholder?: string;
    type?: string;
    autoComplete?: string;
}) {
    return (
        <label className="block">
            <span className="mono-label">{label}</span>
            <input
                type={type}
                value={value}
                placeholder={placeholder}
                autoComplete={autoComplete}
                onChange={e => onChange(e.target.value)}
                className="mt-1 w-full bg-(--base-02) border border-(--base-04) rounded-md px-3 py-2 text-sm text-(--base-09) focus:border-(--accent) focus:shadow-[0_0_0_3px_rgba(112,72,200,0.15)] outline-none transition"
            />
        </label>
    );
}

// ModeOption is local to this tab: BackupsTab's copy is not exported.
function ModeOption({
    selected, onClick, title, desc,
}: {
    selected: boolean;
    onClick: () => void;
    title: string;
    desc: string;
}) {
    return (
        <button
            type="button"
            onClick={onClick}
            className={`w-full text-left rounded-md border px-3 py-2.5 transition flex items-start gap-3
                ${selected
                    ? 'border-(--accent) bg-(--accent)/8 shadow-[0_0_0_3px_rgba(112,72,200,0.10)]'
                    : 'border-(--base-04) bg-(--base-03) hover:border-(--base-05)'}`}
        >
            <span
                aria-hidden
                className={`mt-1 inline-flex w-3.5 h-3.5 rounded-full border-2 shrink-0
                    ${selected ? 'border-(--accent) bg-(--accent)' : 'border-(--base-05) bg-transparent'}`}
            />
            <span className="min-w-0">
                <span className="block text-sm font-medium text-(--base-09)">{title}</span>
                <span className="block text-xs text-(--base-06) mt-0.5 break-all">{desc}</span>
            </span>
        </button>
    );
}
