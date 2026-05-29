"use client";

import React, { useCallback, useEffect, useState } from 'react';
import {
    Terminal, Copy, RefreshCw, Eye, EyeOff, Save, AlertTriangle,
    CircleCheck, CircleAlert, Play,
} from 'lucide-react';
import { getRconConfig, setRconConfig, execRcon } from '@/lib/api/rcon';

// Phase 9 — RCON enable/password card. Lives at the top of the existing
// Network top-level tab. Generates a 24-byte hex password on first enable,
// supports manual override + regenerate, and ships a "Test command" mini-
// console so the operator can verify connectivity without leaving the tab.

interface RconConfigCardProps {
    serverId: number;
}

export default function RconConfigCard({ serverId }: RconConfigCardProps) {
    const [loaded, setLoaded] = useState(false);
    const [enabled, setEnabled] = useState(false);
    const [port, setPort] = useState(25575);
    const [hasSecret, setHasSecret] = useState(false);
    const [revealed, setRevealed] = useState<string | null>(null);
    const [saving, setSaving] = useState(false);
    const [dirty, setDirty] = useState(false);
    const [testCmd, setTestCmd] = useState('list');
    const [testing, setTesting] = useState(false);
    const [testOutput, setTestOutput] = useState<{ ok: boolean; text: string } | null>(null);
    const [toast, setToast] = useState<{ msg: string; ok: boolean } | null>(null);

    const showToast = useCallback((msg: string, ok = true) => {
        setToast({ msg, ok });
        setTimeout(() => setToast(null), 3500);
    }, []);

    const refresh = useCallback(async () => {
        const res = await getRconConfig(serverId);
        if (!res.success) return;
        setEnabled(res.enabled);
        setPort(res.port || 25575);
        setHasSecret(res.hasSecret);
        setLoaded(true);
        setDirty(false);
    }, [serverId]);

    useEffect(() => { refresh(); }, [refresh]);

    const handleSave = useCallback(async (opts: { regenerate?: boolean } = {}) => {
        setSaving(true);
        const res = await setRconConfig(serverId, { enabled, port, regenerate: opts.regenerate });
        setSaving(false);
        if (!res.success) {
            showToast(res.message || 'Save failed', false);
            return;
        }
        setEnabled(res.enabled);
        setPort(res.port);
        setHasSecret(res.hasSecret);
        setDirty(false);
        if (res.password) {
            setRevealed(res.password);
            showToast('Password regenerated. Copy it now — it won\'t be shown again.', true);
        } else {
            showToast(res.message || 'Saved.', true);
        }
    }, [serverId, enabled, port, showToast]);

    const handleTest = useCallback(async () => {
        const cmd = testCmd.trim();
        if (!cmd) return;
        setTesting(true);
        setTestOutput(null);
        const res = await execRcon(serverId, cmd);
        setTesting(false);
        setTestOutput({
            ok: res.success,
            text: res.success ? (res.output || '(no output)') : (res.error || 'RCON request failed'),
        });
    }, [serverId, testCmd]);

    const copy = (text: string) => {
        navigator.clipboard.writeText(text);
        showToast('Copied to clipboard.', true);
    };

    return (
        <section className="card p-5 space-y-4">
            <header className="flex items-start gap-3">
                <div className="w-9 h-9 rounded-md bg-(--accent-ghost) flex items-center justify-center shrink-0">
                    <Terminal size={16} className="text-(--accent-light)" />
                </div>
                <div className="min-w-0 flex-1">
                    <h2 className="text-base font-display font-semibold text-(--base-09)">RCON</h2>
                    <p className="text-xs text-(--base-06) mt-0.5">
                        Remote console. Required for the Players tab + Scheduled Tasks &ldquo;say&rdquo; jobs.
                        Server.properties must have <code className="font-mono">enable-rcon=true</code> + this password — toggle below.
                    </p>
                </div>
                <div className="flex items-center gap-2 shrink-0">
                    <span className="text-xs text-(--base-06)">{enabled ? 'Enabled' : 'Disabled'}</span>
                    <button
                        type="button"
                        role="switch"
                        aria-checked={enabled}
                        onClick={() => { setEnabled(e => !e); setDirty(true); }}
                        className={`toggle-track ${enabled ? 'toggle-track-on' : 'toggle-track-off'}`}
                        disabled={!loaded}
                    >
                        <span className={`toggle-knob ${enabled ? 'toggle-knob-on' : 'toggle-knob-off'}`} />
                    </button>
                </div>
            </header>

            <div className="grid grid-cols-1 sm:grid-cols-3 gap-3">
                <div>
                    <label className="input-label">RCON Port</label>
                    <input
                        type="number"
                        min={1024}
                        max={65535}
                        value={port}
                        onChange={e => { setPort(parseInt(e.target.value || '25575', 10)); setDirty(true); }}
                        className="input-field input-mono w-full"
                        disabled={!loaded}
                    />
                    <p className="text-xs text-(--base-06) mt-1">Default 25575.</p>
                </div>

                <div className="sm:col-span-2">
                    <label className="input-label">Password</label>
                    <div className="flex items-center gap-2">
                        <div className="flex-1 input-field input-mono flex items-center gap-2">
                            {revealed ? (
                                <span className="truncate">{revealed}</span>
                            ) : (
                                <span className="text-(--base-06)">{hasSecret ? '••••••••••••••••••••' : '(not set)'}</span>
                            )}
                        </div>
                        {revealed && (
                            <>
                                <button onClick={() => copy(revealed)} className="btn btn-secondary btn-sm" title="Copy">
                                    <Copy size={12} />
                                </button>
                                <button onClick={() => setRevealed(null)} className="btn btn-secondary btn-sm" title="Hide">
                                    <EyeOff size={12} />
                                </button>
                            </>
                        )}
                        <button onClick={() => handleSave({ regenerate: true })} className="btn btn-secondary btn-sm" disabled={saving}>
                            <RefreshCw size={12} />
                            Regenerate
                        </button>
                    </div>
                    {!hasSecret && enabled && (
                        <p className="text-xs text-(--warning-light) mt-1 flex items-center gap-1">
                            <AlertTriangle size={11} />
                            Saving will mint a fresh random password (shown once).
                        </p>
                    )}
                </div>
            </div>

            <div className="flex items-center justify-between border-t border-(--base-03) pt-3">
                <p className="text-xs text-(--base-06)">
                    Requires a server restart to take effect.
                </p>
                <button onClick={() => handleSave()} className="btn btn-primary btn-sm" disabled={!dirty || saving}>
                    <Save size={12} />
                    Save
                </button>
            </div>

            {/* Test command — quick sanity check from inside the panel */}
            <div className="border-t border-(--base-03) pt-3">
                <label className="input-label flex items-center gap-2">
                    <Play size={11} />
                    Test command
                </label>
                <div className="flex items-center gap-2 mt-1">
                    <input
                        type="text"
                        value={testCmd}
                        onChange={e => setTestCmd(e.target.value)}
                        onKeyDown={e => { if (e.key === 'Enter') handleTest(); }}
                        placeholder="e.g. list"
                        className="input-field input-mono flex-1"
                        disabled={!enabled || !hasSecret}
                    />
                    <button
                        onClick={handleTest}
                        className="btn btn-secondary btn-sm"
                        disabled={testing || !enabled || !hasSecret}
                    >
                        {testing ? 'Running…' : 'Run'}
                    </button>
                </div>
                {testOutput && (
                    <pre className={`mt-2 p-2 rounded-md font-mono text-xs whitespace-pre-wrap border ${
                        testOutput.ok
                            ? 'bg-(--base-02) border-(--base-03) text-(--base-08)'
                            : 'bg-(--error-ghost) border-(--error)/30 text-(--error-light)'
                    }`}>{testOutput.text}</pre>
                )}
            </div>

            {toast && (
                <div className="toast-container">
                    <div className="toast">
                        <div className={`toast-bar ${toast.ok ? 'bg-(--success-light)' : 'bg-(--error-light)'}`}></div>
                        {toast.ok ? <CircleCheck size={14} /> : <CircleAlert size={14} />}
                        <span className="text-sm text-(--base-09)">{toast.msg}</span>
                    </div>
                </div>
            )}
        </section>
    );
}
