"use client";

import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useParams } from 'next/navigation';
import { CircleCheck, CircleAlert, Image as ImageIcon, Upload, Trash2, MessageSquare, RotateCcw } from 'lucide-react';
import { useAppData } from '@/lib/AppDataContext';
import { getFileContent, saveFile } from '@/lib/api';
import { uploadFiles } from '@/lib/api/files';
import { API_URL, getAuthHeader } from '@/lib/api/core';
import { parseProperties, serializeProperties } from '@/lib/properties-codec';

// Phase 8 — Display sub-tab. Surgically edits motd= in server.properties so
// the user gets a friendly multi-line editor without touching the rest of the
// file, and uploads server-icon.png to the active sub-server dir.
//
// Why not a dedicated /display backend endpoint: server.properties is owned by
// the FileBrowser API and ServerPropertiesView; reusing those endpoints means
// the Display tab automatically benefits from existing auth + audit + permission
// gates without parallel routes to maintain.

const ICON_PATH_NAME = 'server-icon.png';
// Minecraft chat colour codes — preview the user's input the way the game
// renders it so & codes don't look like literal & in the editor.
const COLOR_MAP: Record<string, string> = {
    '0': '#000000', '1': '#0000AA', '2': '#00AA00', '3': '#00AAAA',
    '4': '#AA0000', '5': '#AA00AA', '6': '#FFAA00', '7': '#AAAAAA',
    '8': '#555555', '9': '#5555FF', a: '#55FF55', b: '#55FFFF',
    c: '#FF5555', d: '#FF55FF', e: '#FFFF55', f: '#FFFFFF',
};

interface ColoredSegment {
    text: string;
    color: string;
    bold?: boolean;
    italic?: boolean;
    underline?: boolean;
    strike?: boolean;
}

function renderMotdPreview(raw: string): ColoredSegment[][] {
    const lines = raw.split('\n').slice(0, 2);
    return lines.map(line => {
        const segments: ColoredSegment[] = [];
        let current: ColoredSegment = { text: '', color: '#FFFFFF' };
        for (let i = 0; i < line.length; i++) {
            const ch = line[i];
            if ((ch === '&' || ch === '§') && i + 1 < line.length) {
                const code = line[i + 1].toLowerCase();
                if (code in COLOR_MAP) {
                    if (current.text) { segments.push(current); }
                    current = { text: '', color: COLOR_MAP[code] };
                    i++;
                    continue;
                }
                if (code === 'l') { if (current.text) segments.push(current); current = { ...current, text: '', bold: true }; i++; continue; }
                if (code === 'o') { if (current.text) segments.push(current); current = { ...current, text: '', italic: true }; i++; continue; }
                if (code === 'n') { if (current.text) segments.push(current); current = { ...current, text: '', underline: true }; i++; continue; }
                if (code === 'm') { if (current.text) segments.push(current); current = { ...current, text: '', strike: true }; i++; continue; }
                if (code === 'r') { if (current.text) segments.push(current); current = { text: '', color: '#FFFFFF' }; i++; continue; }
            }
            current.text += ch;
        }
        if (current.text) segments.push(current);
        return segments;
    });
}

