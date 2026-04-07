"use client";

import React from 'react';
import { PlayCircle, Server as ServerIcon, Plus, RefreshCw } from 'lucide-react';

interface SubServerSidebarProps {
    subServers: string[];
    activeSubServer?: string;
    switchTarget: string | null;
    onSwitchSelect: (name: string | null) => void;
    onSwitchConfirm: () => void;
    onAddNew: () => void;
    submitting: boolean;
    disabled?: boolean;
    maxSubServers?: number;
}

export default function SubServerSidebar({
    subServers, activeSubServer, switchTarget,
    onSwitchSelect, onSwitchConfirm, onAddNew,
    submitting, disabled, maxSubServers,
}: SubServerSidebarProps) {
    const limitReached = maxSubServers != null && maxSubServers > 0 && subServers.length >= maxSubServers;
    return (
        <div className="w-60 shrink-0 card flex flex-col overflow-hidden">
            {/* Header */}
            <div className="px-3 py-3 border-b border-(--base-03) flex items-center justify-between">
                <p className="input-label mb-0">Servers</p>
                <button
                    type="button"
                    onClick={onAddNew}
                    disabled={disabled || limitReached}
                    className={`p-1 rounded-sm transition-all ${
                        limitReached
                            ? 'text-(--base-05) cursor-not-allowed'
                            : 'text-(--accent-light) hover:bg-(--accent-ghost)'
                    }`}
                    title={limitReached ? `Limit reached (${maxSubServers})` : 'Add sub-server'}
                >
                    <Plus size={16} />
                </button>
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
            </div>

            {/* Switch confirmation */}
            {switchTarget && switchTarget !== activeSubServer && (
                <div className="p-2 border-t border-(--base-03) space-y-1">
                    <p className="text-xs text-(--warning-light) font-medium px-1 truncate">
                        Switch to <span className="font-mono">{switchTarget}</span>?
                    </p>
                    <div className="flex gap-1">
                        <button type="button" onClick={onSwitchConfirm} disabled={submitting}
                            className="btn btn-primary flex-1 py-1.5 text-xs bg-(--warning) border-(--warning)">
                            {submitting ? <RefreshCw size={12} className="animate-spin" /> : 'Confirm'}
                        </button>
                        <button type="button" onClick={() => onSwitchSelect(null)}
                            className="btn btn-secondary flex-1 py-1.5 text-xs">Cancel</button>
                    </div>
                </div>
            )}
        </div>
    );
}
