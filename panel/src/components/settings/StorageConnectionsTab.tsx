"use client";

import { useState, useEffect, useCallback } from 'react';
import { Plus, Trash2, Pencil, X, Cloud, Save, Cable } from 'lucide-react';
import {
    StorageConnection,
    StorageConnectionConfig,
    StorageConnectionInput,
    listStorageConnections, createStorageConnection, updateStorageConnection, deleteStorageConnection,
    testStorageConnection, testDraftStorageConnection,
} from '@/lib/api';
import { SkeletonHeader, SkeletonCard } from '@/components/Skeleton';
import { confirmDialog } from '@/components/ui/ConfirmDialog';
import { toast } from '@/components/ui/Toast';
import Switch from '@/components/ui/Switch';
import HelpPanel, { HelpPanelButton, useHelpPanel, type HelpEntry } from '@/components/ui/HelpPanel';

// editing holds a StorageConnection plus a transient secret. The list never
// carries the secret (secretSet only); a save sends secretAccessKey only when
// the operator typed one, so a blank field keeps the stored credential.
type EditingConnection = StorageConnection & { secretAccessKey?: string };

const EMPTY: EditingConnection = {
    id: 0,
    name: '',
    provider: 's3',
    config: { endpoint: '', region: '', bucket: '', forcePathStyle: true, prefix: '' },
    accessKey: '',
    secretAccessKey: '',
    secretSet: false,
};

// The plain-text s3 config fields, in display order (the secret is handled
// separately below).
const S3_FIELDS: { key: 'endpoint' | 'region' | 'bucket' | 'prefix'; label: string; placeholder?: string }[] = [
    { key: 'endpoint', label: 'Endpoint', placeholder: 'https://fsn1.your-objectstorage.com' },
    { key: 'region', label: 'Region', placeholder: 'us-east-1' },
    { key: 'bucket', label: 'Bucket' },
    { key: 'prefix', label: 'Prefix (optional)' },
];

// Six fields, four of whose right answers depend entirely on which provider you
// are pointing at, and no two providers agree. That is a page of explanation
// rather than a tooltip, which is what the slide-out panel is for.
const HELP: HelpEntry[] = [
    {
        field: 'Name',
        body: (
            <>
                Yours to choose, and the only part users of this connection see. Other screens
                reference it by name, so something that says where it is beats something that says
                what it is for: <code>hetzner-fsn1</code> outlives <code>backups</code>.
            </>
        ),
    },
    {
        field: 'Endpoint',
        body: (
            <>
                The S3 API address, with the scheme and no bucket in it.
                <br />
                <strong>Cloudflare R2:</strong> <code>https://&lt;account-id&gt;.r2.cloudflarestorage.com</code>.
                The account id is in the R2 dashboard, top right, and in the S3 API URL R2 shows you.
                <br />
                <strong>Hetzner:</strong> <code>https://fsn1.your-objectstorage.com</code> (or nbg1, hel1).
                <br />
                <strong>AWS S3:</strong> leave it empty and set the region instead.
            </>
        ),
    },
    {
        field: 'Region',
        body: (
            <>
                <strong>R2:</strong> <code>auto</code>. R2 has no regions; the signature still needs a
                value and this is the one it expects.
                <br />
                <strong>Hetzner:</strong> the location in the endpoint, e.g. <code>fsn1</code>.
                <br />
                <strong>AWS:</strong> the real one, e.g. <code>eu-central-1</code>. Getting it wrong
                is a signature failure, not a redirect.
            </>
        ),
    },
    {
        field: 'Bucket',
        body: (
            <>
                An existing bucket. Nothing here creates one, and a bucket that does not exist fails
                the test with a message about the key rather than the bucket, which is a confusing
                twenty minutes.
            </>
        ),
    },
    {
        field: 'Prefix',
        body: (
            <>
                Optional path inside the bucket, e.g. <code>dylaris/</code>. Use it to share a bucket
                with something else. Everything this connection writes lands under it, and changing
                it later does not move what is already there.
            </>
        ),
    },
    {
        field: 'Access key ID and secret access key',
        body: (
            <>
                <strong>R2:</strong> R2 dashboard, Manage API tokens, create an{' '}
                <em>Account API token</em> with Object Read and Write. It shows an Access Key ID and
                a Secret Access Key once — those two, not the token value above them.
                <br />
                <strong>Hetzner:</strong> the S3 credentials from the project&apos;s security page.
                <br />
                The secret is encrypted before it is stored and no screen ever shows it again. Leave
                it blank on an edit to keep the one already stored.
            </>
        ),
    },
    {
        field: 'Force path style',
        body: (
            <>
                Whether the bucket goes in the path (<code>endpoint/bucket/key</code>) or in the
                hostname (<code>bucket.endpoint/key</code>).
                <br />
                <strong>ON</strong> for Hetzner and most self-hosted S3 (MinIO, Ceph).
                <br />
                <strong>OFF</strong> for Cloudflare R2 and AWS S3.
                <br />
                Wrong either way looks like a DNS or a 404 error rather than a setting.
            </>
        ),
    },
];

