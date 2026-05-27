"use client";

import React, { useEffect, useState } from 'react';
import { Server as ServerIcon, Plus, RefreshCw, Pencil, Trash2 } from 'lucide-react';

interface SubServerSidebarProps {
    subServers: string[];
    activeSubServer?: string;
    switchTarget: string | null;
    onSwitchSelect: (name: string | null) => void;
    onSwitchConfirm: () => void;
    onAddNew: () => void;
    onEditSubServer?: (name: string) => void;
    onDeleteSubServer?: (name: string) => void;
    submitting: boolean;
    disabled?: boolean;
    maxSubServers?: number;
}

export default function SubServerSidebar({
    subServers, activeSubServer, switchTarget,
    onSwitchSelect, onSwitchConfirm, onAddNew,
    onEditSubServer, onDeleteSubServer,
    submitting, disabled, maxSubServers,
}: SubServerSidebarProps) {
    const limitReached = maxSubServers != null && maxSubServers > 0 && subServers.length >= maxSubServers;
    // Per-item context menu. Tracks the sub-server whose menu is open and
    // the viewport anchor (in client coords) — we render with position:
    // fixed so the popup escapes the sidebar's own overflow clipping.
    const [ctxMenu, setCtxMenu] = useState<{ name: string; x: number; y: number } | null>(null);

    // Close the menu on any outside click / escape so it doesn't get
    // stranded over the page when the user clicks elsewhere.
    useEffect(() => {
        if (!ctxMenu) return;
        const close = () => setCtxMenu(null);
        const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') close(); };
        window.addEventListener('click', close);
        window.addEventListener('contextmenu', close);
        window.addEventListener('keydown', onKey);
        return () => {
            window.removeEventListener('click', close);
            window.removeEventListener('contextmenu', close);
            window.removeEventListener('keydown', onKey);
        };
    }, [ctxMenu]);
    return (
        <div className="w-60 shrink-0 card flex flex-col overflow-hidden">
            {/* Header — no inline + button anymore; the Add action moved
                to a dedicated violet button at the bottom of the list. */}
            <div className="px-3 py-3 border-b border-(--base-03) flex items-center justify-between">
                <p className="input-label mb-0">Servers</p>
            </div>

            {/* Server list */}
            <div className="flex-1 overflow-y-auto p-2 space-y-1">
                {subServers.map(name => {
                    const isActive = name === activeSubServer;
                    const isSwitch = switchTarget === name;
                    return (
                        <button
                            key={name}
                            type="button"
                            onClick={() => {
                                if (!disabled && !isActive) {
                                    onSwitchSelect(isSwitch ? null : name);
                                }
                            }}
                            onContextMenu={e => {
                                if (!onEditSubServer && !onDeleteSubServer) return;
                                e.preventDefault();
                                setCtxMenu({ name, x: e.clientX, y: e.clientY });
                            }}
                            className={`w-full text-left px-3 py-2 rounded-md text-sm font-mono flex items-center gap-2.5 transition-all border ${
                                isActive
                                    ? 'border-(--success-border) bg-(--success-ghost) text-(--success-light)'
                                    : isSwitch
                                    ? 'border-(--warning-border) bg-(--warning-ghost) text-(--warning-light)'
                                    : 'border-transparent hover:bg-(--base-04) text-(--base-07) hover:text-(--base-09)'
                            }`}
                        >
                            {isActive ? (
                                <span className="w-2 h-2 rounded-full bg-(--success-light) shrink-0" />
                            ) : (
                                <ServerIcon size={14} className="shrink-0" />
                            )}
                            <span className="truncate">{name}</span>
                        </button>
                    );
                })}

                {/* Add-server CTA at the bottom of the list. Sits
                    inside the scroll area so it stays adjacent to the
                    last item, not floating in a separate footer. */}
                <button
                    type="button"
                    onClick={onAddNew}
                    disabled={disabled || limitReached}
                    className={`w-full mt-2 px-3 py-2 rounded-md text-sm font-medium flex items-center justify-center gap-2 transition-all border ${
                        disabled || limitReached
                            ? 'border-(--base-04) text-(--base-05) bg-(--base-02) cursor-not-allowed'
                            : 'border-(--accent-border) bg-(--accent) text-white hover:bg-(--accent-light)'
                    }`}
                    title={limitReached ? `Limit reached (${maxSubServers})` : 'Add sub-server'}
                >
                    <Plus size={14} />
                    <span>Add Server</span>
                </button>
            </div>

            {/* Right-click context menu. Position-fixed so it pierces
                the sidebar's overflow-y-auto clipping; click-outside
                closes via the global listener registered in useEffect. */}
            {ctxMenu && (
                <div
                    className="fixed z-50 min-w-[140px] rounded-md bg-(--base-02) border border-(--base-03) shadow-lg py-1 text-sm"
                    style={{ left: ctxMenu.x, top: ctxMenu.y }}
                    onClick={e => e.stopPropagation()}
                    onContextMenu={e => e.preventDefault()}
                >
                    {onEditSubServer && (
                        <button
                            type="button"
                            onClick={() => { onEditSubServer(ctxMenu.name); setCtxMenu(null); }}
                            className="w-full px-3 py-1.5 flex items-center gap-2 text-(--base-08) hover:bg-(--base-04) hover:text-(--base-09)"
                        >
                            <Pencil size={13} /> Edit
                        </button>
                    )}
                    {onDeleteSubServer && (
                        <button
                            type="button"
                            onClick={() => { onDeleteSubServer(ctxMenu.name); setCtxMenu(null); }}
                            className="w-full px-3 py-1.5 flex items-center gap-2 text-(--error-light) hover:bg-(--error-ghost)"
                        >
                            <Trash2 size={13} /> Delete
                        </button>
                    )}
                </div>
            )}

            {/* Switch confirmation */}
            {switchTarget && switchTarget !== activeSubServer && (
                <div className="p-2 border-t border-(--base-03) space-y-1">
                    <p className="text-xs text-(--warning-light) font-medium px-1 truncate">
                        Switch to <span className="font-mono">{switchTarget}</span>?
                    </p>
                    <div className="flex gap-1">
                        <button type="button" onClick={onSwitchConfirm} disabled={submitting}
                            className="btn btn-primary btn-sm flex-1 bg-(--warning) border-(--warning)">
                            {submitting ? <RefreshCw size={12} className="animate-spin" /> : 'Confirm'}
                        </button>
                        <button type="button" onClick={() => onSwitchSelect(null)}
                            className="btn btn-secondary btn-sm flex-1">Cancel</button>
                    </div>
                </div>
            )}
        </div>
    );
}
