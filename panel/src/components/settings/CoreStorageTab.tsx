"use client";

import React, { useState, useEffect, useReducer, useRef } from 'react';
import { getCoreStorage, saveCoreStorage, testCoreStorage } from '@/lib/api/coreStorage';
import { canSaveCoreStorage, s3IdentityChanged, type CoreStorageConfig } from '@/lib/coreStorage';
import { listStorageConnections, type StorageConnection } from '@/lib/api';
import { Cable, CircleCheck, CircleAlert, HardDrive, Cloud, AlertTriangle, Loader2, Info } from 'lucide-react';
import { SkeletonHeader, SkeletonCard, SkeletonFormRow } from '@/components/Skeleton';
import { useUnsavedChanges } from '@/components/settings/UnsavedChanges';
import { Badge } from '@/components/ui/Badge';
import { systemEvents } from '@/lib/systemEvents';
import { reachReducer, initialReachState, statusLabel, statusRemedy, parseStorageReachEvent } from '@/lib/storageReach';

const BACKENDS = [
  { id: 'path', label: 'Filesystem Path', description: 'A local disk or an OS-level mount (NFS/SMB/WebDAV). Must be reachable by every Core.', icon: HardDrive },
  { id: 's3', label: 'S3-compatible', description: 'Any S3 API: AWS, Cloudflare R2, Backblaze B2, Hetzner, MinIO, Wasabi.', icon: Cloud },
] as const;

const EMPTY: CoreStorageConfig = {
  backend: 'path', path: '', pathConfirmed: false,
  s3Endpoint: '', s3Bucket: '', s3Region: '', s3AccessKey: '', s3SecretKey: '',
  s3PathStyle: false, s3Prefix: '', s3SecretSet: false, connectionId: 0,
};

