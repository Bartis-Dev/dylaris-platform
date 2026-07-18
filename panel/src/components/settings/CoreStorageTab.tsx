"use client";

import React, { useState, useEffect, useRef } from 'react';
import { getCoreStorage, saveCoreStorage, testCoreStorage, migrateCoreStorage } from '@/lib/api/coreStorage';
import { canSaveCoreStorage, s3IdentityChanged, summarizeMigrateResult, type CoreStorageConfig, type MigrateSummary } from '@/lib/coreStorage';
import { Cable, CircleCheck, CircleAlert, HardDrive, Cloud, AlertTriangle, ArrowRightLeft, Loader2 } from 'lucide-react';
import { SkeletonHeader, SkeletonCard, SkeletonFormRow } from '@/components/Skeleton';
import { useUnsavedChanges } from '@/components/settings/UnsavedChanges';

const BACKENDS = [
  { id: 'path', label: 'Filesystem Path', description: 'A local disk or an OS-level mount (NFS/SMB/WebDAV). Must be reachable by every Core.', icon: HardDrive },
  { id: 's3', label: 'S3-compatible', description: 'Any S3 API: AWS, Cloudflare R2, Backblaze B2, Hetzner, MinIO, Wasabi.', icon: Cloud },
] as const;

const EMPTY: CoreStorageConfig = {
  backend: 'path', path: '', pathConfirmed: false,
  s3Endpoint: '', s3Bucket: '', s3Region: '', s3AccessKey: '', s3SecretKey: '',
  s3PathStyle: false, s3Prefix: '', s3SecretSet: false,
};

