"use client";

import React, { useCallback, useEffect, useState } from 'react';
import { useParams } from 'next/navigation';
import {
    LayoutGrid, Plus, Pencil, Trash2, Play, Square, ExternalLink,
    AlertTriangle, CircleCheck, CircleAlert, X,
} from 'lucide-react';
import { useAppData } from '@/lib/AppDataContext';
import { systemEvents } from '@/lib/systemEvents';
import {
    listServerTabs, createServerTab, updateServerTab, deleteServerTab,
    type ServerTab, type ServerTabInput,
} from '@/lib/api/serverTabs';
import { DynamicIcon } from '@/lib/icons';
import { SkeletonList } from '@/components/Skeleton';

// Phase 13 — Custom Tabs management. Each tab points at a URL; in the
// Server Detail nav strip the tab renders either as an iframe inside the
// panel (openInPanel=true) or as a popout link (false). Common use:
// minimap viewers (squaremap, BlueMap), per-server status pages, custom
// plugin dashboards.

const ICON_OPTIONS = [
    'layout-grid', 'map', 'globe', 'package', 'monitor', 'compass',
    'gauge', 'activity', 'users', 'shield', 'wrench', 'server',
];

interface EditingTab extends Partial<ServerTab> {
    isNew?: boolean;
}

export default function ServerConfigTabsPage() {
    const params = useParams();
    const { servers } = useAppData();
    const serverId = Number(params?.id);
    const server = servers.find(s => s.id === serverId);

    const [tabs, setTabs] = useState<ServerTab[]>([]);
    const [loading, setLoading] = useState(true);
    const [editing, setEditing] = useState<EditingTab | null>(null);
    const [deletePrompt, setDeletePrompt] = useState<ServerTab | null>(null);
    const [toast, setToast] = useState<{ msg: string; ok: boolean } | null>(null);

    const showToast = useCallback((msg: string, ok = true) => {
        setToast({ msg, ok });
        setTimeout(() => setToast(null), 3500);
    }, []);

    const refresh = useCallback(async () => {
        if (!serverId) return;
        const list = await listServerTabs(serverId);
        setTabs(list);
        setLoading(false);
    }, [serverId]);

    useEffect(() => { refresh(); }, [refresh]);

    useEffect(() => {
        const unsub = systemEvents.on('server_tabs.changed', (evt) => {
            const sid = (evt.payload as any)?.serverId;
            if (sid === undefined || sid === serverId) refresh();
        });
        return () => { unsub(); };
    }, [serverId, refresh]);

    const openCreate = () => setEditing({
        isNew: true,
        name: '',
        url: '',
        icon: 'layout-grid',
        enabled: true,
        openInPanel: true,
        position: tabs.length,
    });

    const openEdit = (t: ServerTab) => setEditing({ ...t });

    const handleSave = async () => {
        if (!editing) return;
        const name = (editing.name || '').trim();
        const url = (editing.url || '').trim();
        if (!name) { showToast('Name required', false); return; }
        if (!url) { showToast('URL required', false); return; }
        try { new URL(url); } catch { showToast('Invalid URL', false); return; }

        const payload: ServerTabInput = {
            name,
            url,
            icon: editing.icon || 'layout-grid',
            enabled: editing.enabled ?? true,
            openInPanel: editing.openInPanel ?? true,
            position: editing.position ?? tabs.length,
        };
        const res = editing.isNew
            ? await createServerTab(serverId, payload)
            : await updateServerTab(serverId, editing.id!, payload);
        if (res.success) {
            setEditing(null);
            showToast(editing.isNew ? 'Tab created.' : 'Tab saved.', true);
            refresh();
        } else {
            showToast(res.message || 'Save failed', false);
        }
    };

    const handleToggleEnabled = async (t: ServerTab) => {
        const res = await updateServerTab(serverId, t.id, { enabled: !t.enabled });
        if (res.success) refresh(); else showToast(res.message || 'Toggle failed', false);
    };

    const handleDelete = async () => {
        if (!deletePrompt) return;
        const res = await deleteServerTab(serverId, deletePrompt.id);
        if (res.success) {
            setDeletePrompt(null);
            showToast('Tab deleted.', true);
            refresh();
        } else {
            showToast(res.message || 'Delete failed', false);
        }
    };

    if (!server) return null;

    return (
        <div className="max-w-3xl space-y-4">
            <header className="flex items-center gap-3 justify-between">
                <div className="flex items-start gap-3">
                    <div className="w-9 h-9 rounded-md bg-(--accent-ghost) flex items-center justify-center shrink-0">
                        <LayoutGrid size={16} className="text-(--accent-light)" />
                    </div>
                    <div>
                        <h2 className="text-base font-display font-semibold text-(--base-09)">Custom Tabs</h2>
                        <p className="text-xs text-(--base-06) mt-0.5">
                            Add extra tabs to this server&apos;s Server Detail nav. Each tab
                            renders a URL — useful for minimap viewers (squaremap, BlueMap),
                            per-server status pages, custom plugin dashboards.
                        </p>
                    </div>
                </div>
                <button onClick={openCreate} className="btn btn-primary btn-sm">
                    <Plus size={13} />
                    New Tab
                </button>
            </header>

            <div className="card p-3 text-xs text-(--base-07) flex items-start gap-2">
                <AlertTriangle size={12} className="text-(--warning-light) shrink-0 mt-0.5" />
                <div>
                    V1 expects the URL to be reachable from the user&apos;s browser
                    (your minimap mod listens on a host-published port, or you have
                    Nginx in front). Automatic reverse-proxy via Dylaris Gateway
                    is on the roadmap.
                </div>
            </div>

            {loading ? (
                <SkeletonList rows={3} />
            ) : tabs.length === 0 ? (
                <div className="card p-8 flex flex-col items-center text-center gap-2">
                    <LayoutGrid size={28} className="text-(--base-05)" />
                    <p className="text-sm text-(--base-07)">No custom tabs yet.</p>
                    <p className="text-xs text-(--base-06)">
                        Common: a Map tab pointing at <code className="font-mono">http://your-host:8100</code>.
                    </p>
                </div>
            ) : (
                <div className="space-y-2">
                    {tabs.map(t => (
                        <article key={t.id} className="card p-3 flex items-center gap-3">
                            <div className={`w-9 h-9 rounded-md flex items-center justify-center shrink-0 ${
                                t.enabled ? 'bg-(--accent-ghost) text-(--accent-light)' : 'bg-(--base-03) text-(--base-06)'
                            }`}>
                                <DynamicIcon name={t.icon} size={16} />
                            </div>
                            <div className="min-w-0 flex-1">
                                <div className="flex items-center gap-2 flex-wrap">
                                    <span className="font-medium text-sm text-(--base-09)">{t.name}</span>
                                    {!t.enabled && (
                                        <span className="mono-label bg-(--base-03) px-1.5 rounded-sm text-(--base-06)">disabled</span>
                                    )}
                                    <span className="mono-label bg-(--base-03) px-1.5 rounded-sm text-(--base-07)">
                                        {t.openInPanel ? 'iframe' : 'popout'}
                                    </span>
                                </div>
                                <a
                                    href={t.url}
                                    target="_blank"
                                    rel="noopener noreferrer"
                                    className="text-xs text-(--accent-light) font-mono truncate inline-flex items-center gap-1 mt-0.5"
                                >
                                    {t.url}
                                    <ExternalLink size={9} />
                                </a>
                            </div>
                            <div className="flex items-center gap-1 shrink-0">
                                <button onClick={() => handleToggleEnabled(t)} className="btn btn-secondary btn-sm">
                                    {t.enabled ? <Square size={12} /> : <Play size={12} />}
                                </button>
                                <button onClick={() => openEdit(t)} className="btn btn-secondary btn-sm">
                                    <Pencil size={12} />
                                </button>
                                <button onClick={() => setDeletePrompt(t)} className="btn btn-secondary btn-sm">
                                    <Trash2 size={12} className="text-(--error)" />
                                </button>
                            </div>
                        </article>
                    ))}
                </div>
            )}

            {/* Editor */}
            {editing && (
                <div className="modal-overlay animate-fade-in" onClick={() => setEditing(null)}>
                    <div className="modal-panel max-w-lg" onClick={e => e.stopPropagation()}>
                        <div className="modal-header">
                            <h3 className="modal-title flex items-center gap-2">
                                <LayoutGrid size={16} />
                                {editing.isNew ? 'New tab' : 'Edit tab'}
                            </h3>
                            <button onClick={() => setEditing(null)} className="text-(--base-06)"><X size={16} /></button>
                        </div>
                        <div className="modal-body space-y-4">
                            <div>
                                <label className="input-label">Name</label>
                                <input
                                    type="text"
                                    value={editing.name || ''}
                                    onChange={e => setEditing({ ...editing, name: e.target.value })}
                                    className="input-field w-full"
                                    placeholder="Map"
                                    maxLength={64}
                                />
                            </div>
                            <div>
                                <label className="input-label">URL</label>
                                <input
                                    type="url"
                                    value={editing.url || ''}
                                    onChange={e => setEditing({ ...editing, url: e.target.value })}
                                    className="input-field input-mono w-full"
                                    placeholder="https://map.example.com"
                                />
                                <p className="text-xs text-(--base-06) mt-1">
                                    Must be reachable from the user&apos;s browser. http and https supported.
                                </p>
                            </div>
                            <div>
                                <label className="input-label">Icon</label>
                                <div className="grid grid-cols-6 gap-1 mt-1">
                                    {ICON_OPTIONS.map(icon => (
                                        <button
                                            key={icon}
                                            type="button"
                                            onClick={() => setEditing({ ...editing, icon })}
                                            className={`w-10 h-10 rounded-md flex items-center justify-center transition-colors ${
                                                editing.icon === icon
                                                    ? 'bg-(--accent-ghost) text-(--accent-light) border border-(--accent)'
                                                    : 'bg-(--base-02) border border-(--base-04) text-(--base-07) hover:bg-(--base-03)'
                                            }`}
                                        >
                                            <DynamicIcon name={icon} size={14} />
                                        </button>
                                    ))}
                                </div>
                            </div>
                            <div className="flex items-center justify-between border-t border-(--base-03) pt-3">
                                <div>
                                    <div className="text-sm font-medium text-(--base-09)">Open in panel</div>
                                    <p className="text-xs text-(--base-06)">
                                        Off → clicking the tab opens the URL in a new browser window.
                                    </p>
                                </div>
                                <button
                                    type="button"
                                    role="switch"
                                    aria-checked={editing.openInPanel ?? true}
                                    onClick={() => setEditing({ ...editing, openInPanel: !(editing.openInPanel ?? true) })}
                                    className={`toggle-track ${(editing.openInPanel ?? true) ? 'toggle-track-on' : 'toggle-track-off'}`}
                                >
                                    <span className={`toggle-knob ${(editing.openInPanel ?? true) ? 'toggle-knob-on' : 'toggle-knob-off'}`} />
                                </button>
                            </div>
                            <div className="flex items-center justify-between border-t border-(--base-03) pt-3">
                                <div>
                                    <div className="text-sm font-medium text-(--base-09)">Enabled</div>
                                    <p className="text-xs text-(--base-06)">Disabled tabs disappear from the nav strip.</p>
                                </div>
                                <button
                                    type="button"
                                    role="switch"
                                    aria-checked={editing.enabled ?? true}
                                    onClick={() => setEditing({ ...editing, enabled: !(editing.enabled ?? true) })}
                                    className={`toggle-track ${(editing.enabled ?? true) ? 'toggle-track-on' : 'toggle-track-off'}`}
                                >
                                    <span className={`toggle-knob ${(editing.enabled ?? true) ? 'toggle-knob-on' : 'toggle-knob-off'}`} />
                                </button>
                            </div>
                        </div>
                        <div className="modal-footer">
                            <button onClick={() => setEditing(null)} className="btn btn-secondary">Cancel</button>
                            <button onClick={handleSave} className="btn btn-primary">
                                {editing.isNew ? 'Create' : 'Save'}
                            </button>
                        </div>
                    </div>
                </div>
            )}

            {/* Delete confirm */}
            {deletePrompt && (
                <div className="modal-overlay animate-fade-in" onClick={() => setDeletePrompt(null)}>
                    <div className="modal-panel max-w-sm" onClick={e => e.stopPropagation()}>
                        <div className="modal-header">
                            <h3 className="modal-title flex items-center gap-2 text-(--error-light)">
                                <AlertTriangle size={18} />
                                Delete tab?
                            </h3>
                        </div>
                        <div className="modal-body">
                            <p className="text-sm text-(--base-07)">
                                Remove <span className="font-semibold text-(--base-09)">{deletePrompt.name}</span> from the nav.
                            </p>
                        </div>
                        <div className="modal-footer">
                            <button onClick={() => setDeletePrompt(null)} className="btn btn-secondary">Cancel</button>
                            <button onClick={handleDelete} className="btn btn-danger">Delete</button>
                        </div>
                    </div>
                </div>
            )}

            {toast && (
                <div className="toast-container">
                    <div className="toast">
                        <div className={`toast-bar ${toast.ok ? 'bg-(--success-light)' : 'bg-(--error-light)'}`}></div>
                        {toast.ok ? <CircleCheck size={14} /> : <CircleAlert size={14} />}
                        <span className="text-sm text-(--base-09)">{toast.msg}</span>
                    </div>
                </div>
            )}
        </div>
    );
}
