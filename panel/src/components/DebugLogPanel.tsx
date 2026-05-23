"use client";

import React, { useEffect, useRef, useState } from 'react';
import { Trash2, Copy, ChevronDown, ChevronUp, Bug } from 'lucide-react';
import { subscribeDevLog, clearDevLog, DevLogEntry } from '@/lib/devLog';

interface DebugLogPanelProps {
    // Only entries whose category starts with `filter` are shown. Pass
    // "beam." to scope a panel to Beam-related steps.
    filter?: string;
    // Initial collapsed state — panels mount collapsed by default so they
    // don't shove existing content offscreen.
    defaultOpen?: boolean;
    // Title shown in the header chip.
    title?: string;
}

const LEVEL_COLOR: Record<DevLogEntry['level'], string> = {
    info: 'text-(--base-08)',
    warn: 'text-(--warning-light)',
    error: 'text-(--error-light)',
};

function formatTime(ts: number): string {
    const d = new Date(ts);
    const hh = String(d.getHours()).padStart(2, '0');
    const mm = String(d.getMinutes()).padStart(2, '0');
    const ss = String(d.getSeconds()).padStart(2, '0');
    const ms = String(d.getMilliseconds()).padStart(3, '0');
    return `${hh}:${mm}:${ss}.${ms}`;
}

function formatData(data: unknown): string {
    if (data === undefined || data === null) return '';
    if (typeof data === 'string') return data;
    try {
        return JSON.stringify(data);
    } catch {
        return String(data);
    }
}

export default function DebugLogPanel({
    filter,
    defaultOpen = true,
    title = 'Debug Log',
}: DebugLogPanelProps) {
    const [entries, setEntries] = useState<DevLogEntry[]>([]);
    const [open, setOpen] = useState(defaultOpen);
    const [copied, setCopied] = useState(false);
    const scrollRef = useRef<HTMLDivElement>(null);
    const stickToBottomRef = useRef(true);

    useEffect(() => subscribeDevLog(setEntries), []);

    // Auto-scroll to bottom only if the user hasn't scrolled up to read.
    useEffect(() => {
        if (!open) return;
        if (!stickToBottomRef.current) return;
        const el = scrollRef.current;
        if (el) el.scrollTop = el.scrollHeight;
    }, [entries, open]);

    const onScroll = () => {
        const el = scrollRef.current;
        if (!el) return;
        const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 24;
        stickToBottomRef.current = atBottom;
    };

    const filtered = filter
        ? entries.filter(e => e.category.startsWith(filter))
        : entries;

    const copyAll = async () => {
        const text = filtered
            .map(e => {
                const d = formatData(e.data);
                return `[${formatTime(e.timestamp)}] [${e.category}] ${e.level.toUpperCase()}: ${e.message}${d ? ` ${d}` : ''}`;
            })
            .join('\n');
        try {
            await navigator.clipboard.writeText(text);
            setCopied(true);
            setTimeout(() => setCopied(false), 1500);
        } catch {
            /* clipboard blocked — ignore */
        }
    };

    return (
        <div className="card border border-(--accent-border)/50 bg-(--accent-ghost)/30">
            {/* Header */}
            <button
                type="button"
                onClick={() => setOpen(o => !o)}
                className="w-full flex items-center justify-between px-3 py-2 hover:bg-(--accent-ghost)/50 transition-colors"
            >
                <div className="flex items-center gap-2">
                    <Bug size={13} className="text-(--accent-light)" />
                    <span className="font-mono text-[10px] uppercase tracking-[0.08em] text-(--accent-light)">
                        {title}
                    </span>
                    <span className="text-[10px] font-mono text-(--base-06)">
                        {filtered.length} {filtered.length === 1 ? 'entry' : 'entries'}
                        {filter ? ` · filter: ${filter}*` : ''}
                    </span>
                </div>
                <div className="flex items-center gap-1">
                    <span
                        onClick={e => { e.stopPropagation(); copyAll(); }}
                        className="text-(--base-06) hover:text-(--base-09) p-1 rounded inline-flex items-center cursor-pointer"
                        title="Copy log"
                    >
                        <Copy size={11} />
                        {copied && <span className="ml-1 text-[10px] text-(--success-light)">copied</span>}
                    </span>
                    <span
                        onClick={e => { e.stopPropagation(); clearDevLog(); }}
                        className="text-(--base-06) hover:text-(--error-light) p-1 rounded inline-flex items-center cursor-pointer"
                        title="Clear log"
                    >
                        <Trash2 size={11} />
                    </span>
                    {open
                        ? <ChevronDown size={12} className="text-(--base-06)" />
                        : <ChevronUp size={12} className="text-(--base-06)" />}
                </div>
            </button>

            {/* Entries */}
            {open && (
                <div
                    ref={scrollRef}
                    onScroll={onScroll}
                    className="font-mono text-[11px] leading-snug px-3 pb-2 max-h-[280px] min-h-[120px] overflow-y-auto border-t border-(--base-03)"
                >
                    {filtered.length === 0 ? (
                        <p className="text-(--base-05) italic py-3">No entries yet — interact with the file browser to record steps.</p>
                    ) : (
                        <div className="space-y-0.5 pt-2">
                            {filtered.map(e => {
                                const d = formatData(e.data);
                                return (
                                    <div key={e.id} className="whitespace-pre-wrap break-words">
                                        <span className="text-(--base-05)">[{formatTime(e.timestamp)}]</span>
                                        {' '}
                                        <span className="text-(--base-06)">[{e.category}]</span>
                                        {' '}
                                        <span className={LEVEL_COLOR[e.level]}>{e.message}</span>
                                        {d && <span className="text-(--base-05) ml-1">{d}</span>}
                                    </div>
                                );
                            })}
                        </div>
                    )}
                </div>
            )}
        </div>
    );
}