export default function CoreStorageTab() {
  const [settings, setSettings] = useState<CoreStorageConfig>(EMPTY);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [testing, setTesting] = useState(false);
  const [migrating, setMigrating] = useState(false);
  const [toast, setToast] = useState<{ msg: string; ok: boolean } | null>(null);
  const [migrateSummary, setMigrateSummary] = useState<MigrateSummary | null>(null);

  // Snapshot of the last-saved config, used for dirty detection AND as the
  // source of truth for "is there already a valid config on the server"
  // (the Migrate action acts on the SAVED config, not the in-progress form).
  const snapshotRef = useRef<CoreStorageConfig | null>(null);

  const showToast = (msg: string, ok = true) => { setToast({ msg, ok }); setTimeout(() => setToast(null), 4500); };

  useEffect(() => {
    getCoreStorage().then(res => {
      if (res.success && res.settings) {
        // The secret is never returned by GET; keep the local field blank so
        // "unchanged" always means "the admin didn't type anything new".
        const s: CoreStorageConfig = { ...EMPTY, ...res.settings, s3SecretKey: '' };
        setSettings(s);
        snapshotRef.current = s;
      }
      setLoading(false);
    });
  }, []);

  const canSave = canSaveCoreStorage(settings);
  const savedConfigured = snapshotRef.current !== null && canSaveCoreStorage(snapshotRef.current);
  const identityChanged = settings.backend === 's3' && s3IdentityChanged(settings, snapshotRef.current);

  const handleSave = async () => {
    if (!canSave) {
      showToast(
        settings.backend === 'path'
          ? 'Enter an absolute path and tick the confirmation checkbox before saving.'
          : 'Fill in the bucket, access key and secret before saving.',
        false,
      );
      return;
    }
    setSaving(true);
    const res = await saveCoreStorage(settings);
    if (res.success) {
      showToast('Core file storage saved.');
      const saved: CoreStorageConfig = { ...settings, s3SecretKey: '', s3SecretSet: settings.s3SecretSet || settings.s3SecretKey !== '' };
      setSettings(saved);
      snapshotRef.current = saved;
    } else {
      showToast(res.message || 'Save failed.', false);
    }
    setSaving(false);
  };

  const handleDiscard = () => { if (snapshotRef.current) setSettings(snapshotRef.current); };

  const dirty = snapshotRef.current !== null && JSON.stringify(settings) !== JSON.stringify(snapshotRef.current);
  useUnsavedChanges({ dirty, save: handleSave, discard: handleDiscard, saving });

  const handleTest = async () => {
    setTesting(true);
    const res = await testCoreStorage(settings);
    if (res.success && res.ok) showToast(res.message || 'Connection successful: write, read and delete all succeeded.');
    else showToast(res.message || 'Connection test failed.', false);
    setTesting(false);
  };

  const handleMigrate = async () => {
    if (!savedConfigured) {
      showToast('Save a valid Core file storage config before migrating.', false);
      return;
    }
    setMigrating(true);
    setMigrateSummary(null);
    const res = await migrateCoreStorage();
    // IMPORTANT: interpret the BODY (summarizeMigrateResult), never res.ok /
    // HTTP status alone - a partial failure comes back as HTTP 200 with
    // success:false (see core/handlers/core_storage.go Migrate).
    const summary = summarizeMigrateResult(res);
    setMigrateSummary(summary);
    if (summary.ok) {
      showToast(`Migration complete: ${summary.totalCopied} copied, ${summary.totalSkipped} already present.`);
    } else if (res.message && summary.perSubsystem.length === 0) {
      // Hard failure before any subsystem ran (e.g. 400 "not configured").
      showToast(res.message, false);
    } else {
      showToast(`Migration finished with ${summary.totalFailed} failure(s). See details below.`, false);
    }
    setMigrating(false);
  };

  const set = <K extends keyof CoreStorageConfig>(key: K, value: CoreStorageConfig[K]) =>
    setSettings(prev => ({ ...prev, [key]: value }));

  if (loading) return (
    <div className="max-w-2xl space-y-6">
      <SkeletonHeader />
      <div className="grid grid-cols-1 sm:grid-cols-2 gap-3"><SkeletonCard height="h-24" /><SkeletonCard height="h-24" /></div>
      <SkeletonFormRow />
    </div>
  );

  return (
    <div className="max-w-2xl space-y-6">
      <div>
        <h2 className="text-base font-display font-bold text-(--base-09) mb-1">Core File Storage</h2>
        <p className="text-sm text-(--base-07)">Where Core keeps Library files, ticket attachments and ticket backups. Required before running more than one Core, and before enabling the Ticket System.</p>
      </div>

      {/* Backend selector */}
      <div>
        <label className="input-label mb-2 block">Backend</label>
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
          {BACKENDS.map(b => {
            const Icon = b.icon;
            const active = settings.backend === b.id;
            return (
              <button
                key={b.id}
                type="button"
                onClick={() => set('backend', b.id)}
                className={`card p-4 text-left transition-all relative focus:outline-none focus-visible:ring-2 focus-visible:ring-(--accent) ${
                  active ? 'border-(--accent) ring-1 ring-(--accent)/40 bg-(--accent-ghost)' : 'border-(--base-03) hover:border-(--base-05)'
                }`}
              >
                <div className="flex items-start gap-3">
                  <div className={`w-9 h-9 rounded-md flex items-center justify-center shrink-0 ${active ? 'bg-(--accent)/20 text-(--accent-light)' : 'bg-(--base-03) text-(--base-06)'}`}>
                    <Icon size={18} />
                  </div>
                  <div className="min-w-0">
                    <div className={`font-medium text-sm flex items-center gap-1.5 ${active ? 'text-(--accent-light)' : 'text-(--base-09)'}`}>
                      {b.label}
                      {active && <CircleCheck size={13} className="text-(--accent-light)" />}
                    </div>
                    <div className="text-xs text-(--base-06) mt-1">{b.description}</div>
                  </div>
                </div>
              </button>
            );
          })}
        </div>
      </div>

      {/* Path backend */}
      {settings.backend === 'path' && (
        <div className="space-y-4">
          <div className="flex flex-col gap-[5px]">
            <label className="input-label">Absolute Path</label>
            <input
              type="text"
              value={settings.path}
              onChange={e => set('path', e.target.value)}
              placeholder="/mnt/dylaris-shared"
              className="input-mono w-full"
            />
          </div>

          <div className="flex items-start gap-2 rounded-(--radius-md) border border-(--warning-border) bg-(--warning-ghost) px-3 py-2.5">
            <AlertTriangle size={16} className="text-(--warning) shrink-0 mt-0.5" />
            <p className="text-xs text-(--base-08)">
              Must be reachable by <strong>every</strong> Core. A host-local directory only works with a single Core or
              all Cores pinned to one host. For multiple Cores across hosts, mount a shared filesystem here (NFS/SMB/WebDAV)
              or use S3.
            </p>
          </div>

          <label className="flex items-start gap-2 cursor-pointer select-none group">
            <input
              type="checkbox"
              checked={settings.pathConfirmed}
              onChange={e => set('pathConfirmed', e.target.checked)}
              className="mt-0.5 accent-(--accent)"
            />
            <span className="text-xs text-(--base-08) group-hover:text-(--base-09)">
              I confirm this path is shared across all Cores, or I run a single Core.
            </span>
          </label>
          {!settings.pathConfirmed && (
            <p className="text-xs text-(--error-light)">Required: Save is disabled until this is confirmed.</p>
          )}
        </div>
      )}

      {/* S3 backend */}
      {settings.backend === 's3' && (
        <div className="space-y-4">
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
            <div className="flex flex-col gap-[5px]">
              <label className="input-label">Endpoint URL (blank = AWS)</label>
              <input type="text" value={settings.s3Endpoint} onChange={e => set('s3Endpoint', e.target.value)}
                placeholder="https://s3.example.com" className="input-mono w-full" />
            </div>
            <div className="flex flex-col gap-[5px]">
              <label className="input-label">Bucket</label>
              <input type="text" value={settings.s3Bucket} onChange={e => set('s3Bucket', e.target.value)}
                placeholder="dylaris-core" className="input-mono w-full" />
            </div>
            <div className="flex flex-col gap-[5px]">
              <label className="input-label">Region</label>
              <input type="text" value={settings.s3Region} onChange={e => set('s3Region', e.target.value)}
                placeholder="us-east-1" className="input-mono w-full" />
            </div>
            <div className="flex flex-col gap-[5px]">
              <label className="input-label">Access Key</label>
              <input type="text" value={settings.s3AccessKey} onChange={e => set('s3AccessKey', e.target.value)}
                placeholder="AKIA..." className="input-mono w-full" />
            </div>
          </div>

          <div className="flex flex-col gap-[5px]">
            <label className="input-label">Secret Key</label>
            <input
              type="password"
              value={settings.s3SecretKey || ''}
              onChange={e => set('s3SecretKey', e.target.value)}
              placeholder={settings.s3SecretSet ? 'Leave blank to keep the stored secret' : '••••••••••••'}
              className="input-mono w-full"
              autoComplete="new-password"
            />
            {settings.s3SecretSet && !settings.s3SecretKey && (
              <p className="text-xs text-(--success-light) flex items-center gap-1">
                <CircleCheck size={12} /> A secret is already stored. Leaving this blank keeps it.
              </p>
            )}
            {identityChanged && (
              <p className="flex items-start gap-1.5 text-xs text-(--warning-light) bg-(--warning-ghost) border border-(--warning-border) rounded-(--radius-md) px-2.5 py-2 mt-1">
                <AlertTriangle size={13} className="mt-0.5 shrink-0" />
                <span>
                  You changed the endpoint, bucket or access key. Re-enter the secret to save: the backend refuses to pair
                  a stored secret with a different identity (this prevents credentials from being silently re-pointed at a
                  different endpoint).
                </span>
              </p>
            )}
          </div>

          <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
            <div className="flex flex-col gap-[5px]">
              <label className="input-label">Key Prefix (optional)</label>
              <input type="text" value={settings.s3Prefix} onChange={e => set('s3Prefix', e.target.value)}
                placeholder="core" className="input-mono w-full" />
            </div>
            <label className="flex items-center gap-2 sm:mt-6 cursor-pointer select-none group">
              <input type="checkbox" checked={settings.s3PathStyle} onChange={e => set('s3PathStyle', e.target.checked)} className="accent-(--accent)" />
              <span className="text-xs text-(--base-08) group-hover:text-(--base-09)">Path-style addressing (required for MinIO)</span>
            </label>
          </div>
        </div>
      )}

      {/* Actions */}
      <div className="flex flex-wrap gap-3 pt-4 border-t border-(--base-03)">
        <button
          type="button"
          onClick={handleSave}
          disabled={!canSave || saving}
          className="btn btn-primary disabled:opacity-40 disabled:cursor-not-allowed inline-flex items-center gap-1.5"
        >
          {saving && <Loader2 size={14} className="animate-spin" />}
          {saving ? 'Saving...' : 'Save'}
        </button>
        <button
          type="button"
          onClick={handleTest}
          disabled={testing}
          className="btn btn-secondary disabled:opacity-40 disabled:cursor-not-allowed inline-flex items-center gap-1.5"
        >
          {testing ? <Loader2 size={14} className="animate-spin" /> : <Cable size={14} />}
          {testing ? 'Testing...' : 'Test Connection'}
        </button>
        <button
          type="button"
          onClick={handleMigrate}
          disabled={migrating || !savedConfigured}
          title={!savedConfigured ? 'Save a valid config first' : undefined}
          className="btn btn-secondary disabled:opacity-40 disabled:cursor-not-allowed inline-flex items-center gap-1.5"
        >
          {migrating ? <Loader2 size={14} className="animate-spin" /> : <ArrowRightLeft size={14} />}
          {migrating ? 'Migrating...' : 'Migrate local data'}
        </button>
      </div>
      <p className="text-xs text-(--base-06)">
        Copies the existing on-disk Library, ticket-attachments and ticket-backups files into the backend configured above.
        Originals are never deleted or modified, and files already present at the destination are skipped - so this is
        always safe to re-run after a partial failure.
        {!savedConfigured && ' Save a valid config above to enable this.'}
      </p>

      {/* Migration result detail */}
      {migrateSummary && (
        <div className={`card p-4 space-y-3 border ${migrateSummary.ok ? 'border-(--success-light)/40' : 'border-(--error)/40'}`}>
          <div className="flex items-center gap-2 text-sm font-medium">
            {migrateSummary.ok ? (
              <CircleCheck size={15} className="text-(--success-light)" />
            ) : (
              <CircleAlert size={15} className="text-(--error-light)" />
            )}
            <span className="text-(--base-09)">
              {migrateSummary.ok ? 'Migration completed successfully' : 'Migration finished with failures'}
            </span>
          </div>
          {migrateSummary.perSubsystem.length > 0 && (
            <div className="overflow-x-auto">
              <table className="w-full text-xs">
                <thead>
                  <tr className="text-(--base-06) font-mono uppercase text-[10px] tracking-[0.08em]">
                    <th className="text-left py-1 pr-3">Subsystem</th>
                    <th className="text-right py-1 px-3">Copied</th>
                    <th className="text-right py-1 px-3">Skipped</th>
                    <th className="text-right py-1 pl-3">Failed</th>
                  </tr>
                </thead>
                <tbody>
                  {migrateSummary.perSubsystem.map(s => (
                    <tr key={s.name} className="border-t border-(--base-03)">
                      <td className="py-1.5 pr-3 text-(--base-09) font-mono">{s.name}</td>
                      <td className="py-1.5 px-3 text-right text-(--success-light)">{s.copied}</td>
                      <td className="py-1.5 px-3 text-right text-(--base-07)">{s.skipped}</td>
                      <td className={`py-1.5 pl-3 text-right ${s.failed > 0 ? 'text-(--error-light)' : 'text-(--base-07)'}`}>{s.failed}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
              {migrateSummary.perSubsystem.some(s => s.errors.length > 0) && (
                <div className="mt-2 space-y-1">
                  {migrateSummary.perSubsystem.filter(s => s.errors.length > 0).map(s => (
                    <div key={s.name}>
                      <div className="text-[11px] font-mono text-(--base-06) uppercase tracking-[0.06em]">{s.name} errors</div>
                      <ul className="list-disc list-inside text-xs text-(--error-light) space-y-0.5">
                        {s.errors.map((e, i) => <li key={i}>{e}</li>)}
                      </ul>
                    </div>
                  ))}
                </div>
              )}
            </div>
          )}
          {migrateSummary.note && <p className="text-xs text-(--base-06)">{migrateSummary.note}</p>}
        </div>
      )}

      {toast && (
        <div className="toast-container"><div className="toast">
          <div className={`toast-bar ${toast.ok ? 'bg-(--success-light)' : 'bg-(--error-light)'}`}></div>
          {toast.ok ? <CircleCheck size={14} /> : <CircleAlert size={14} />}
          <span className="text-sm text-(--base-09)">{toast.msg}</span>
        </div></div>
      )}
    </div>
  );
}