export default function StorageConnectionsTab() {
    const [connections, setConnections] = useState<StorageConnection[]>([]);
    const [loading, setLoading] = useState(true);
    const [editing, setEditing] = useState<EditingConnection | null>(null);
    const [saving, setSaving] = useState(false);
    const [testingId, setTestingId] = useState<number | null>(null);
    const [draftTesting, setDraftTesting] = useState(false);
    // The verdict stays on screen inside the dialog rather than in a toast: it
    // is the thing being read while the fields next to it are corrected, and a
    // toast that has already faded is no help to that.
    const [draftVerdict, setDraftVerdict] = useState<{ ok: boolean; message: string } | null>(null);
    const help = useHelpPanel();

    const reload = useCallback(async () => {
        setLoading(true);
        const res = await listStorageConnections();
        if (res.success && res.connections) setConnections(res.connections);
        setLoading(false);
    }, []);

    useEffect(() => { reload(); }, [reload]);

    const setConfig = (patch: Partial<StorageConnectionConfig>) => {
        setEditing(prev => (prev ? { ...prev, config: { ...prev.config, ...patch } } : prev));
        // Any edit invalidates the verdict above it. Leaving a green "OK" next
        // to a changed endpoint is worse than showing nothing.
        setDraftVerdict(null);
    };

    const payloadFor = (c: EditingConnection): StorageConnectionInput => {
        const payload: StorageConnectionInput = {
            name: c.name.trim(),
            provider: 's3',
            config: {
                endpoint: c.config.endpoint ?? '',
                region: c.config.region ?? '',
                bucket: c.config.bucket ?? '',
                forcePathStyle: !!c.config.forcePathStyle,
                prefix: c.config.prefix ?? '',
            },
            accessKey: c.accessKey ?? '',
        };
        // Write-only: only send the secret when the operator typed a new one.
        if (c.secretAccessKey) payload.secretAccessKey = c.secretAccessKey;
        return payload;
    };

    const handleSave = async () => {
        if (!editing) return;
        if (!editing.name.trim()) { toast('Name is required.', false); return; }
        const payload = payloadFor(editing);

        setSaving(true);
        const res = editing.id === 0
            ? await createStorageConnection(payload)
            : await updateStorageConnection(editing.id, payload);
        setSaving(false);
        if (res.success) {
            toast('Connection saved.');
            setEditing(null);
            setDraftVerdict(null);
            reload();
        } else {
            // Show what the server said: a duplicate name and a connection that
            // no longer exists are both actionable and both look identical
            // behind a bare "Save failed."
            toast(res.message || 'Save failed.', false);
        }
    };

    // Test what is on screen, before it is committed. Saving first and testing
    // afterwards was the only order available, and a saved-but-wrong connection
    // is a thing other screens can already select.
    const handleDraftTest = async () => {
        if (!editing) return;
        setDraftTesting(true);
        setDraftVerdict(null);
        const res = await testDraftStorageConnection({ ...payloadFor(editing), id: editing.id });
        setDraftTesting(false);
        const ok = !!(res.success && res.ok);
        setDraftVerdict({ ok, message: res.message || (ok ? 'Connection OK' : 'Connection failed') });
    };

    const handleDelete = async (id: number) => {
        if (!(await confirmDialog({ title: 'Delete storage connection', message: 'Delete this storage connection? Features that reference it will fall back to their inline configuration.' }))) return;
        const res = await deleteStorageConnection(id);
        if (res.success) reload();
        else toast(res.message || 'Delete failed.', false);
    };

    const handleTest = async (id: number) => {
        setTestingId(id);
        const res = await testStorageConnection(id);
        setTestingId(null);
        const ok = !!(res.success && res.ok);
        toast(res.message || (ok ? 'Connection OK' : 'Connection failed'), ok);
    };

    if (loading) return (
        <div className="max-w-3xl space-y-6">
            <SkeletonHeader />
            <SkeletonCard height="h-64" />
        </div>
    );

    return (
        <div className="max-w-3xl space-y-6">
            <div>
                <h2 className="h-section mb-1">Storage connections</h2>
                <p className="text-sm text-(--base-07)">Define an S3 connection once, by name, and reuse it across core storage, modpacks and backups. The secret is encrypted at rest and never leaves the server.</p>
            </div>

            <div className="card card-pad">
                <div className="flex items-center justify-between mb-4">
                    <h3 className="text-sm font-display font-semibold text-(--accent-light)">Connections</h3>
                    <button onClick={() => { setEditing({ ...EMPTY }); setDraftVerdict(null); }} className="btn btn-primary btn-sm">
                        <Plus size={12} /> Add connection
                    </button>
                </div>

                {connections.length === 0 ? (
                    <p className="alert alert-info text-xs">No storage connections yet. Add one to reuse the same S3 credentials in several places.</p>
                ) : (
                    <div className="space-y-2">
                        {connections.map(c => (
                            <div key={c.id} className="flex items-center justify-between gap-3 bg-(--base-03) border border-(--base-04) rounded-md px-3 py-2.5">
                                <div className="flex items-center gap-3 min-w-0">
                                    <Cloud size={16} className="text-(--accent-light) shrink-0" />
                                    <div className="min-w-0">
                                        <div className="flex items-center gap-2">
                                            <span className="text-sm font-medium text-(--base-09)">{c.name}</span>
                                            {!c.secretSet && <span className="badge badge-warning">no secret</span>}
                                        </div>
                                        <div className="mono-label">{c.provider}{c.config.bucket ? ` · ${c.config.bucket}` : ''}</div>
                                    </div>
                                </div>
                                <div className="flex items-center gap-1.5">
                                    <button onClick={() => handleTest(c.id)} className="btn btn-secondary btn-sm" disabled={testingId === c.id}>
                                        <Cable size={12} /> {testingId === c.id ? 'Testing…' : 'Test'}
                                    </button>
                                    <button onClick={() => { setEditing({ ...c }); setDraftVerdict(null); }} className="btn btn-secondary btn-sm">
                                        <Pencil size={12} /> Edit
                                    </button>
                                    <button onClick={() => handleDelete(c.id)} className="btn btn-danger btn-sm" aria-label={`Delete ${c.name}`}>
                                        <Trash2 size={12} />
                                    </button>
                                </div>
                            </div>
                        ))}
                    </div>
                )}
            </div>

            {editing && (
                <div className="modal-overlay animate-fade-in" onClick={() => setEditing(null)}>
                    <div
                        className="flex flex-col lg:flex-row items-stretch gap-4 w-full max-w-5xl max-h-[90vh] px-4"
                        onClick={e => e.stopPropagation()}
                    >
                        <div className="modal-panel w-full lg:max-w-lg flex flex-col min-h-0">
                            <div className="modal-header flex items-center justify-between gap-2">
                                <h3 className="modal-title">{editing.id === 0 ? 'New connection' : 'Edit connection'}</h3>
                                <div className="flex items-center gap-1">
                                    <HelpPanelButton open={help.open} onToggle={help.toggle} label="Show field help" />
                                    <button onClick={() => setEditing(null)} aria-label="Close" className="p-1 text-(--base-06) hover:text-(--base-09)">
                                        <X size={16} />
                                    </button>
                                </div>
                            </div>
                            <div className="modal-body space-y-4 overflow-y-auto">
                                <div className="form-group">
                                    <label className="input-label">Name</label>
                                    <input
                                        type="text"
                                        value={editing.name}
                                        onChange={e => { setEditing({ ...editing, name: e.target.value }); }}
                                        className="input-field"
                                        placeholder="hetzner-fsn1"
                                    />
                                </div>

                                {S3_FIELDS.map(f => (
                                    <div key={f.key} className="form-group">
                                        <label className="input-label">{f.label}</label>
                                        <input
                                            type="text"
                                            value={editing.config[f.key] ?? ''}
                                            onChange={e => setConfig({ [f.key]: e.target.value })}
                                            className="input-field font-mono"
                                            placeholder={f.placeholder}
                                        />
                                    </div>
                                ))}

                                <div className="form-group">
                                    <label className="input-label">Access key ID</label>
                                    <input
                                        type="text"
                                        value={editing.accessKey}
                                        onChange={e => { setEditing({ ...editing, accessKey: e.target.value }); setDraftVerdict(null); }}
                                        className="input-field font-mono"
                                        autoComplete="off"
                                    />
                                </div>

                                <div className="form-group">
                                    <label className="input-label">Secret access key</label>
                                    <input
                                        type="password"
                                        value={editing.secretAccessKey ?? ''}
                                        onChange={e => { setEditing({ ...editing, secretAccessKey: e.target.value }); setDraftVerdict(null); }}
                                        className="input-field font-mono"
                                        autoComplete="new-password"
                                        placeholder={editing.secretSet ? 'Leave blank to keep the stored secret' : ''}
                                    />
                                </div>

                                <div className="flex items-center justify-between gap-4">
                                    <div>
                                        <label className="input-label mb-0">Force path style</label>
                                        <p className="text-xs text-(--base-06) mt-0.5">ON for Hetzner and MinIO. OFF for Cloudflare R2 and AWS S3.</p>
                                    </div>
                                    <Switch
                                        checked={!!editing.config.forcePathStyle}
                                        onChange={v => setConfig({ forcePathStyle: v })}
                                        ariaLabel="Force path style"
                                    />
                                </div>

                                {draftVerdict && (
                                    <div className={`text-xs px-3 py-2 rounded-md ${
                                        draftVerdict.ok
                                            ? 'text-(--success-light) bg-(--success-ghost)'
                                            : 'text-(--error-light) bg-(--error-ghost)'
                                    }`}>
                                        {draftVerdict.message}
                                    </div>
                                )}
                            </div>
                            <div className="modal-footer">
                                <button
                                    onClick={handleDraftTest}
                                    className="btn btn-secondary mr-auto"
                                    disabled={draftTesting || saving}
                                >
                                    <Cable size={13} /> {draftTesting ? 'Testing…' : 'Test'}
                                </button>
                                <button onClick={() => setEditing(null)} className="btn btn-secondary">Cancel</button>
                                <button onClick={handleSave} className="btn btn-primary" disabled={saving}>
                                    <Save size={13} /> {saving ? 'Saving…' : 'Save'}
                                </button>
                            </div>
                        </div>

                        <HelpPanel
                            open={help.open}
                            onClose={help.close}
                            title="What goes where"
                            entries={HELP}
                            footer={
                                <>
                                    The test writes a small file, reads it back, compares it and deletes it
                                    again. Anything less would pass on a bucket you can write to and not read
                                    from, which is a backup you cannot restore.
                                </>
                            }
                        />
                    </div>
                </div>
            )}
        </div>
    );
}