export default function ServerConfigDisplayPage() {
    const params = useParams();
    const { servers } = useAppData();
    const serverId = Number(params?.id);
    const server = servers.find(s => s.id === serverId);
    const activeSubServer = server?.activeSubServer || '';
    const serverUuid = server?.uuid || '';
    const propertiesPath = activeSubServer ? `${activeSubServer}/server.properties` : '';

    const [propertiesLoaded, setPropertiesLoaded] = useState(false);
    const [propertiesText, setPropertiesText] = useState('');
    const [motd, setMotd] = useState('');
    const [motdInitial, setMotdInitial] = useState('');
    const [savingMotd, setSavingMotd] = useState(false);
    const [iconBust, setIconBust] = useState(0);
    const [uploadingIcon, setUploadingIcon] = useState(false);
    const [toast, setToast] = useState<{ msg: string; ok: boolean } | null>(null);
    const fileInputRef = useRef<HTMLInputElement>(null);

    const showToast = useCallback((msg: string, ok = true) => {
        setToast({ msg, ok });
        setTimeout(() => setToast(null), 3500);
    }, []);

    useEffect(() => {
        if (!propertiesPath || !serverUuid) return;
        let cancelled = false;
        (async () => {
            const res = await getFileContent(propertiesPath, serverUuid);
            if (cancelled) return;
            if (res.success) {
                setPropertiesText(res.content || '');
                const parsed = parseProperties(res.content || '');
                // parseProperties already decodes \\n → \n on read; serializeProperties
                // re-escapes on write. We just pass the value through unchanged.
                const decoded = parsed.values['motd'] || '';
                setMotd(decoded);
                setMotdInitial(decoded);
            } else {
                // empty/missing file is fine — first boot
                setMotd('');
                setMotdInitial('');
            }
            setPropertiesLoaded(true);
        })();
        return () => { cancelled = true; };
    }, [propertiesPath, serverUuid]);

    const motdDirty = motd !== motdInitial;
    const previewLines = useMemo(() => renderMotdPreview(motd || 'A Minecraft Server'), [motd]);

    const handleSaveMotd = useCallback(async () => {
        if (!propertiesPath || !serverUuid) return;
        setSavingMotd(true);
        // Surgically rewrite only the motd line — preserves blank lines,
        // comments, key order. The codec re-escapes \n in the value.
        const parsed = parseProperties(propertiesText || '');
        const next = serializeProperties(parsed, { motd });
        const res = await saveFile(propertiesPath, next, serverUuid);
        if (res.success) {
            setPropertiesText(next);
            setMotdInitial(motd);
            showToast('MOTD saved. Restart the server to apply.', true);
        } else {
            showToast(res.message || 'Failed to save MOTD', false);
        }
        setSavingMotd(false);
    }, [propertiesPath, serverUuid, propertiesText, motd, showToast]);

    const handleIconUpload = useCallback(async (file: File) => {
        if (!activeSubServer || !serverUuid) return;
        if (file.type !== 'image/png') { showToast('server-icon must be a PNG file', false); return; }
        if (file.size > 1 * 1024 * 1024) { showToast('server-icon must be < 1MB', false); return; }
        // Validate dimensions client-side. Minecraft accepts 64x64 only;
        // anything else is rendered as a blank slot in the multiplayer list.
        const bitmap = await createImageBitmap(file).catch(() => null);
        if (!bitmap) { showToast('Could not decode PNG', false); return; }
        if (bitmap.width !== 64 || bitmap.height !== 64) {
            showToast(`Icon must be exactly 64x64 px (got ${bitmap.width}x${bitmap.height})`, false);
            return;
        }

        setUploadingIcon(true);
        // uploadFiles takes a FileList — wrap our single file in a DataTransfer.
        const dt = new DataTransfer();
        // Rename to server-icon.png so the in-game client picks it up by
        // convention, regardless of what the user named the source file.
        dt.items.add(new File([file], ICON_PATH_NAME, { type: 'image/png' }));
        const res = await uploadFiles(activeSubServer, dt.files, () => { /* progress unused */ }, 'overwrite');
        setUploadingIcon(false);
        if (res.success) {
            setIconBust(b => b + 1);
            showToast('Icon uploaded. Restart the server to apply.', true);
        } else {
            showToast(res.message || 'Upload failed', false);
        }
    }, [activeSubServer, serverUuid, showToast]);

    const handleDeleteIcon = useCallback(async () => {
        if (!activeSubServer || !serverUuid) return;
        try {
            const res = await fetch(`${API_URL}/files/delete`, {
                method: 'POST',
                headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
                body: JSON.stringify({ path: `${activeSubServer}/${ICON_PATH_NAME}`, server_uuid: serverUuid }),
            });
            const data = await res.json();
            if (res.ok && data.success !== false) {
                setIconBust(b => b + 1);
                showToast('Icon removed.', true);
            } else {
                showToast(data.message || 'Delete failed', false);
            }
        } catch {
            showToast('Network error', false);
        }
    }, [activeSubServer, serverUuid, showToast]);

    const iconUrl = useMemo(() => {
        if (!activeSubServer || !serverUuid) return '';
        return `${API_URL}/files/download?path=${encodeURIComponent(`${activeSubServer}/${ICON_PATH_NAME}`)}&server_uuid=${encodeURIComponent(serverUuid)}&_cb=${iconBust}`;
    }, [activeSubServer, serverUuid, iconBust]);

    if (!server) return null;
    if (!activeSubServer) {
        return (
            <div className="max-w-2xl">
                <div className="card p-5 text-sm text-(--base-07)">
                    Install or switch to a sub-server in Setup before editing display options.
                </div>
            </div>
        );
    }

    return (
        <div className="max-w-3xl space-y-6">
            {/* MOTD */}
            <section className="card p-5 space-y-4">
                <header className="flex items-start gap-3">
                    <div className="w-9 h-9 rounded-md bg-(--accent-ghost) flex items-center justify-center shrink-0">
                        <MessageSquare size={16} className="text-(--accent-light)" />
                    </div>
                    <div className="min-w-0 flex-1">
                        <h2 className="text-base font-display font-semibold text-(--base-09)">MOTD</h2>
                        <p className="text-xs text-(--base-06) mt-0.5">
                            Up to 2 lines, supports Minecraft colour codes (<code className="font-mono">&amp;a</code>, <code className="font-mono">&amp;l</code>, etc.).
                            Requires a server restart to take effect.
                        </p>
                    </div>
                </header>

                <textarea
                    value={motd}
                    onChange={e => setMotd(e.target.value)}
                    placeholder="A Minecraft Server"
                    rows={2}
                    maxLength={120}
                    disabled={!propertiesLoaded}
                    className="input-field font-mono text-sm resize-none w-full"
                />

                {/* Preview pane — emulates the multiplayer server-list MOTD slot */}
                <div>
                    <div className="mono-label mb-1.5">Preview</div>
                    <div className="bg-[#1a1a1a] border border-(--base-04) rounded-md p-3 font-mono text-sm leading-tight space-y-0.5">
                        {previewLines.map((segments, i) => (
                            <div key={i}>
                                {segments.map((seg, j) => (
                                    <span
                                        key={j}
                                        style={{
                                            color: seg.color,
                                            fontWeight: seg.bold ? 700 : 400,
                                            fontStyle: seg.italic ? 'italic' : 'normal',
                                            textDecoration: [seg.underline && 'underline', seg.strike && 'line-through'].filter(Boolean).join(' ') || 'none',
                                        }}
                                    >
                                        {seg.text || ' '}
                                    </span>
                                ))}
                            </div>
                        ))}
                    </div>
                </div>

                <div className="flex items-center gap-2 justify-end">
                    {motdDirty && (
                        <button
                            type="button"
                            onClick={() => setMotd(motdInitial)}
                            className="btn btn-secondary btn-sm"
                            disabled={savingMotd}
                        >
                            <RotateCcw size={13} />
                            Revert
                        </button>
                    )}
                    <button
                        type="button"
                        onClick={handleSaveMotd}
                        className="btn btn-primary btn-sm"
                        disabled={!motdDirty || savingMotd}
                    >
                        {savingMotd ? 'Saving…' : 'Save MOTD'}
                    </button>
                </div>
            </section>

            {/* Server Icon */}
            <section className="card p-5 space-y-4">
                <header className="flex items-start gap-3">
                    <div className="w-9 h-9 rounded-md bg-(--accent-ghost) flex items-center justify-center shrink-0">
                        <ImageIcon size={16} className="text-(--accent-light)" />
                    </div>
                    <div className="min-w-0 flex-1">
                        <h2 className="text-base font-display font-semibold text-(--base-09)">Server Icon</h2>
                        <p className="text-xs text-(--base-06) mt-0.5">
                            64×64 PNG, max 1 MB. Stored as <code className="font-mono">{activeSubServer}/server-icon.png</code>.
                            Requires a server restart to take effect.
                        </p>
                    </div>
                </header>

                <div className="flex items-center gap-4">
                    <div className="w-16 h-16 rounded-md border border-(--base-04) bg-(--base-02) flex items-center justify-center overflow-hidden shrink-0">
                        {/* Cache-bust on every upload so the new icon shows immediately */}
                        {/* eslint-disable-next-line @next/next/no-img-element */}
                        <img
                            src={iconUrl}
                            alt="server icon"
                            className="w-full h-full object-contain"
                            onError={(e) => { (e.target as HTMLImageElement).style.display = 'none'; }}
                            style={{ imageRendering: 'pixelated' }}
                        />
                    </div>
                    <div className="flex items-center gap-2">
                        <input
                            ref={fileInputRef}
                            type="file"
                            accept="image/png"
                            className="hidden"
                            onChange={e => {
                                const f = e.target.files?.[0];
                                if (f) handleIconUpload(f);
                                e.target.value = '';
                            }}
                        />
                        <button
                            type="button"
                            onClick={() => fileInputRef.current?.click()}
                            className="btn btn-secondary btn-sm"
                            disabled={uploadingIcon}
                        >
                            <Upload size={13} />
                            {uploadingIcon ? 'Uploading…' : 'Upload PNG'}
                        </button>
                        <button
                            type="button"
                            onClick={handleDeleteIcon}
                            className="btn btn-secondary btn-sm"
                            disabled={uploadingIcon}
                        >
                            <Trash2 size={13} className="text-(--error)" />
                            Remove
                        </button>
                    </div>
                </div>
            </section>

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
