"use client";

import React, { useState } from 'react';
import { DownloadCloud, X, Loader2, Package } from 'lucide-react';
import { importSolderPreview, importSolder, type SolderImportPack } from '@/lib/api/packsImport';

// Two-step import: (1) enter a Solder API base URL and load its pack list,
// (2) pick one pack and import it. onImported fires after a successful import
// so the parent can refresh + toast.
export default function ImportSolderDialog({
    onClose,
    onImported,
}: {
    onClose: () => void;
    onImported: (packId: number, imported: number, builds: number) => void;
}) {
    const [url, setUrl] = useState('');
    const [packs, setPacks] = useState<SolderImportPack[] | null>(null);
    const [selected, setSelected] = useState<string>('');
    const [loading, setLoading] = useState(false);
    const [importing, setImporting] = useState(false);
    const [error, setError] = useState<string | null>(null);

    const loadList = async () => {
        setError(null);
        if (!url.trim()) { setError('Enter the Solder API base URL.'); return; }
        setLoading(true);
        const res = await importSolderPreview(url.trim());
        setLoading(false);
        if (res.success && Array.isArray(res.packs)) {
            setPacks(res.packs);
            setSelected(res.packs[0]?.slug ?? '');
            if (res.packs.length === 0) setError('This instance advertises no modpacks.');
        } else {
            setError(res.message || 'Could not read that Solder instance.');
        }
    };

    const runImport = async () => {
        setError(null);
        if (!selected) { setError('Pick a modpack to import.'); return; }
        setImporting(true);
        const res = await importSolder(url.trim(), selected);
        setImporting(false);
        if (res.success && typeof res.packId === 'number') {
            onImported(res.packId, res.imported ?? 0, res.builds ?? 0);
        } else {
            setError(res.message || 'Import failed.');
        }
    };

    return (
        <div className="modal-overlay animate-fade-in" onClick={onClose}>
            <div className="modal-panel max-w-lg" onClick={e => e.stopPropagation()}>
                <div className="modal-header">
                    <h3 className="modal-title flex items-center gap-2">
                        <DownloadCloud size={16} />
                        Import from Solder
                    </h3>
                    <button onClick={onClose} className="text-(--base-06)"><X size={16} /></button>
                </div>
                <div className="modal-body space-y-4">
                    <div>
                        <label className="input-label">Solder API base URL</label>
                        <div className="flex gap-2">
                            <input
                                type="text"
                                value={url}
                                onChange={e => { setUrl(e.target.value); setPacks(null); }}
                                className="input-field input-mono w-full"
                                placeholder="https://solder.example.com/api"
                            />
                            <button onClick={loadList} className="btn btn-secondary btn-sm shrink-0" disabled={loading}>
                                {loading ? <Loader2 size={13} className="animate-spin" /> : 'Load'}
                            </button>
                        </div>
                        <p className="text-xs text-(--base-06) mt-1">
                            Must be a public http/https URL. Private or loopback addresses are rejected.
                        </p>
                    </div>

                    {packs && packs.length > 0 && (
                        <div>
                            <label className="input-label">Modpack to import</label>
                            <select
                                value={selected}
                                onChange={e => setSelected(e.target.value)}
                                className="input-field w-full"
                            >
                                {packs.map(p => (
                                    <option key={p.slug} value={p.slug}>{p.name} ({p.slug})</option>
                                ))}
                            </select>
                            <p className="text-xs text-(--base-06) mt-1 flex items-center gap-1">
                                <Package size={11} className="text-(--accent-light)" />
                                Imports all published builds as drafts you can edit and publish.
                            </p>
                        </div>
                    )}

                    {error && <p className="text-xs text-(--error-light)">{error}</p>}
                </div>
                <div className="modal-footer">
                    <button onClick={onClose} className="btn btn-secondary">Cancel</button>
                    <button
                        onClick={runImport}
                        className="btn btn-primary disabled:opacity-40 disabled:cursor-not-allowed"
                        disabled={!selected || importing || loading}
                    >
                        {importing ? <Loader2 size={14} className="animate-spin" /> : 'Import'}
                    </button>
                </div>
            </div>
        </div>
    );
}