export default function CoreStorageTab() {
  const [settings, setSettings] = useState<CoreStorageConfig>(EMPTY);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [testing, setTesting] = useState(false);
  const [toast, setToast] = useState<{ msg: string; ok: boolean } | null>(null);
  // Kept out of the toast on purpose: a durability warning that scrolls away
  // after three seconds is a warning nobody acts on. It stays on screen until
  // the next test replaces or clears it.
  const [testWarning, setTestWarning] = useState<string | null>(null);

  // onlineCores is the expected participant count shown as the counter's
  // total while a save round is running. The old count-based refusal
  // (blocking the filesystem backend outright above 1 Core) is gone: the
  // server now proves reachability with a real cross-Core round instead of
  // guessing from a count, so a genuinely shared path across many Cores is
  // no longer refused before it is even tried.
  const [onlineCores, setOnlineCores] = useState(0);
  const [multiCoreWarning, setMultiCoreWarning] = useState<string | null>(null);

  // Saved storage connections for the "use a saved connection" selector.
  const [connections, setConnections] = useState<StorageConnection[]>([]);

  // Snapshot of the last-saved config, used for dirty detection.
  const snapshotRef = useRef<CoreStorageConfig | null>(null);

  const [reach, dispatchReach] = useReducer(reachReducer, initialReachState);
  const reachActive = reach.phase === 'verifying' || reach.phase === 'slow';

  // Bumped at the start of every handleSave call (Save or Retry) and
  // compared after the await: a Retry can start a fresh attempt while an
  // older, stale one is still in flight (that is the whole point of letting
  // Retry fire immediately instead of waiting for the stale request), so the
  // stale attempt's eventual result must be recognized as superseded and
  // discarded rather than clobbering the newer, still-active round's state.
  const saveGenerationRef = useRef(0);

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
      setOnlineCores(res.onlineCores ?? 0);
      setMultiCoreWarning(res.hostPathWarning || null);
      setLoading(false);
    });
  }, []);

  useEffect(() => {
    listStorageConnections().then(res => {
      if (res.success && res.connections) setConnections(res.connections);
    });
  }, []);

  const canSave = canSaveCoreStorage(settings, snapshotRef.current);
  const identityChanged = settings.backend === 's3' && s3IdentityChanged(settings, snapshotRef.current);
  // A saved connection supplies the credentials; the inline s3 fields and the
  // inline Test are then hidden (the connection has its own test on the Storage
  // Connections page).
  const usingConnection = settings.backend === 's3' && (settings.connectionId ?? 0) > 0;
  // Concrete path used in the copyable docker-stack example below. Falls back to
  // the placeholder so the snippet is still valid before anything is typed.
  const exampleStoragePath = settings.path.trim() || '/mnt/dylaris-shared';

  const handleSave = async () => {
    if (!canSave) {
      const reason =
        settings.backend !== 'path'
          ? 'Fill in the bucket, access key and secret before saving.'
          : 'Enter an absolute path and tick the confirmation checkbox before saving.';
      showToast(reason, false);
      return;
    }
    const generation = ++saveGenerationRef.current;
    setSaving(true);
    // onlineCores is only the EXPECTED participant count for the counter; the
    // server decides the real participant set from live heartbeats. Captured
    // once so the synthetic success event below uses this SAME total instead
    // of `reach.total` read from the closure captured before this dispatch
    // takes effect (which would still hold the previous round's total).
    const total = Math.max(onlineCores, 1);
    dispatchReach({ type: 'start', total });
    const res = await saveCoreStorage(settings);
    // A newer Retry can have started (and even finished) while this request
    // was in flight; a stale result must not touch state that belongs to
    // that newer, still-active round. See saveGenerationRef above.
    if (saveGenerationRef.current !== generation) return;
    if (res.success) {
      dispatchReach({
        type: 'progress',
        progress: { confirmed: total, total, done: true, ok: true, results: [] },
      });
      showToast('Core file storage saved.');
      const saved: CoreStorageConfig = { ...settings, s3SecretKey: '', s3SecretSet: settings.s3SecretSet || settings.s3SecretKey !== '' };
      setSettings(saved);
      snapshotRef.current = saved;
    } else {
      dispatchReach({
        type: 'failure',
        message: res.message || 'Save failed.',
        progress: res.round ?? null,
        // The real server round id, when the refusal carried one: lets the
        // reducer's own stale-round guard reject this if a newer round's
        // progress has since taken over roundId (see reachReducer 'failure').
        roundId: res.round?.roundId,
      });
      if (!res.round) showToast(res.message || 'Save failed.', false);
    }
    setSaving(false);
  };

  // Live X/N counter. The save request stays open for the length of the
  // round, so the progress has to arrive out-of-band.
  useEffect(() => {
    if (!reachActive) return;
    return systemEvents.on('storagereach.changed', (event) => {
      const parsed = parseStorageReachEvent(event.payload);
      if (parsed) dispatchReach(parsed);
    });
  }, [reachActive]);

  useEffect(() => {
    if (!reachActive) return;
    const startedAt = Date.now();
    const id = setInterval(() => dispatchReach({ type: 'tick', elapsedMs: Date.now() - startedAt }), 250);
    return () => clearInterval(id);
  }, [reachActive]);

  const handleDiscard = () => { if (snapshotRef.current) setSettings(snapshotRef.current); };

  const dirty = snapshotRef.current !== null && JSON.stringify(settings) !== JSON.stringify(snapshotRef.current);
  useUnsavedChanges({ dirty, save: handleSave, discard: handleDiscard, saving });

  const handleTest = async () => {
    setTesting(true);
    const res = await testCoreStorage(settings);
    if (res.success && res.ok) showToast(res.message || 'Connection successful: write, read and delete all succeeded.');
    else showToast(res.message || 'Connection test failed.', false);
    // Cleared on every test, so a warning never outlives the config that
    // produced it: fixing the mount and re-testing makes it disappear.
    setTestWarning(res.warning || null);
    setTesting(false);
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
                  active
                    ? 'border-(--accent) ring-1 ring-(--accent)/40 bg-(--accent-ghost)'
                    : 'border-(--base-03) hover:border-(--base-05)'
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

      {/* A host path that was already saved before the deployment grew. Kept
          separate from the selector hint above: this one means files are
          being written to a split store right now. */}
      {multiCoreWarning && (
        <div className="alert alert-warning text-xs flex items-start gap-2">
          <AlertTriangle size={14} className="shrink-0 mt-0.5" />
          <span>{multiCoreWarning}</span>
        </div>
      )}

      {/* Path backend */}
      {settings.backend === 'path' && (
        <div className="space-y-4">
          <div className="flex flex-col gap-[5px]">
            <label className="input-label" htmlFor="core-storage-path">Absolute Path</label>
            <input
              id="core-storage-path"
              type="text"
              value={settings.path}
              onChange={e => set('path', e.target.value)}
              placeholder="/mnt/dylaris-shared"
              className="input-mono w-full"
            />
          </div>

          <div className="alert alert-warning text-xs">
            <AlertTriangle size={14} className="shrink-0 mt-0.5" />
            <div className="min-w-0 space-y-2.5">
              <p>
                Must be reachable by <strong>every</strong> Core, and must be a <strong>mounted volume</strong> - a plain
                directory lives inside the container and is erased when it is recreated. A host-local directory therefore
                only works with a single Core. For multiple Cores, mount a shared filesystem (NFS/SMB) at this path on
                every host and bind-mount it into the Core container, or use S3. The commented recipe in{' '}
                <code className="font-mono text-[11px]">docker-stack.yml</code> covers the mount options and the
                bind-propagation setting that a share mounted after container start requires.
              </p>
              <div>
                <p className="mb-1 text-(--base-07)">
                  Example <code className="font-mono text-[11px]">docker-stack.yml</code> volume mapping for this path:
                </p>
                <pre className="p-2.5 rounded-md bg-(--base-02) border border-(--base-04) font-mono text-[11px] leading-relaxed overflow-x-auto text-(--base-08)">{`services:
  core:
    volumes:
      - ${exampleStoragePath}:${exampleStoragePath}`}</pre>
                <p className="mt-1.5 text-(--base-06)">
                  Bind-mount the same host path identically into <strong>every</strong> Core replica, matching the path
                  entered above.
                </p>
              </div>
            </div>
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
          <div className="flex flex-col gap-[5px]">
            <label className="input-label" htmlFor="core-storage-connection">Storage connection</label>
            <select
              id="core-storage-connection"
              value={settings.connectionId ?? 0}
              onChange={e => set('connectionId', Number(e.target.value))}
              className="input-field w-full"
            >
              <option value={0}>Enter credentials manually</option>
              {connections.map(c => <option key={c.id} value={c.id}>{c.name}</option>)}
            </select>
            <p className="text-xs text-(--base-06)">Reuse a saved connection from Settings -&gt; Storage Connections, or enter S3 credentials inline below.</p>
          </div>

          {usingConnection ? (
            <div className="alert alert-info text-xs flex items-start gap-2">
              <Info size={14} className="shrink-0 mt-0.5" />
              <span>Credentials come from the selected connection. Manage or test it on the Storage Connections page.</span>
            </div>
          ) : (
          <>
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
            <div className="flex flex-col gap-[5px]">
              <label className="input-label" htmlFor="core-storage-s3-endpoint">Endpoint URL (blank = AWS)</label>
              <input id="core-storage-s3-endpoint" type="text" value={settings.s3Endpoint} onChange={e => set('s3Endpoint', e.target.value)}
                placeholder="https://s3.example.com" className="input-mono w-full" />
            </div>
            <div className="flex flex-col gap-[5px]">
              <label className="input-label" htmlFor="core-storage-s3-bucket">Bucket</label>
              <input id="core-storage-s3-bucket" type="text" value={settings.s3Bucket} onChange={e => set('s3Bucket', e.target.value)}
                placeholder="dylaris-core" className="input-mono w-full" />
            </div>
            <div className="flex flex-col gap-[5px]">
              <label className="input-label" htmlFor="core-storage-s3-region">Region</label>
              <input id="core-storage-s3-region" type="text" value={settings.s3Region} onChange={e => set('s3Region', e.target.value)}
                placeholder="us-east-1" className="input-mono w-full" />
            </div>
            <div className="flex flex-col gap-[5px]">
              <label className="input-label" htmlFor="core-storage-s3-access-key">Access Key</label>
              <input id="core-storage-s3-access-key" type="text" value={settings.s3AccessKey} onChange={e => set('s3AccessKey', e.target.value)}
                placeholder="AKIA..." className="input-mono w-full" />
            </div>
          </div>

          <div className="flex flex-col gap-[5px]">
            <label className="input-label" htmlFor="core-storage-s3-secret-key">Secret Key</label>
            <input
              id="core-storage-s3-secret-key"
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
              <div className="alert alert-warning text-xs mt-1">
                <AlertTriangle size={14} className="shrink-0 mt-0.5" />
                <span>
                  You changed the endpoint, bucket or access key. Re-enter the secret to save: the backend refuses to pair
                  a stored secret with a different identity (this prevents credentials from being silently re-pointed at a
                  different endpoint).
                </span>
              </div>
            )}
          </div>

          <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
            <div className="flex flex-col gap-[5px]">
              <label className="input-label" htmlFor="core-storage-s3-prefix">Key Prefix (optional)</label>
              <input id="core-storage-s3-prefix" type="text" value={settings.s3Prefix} onChange={e => set('s3Prefix', e.target.value)}
                placeholder="core" className="input-mono w-full" />
            </div>
            <label className="flex items-center gap-2 sm:mt-6 cursor-pointer select-none group">
              <input type="checkbox" checked={settings.s3PathStyle} onChange={e => set('s3PathStyle', e.target.checked)} className="accent-(--accent)" />
              <span className="text-xs text-(--base-08) group-hover:text-(--base-09)">Path-style addressing (required for MinIO)</span>
            </label>
          </div>
          </>
          )}
        </div>
      )}

      {/* Live fleet-wide reachability round, run on every save: the settings
          are persisted only once every online Core proves it can read and
          write the same shared storage. */}
      {reachActive && (
        <div className="alert alert-info text-xs flex flex-col gap-2" role="status" aria-live="polite">
          <div className="flex items-center gap-2">
            <Loader2 size={14} className="animate-spin" />
            <span className="font-mono text-[11px] tracking-[0.04em] text-(--base-09)">
              {reach.confirmed}/{reach.total} cores can read and write this storage
            </span>
          </div>
          {reach.phase === 'slow' && (
            <span className="text-(--base-06)">
              Taking longer than usual (first setup or cross-region mounts can be slower).
            </span>
          )}
        </div>
      )}

      {reach.phase === 'failed' && (
        <div className="alert alert-error text-xs flex flex-col gap-3" role="alert">
          <span className="text-(--base-09)">{reach.message}</span>
          {reach.results.length > 0 && (
            <ul className="flex flex-col gap-1.5">
              {reach.results.map(r => (
                <li key={r.coreId} className="flex flex-col gap-0.5">
                  <div className="flex items-center gap-2">
                    <Badge variant={r.status === 'ok' ? 'success' : 'warning'}>{statusLabel(r.status)}</Badge>
                    <span className="font-mono text-[11px] text-(--base-07)">{r.coreId}</span>
                  </div>
                  {r.status !== 'ok' && (
                    <span className="text-(--base-06)">
                      {statusRemedy(r.status)}
                      {r.missingPeers?.length ? ` Cannot see: ${r.missingPeers.join(', ')}.` : ''}
                      {r.deniedPeers?.length ? ` Cannot write into: ${r.deniedPeers.join(', ')}.` : ''}
                    </span>
                  )}
                </li>
              ))}
            </ul>
          )}
          <button
            type="button"
            className="btn btn-secondary btn-sm self-start"
            onClick={handleSave}
            // Driven by the round's own phase, not `saving`: `saving` stays
            // true for as long as the ORIGINAL, possibly-stale request is
            // still in flight, which is exactly the state this failure panel
            // is shown in on a genuine timeout. Retry must fire immediately,
            // not wait for that stale request to resolve.
            disabled={reachActive}
          >
            Retry
          </button>
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
        {!usingConnection && (
          <button
            type="button"
            onClick={handleTest}
            disabled={testing}
            className="btn btn-secondary disabled:opacity-40 disabled:cursor-not-allowed inline-flex items-center gap-1.5"
          >
            {testing ? <Loader2 size={14} className="animate-spin" /> : <Cable size={14} />}
            {testing ? 'Testing...' : 'Test Connection'}
          </button>
        )}
      </div>

      {testWarning && (
        <div className="alert alert-warning text-xs flex items-start gap-2">
          <CircleAlert size={14} className="shrink-0 mt-0.5" />
          <span>{testWarning}</span>
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
