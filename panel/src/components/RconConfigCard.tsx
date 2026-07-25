"use client";

import { useCallback, useEffect, useRef, useState } from 'react';
import {
    Terminal, Copy, RefreshCw, EyeOff, Save, AlertTriangle,
    CircleCheck, CircleAlert, Play, RotateCcw,
} from 'lucide-react';
import { getRconConfig, setRconConfig, execRcon, friendlyRconError } from '@/lib/api/rcon';
import { serverPower } from '@/lib/api';

// RCON enable/password card. Lives as the RCON sub-section of the Players
// tab. Generates a 24-byte hex password on first enable, supports manual
// override + regenerate, and ships a "Test command" mini-console so the
// operator can verify connectivity without leaving the tab. Enabling writes
// enable-rcon + the password into server.properties on the node (Core does
// this) so the operator never hand-edits the file. onEnabledChange lets the
// parent Players tab unlock/lock its other sections as RCON flips.

interface RconConfigCardProps {
    serverId: number;
    onEnabledChange?: (enabled: boolean) => void;
}

export default function RconConfigCard({ serverId, onEnabledChange }: RconConfigCardProps) {
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
    // Set when a Save actually rewrote server.properties (Core's
    // restartRequired) while enabled=true - the running server (if any) has
    // not picked it up yet, so we hold off treating RCON as live until an
    // explicit restart + reachability check succeed. No auto-restart (owner
    // decision) - the operator drives it via the button below.
    const [needsRestart, setNeedsRestart] = useState(false);
    const [restarting, setRestarting] = useState(false);
    // Guards the post-restart reachability poll against setting state after
    // the card has unmounted (e.g. the operator navigates away mid-poll).
    const restartPollRef = useRef<{ cancelled: boolean } | null>(null);
    useEffect(() => () => { if (restartPollRef.current) restartPollRef.current.cancelled = true; }, []);

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
        // A persisted restartRequired means MC has not reopened the RCON listener
        // yet: restore the banner and, exactly like the post-save flow, keep the
        // parent Players tabs locked until a restart confirms RCON is live. This
        // is what survives a page reload (the flag now comes from the DB, not
        // just local state).
        const pending = res.enabled && (res.restartRequired ?? false);
        setNeedsRestart(pending);
        onEnabledChange?.(res.enabled && !pending);
    }, [serverId, onEnabledChange]);

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
        if (res.enabled && res.restartRequired) {
            // server.properties changed but MC has not reopened the RCON
            // listener yet - do NOT optimistically unlock the Players tabs.
            // Wait for the operator to restart and for a reachability check
            // to actually confirm RCON is live.
            setNeedsRestart(true);
        } else {
            setNeedsRestart(false);
            onEnabledChange?.(res.enabled);
        }
    }, [serverId, enabled, port, showToast, onEnabledChange]);

    const handleRestart = useCallback(async () => {
        setRestarting(true);
        try {
            const powerRes: any = await serverPower(serverId, 'restart');
            if (powerRes && (powerRes.success === false || powerRes.error)) {
                showToast(powerRes.error || powerRes.message || 'Restart failed', false);
                setRestarting(false);
                return;
            }
        } catch {
            showToast('Restart request failed', false);
            setRestarting(false);
            return;
        }
        // MC needs a few seconds to boot before the RCON listener is up; poll
        // reachability instead of guessing a fixed delay. Re-runs the same
        // exec path the Test command uses (no new endpoint).
        const token = { cancelled: false };
        restartPollRef.current = token;
        const maxAttempts = 12;
        const delayMs = 5000;
        for (let attempt = 0; attempt < maxAttempts; attempt++) {
            await new Promise(resolve => setTimeout(resolve, delayMs));
            if (token.cancelled) return;
            const test = await execRcon(serverId, 'list');
            if (token.cancelled) return;
            if (test.success) {
                setNeedsRestart(false);
                setRestarting(false);
                onEnabledChange?.(true);
                showToast('Server restarted. RCON is live.', true);
                return;
            }
        }
        setRestarting(false);
        showToast('Server restarted but RCON is still not reachable. Check the console.', false);
    }, [serverId, onEnabledChange, showToast]);

    const handleTest = useCallback(async () => {
        const cmd = testCmd.trim();
        if (!cmd) return;
        setTesting(true);
        setTestOutput(null);
        const res = await execRcon(serverId, cmd);
        setTesting(false);
        setTestOutput({
            ok: res.success,
            text: res.success ? (res.output || '(no output)') : friendlyRconError(res.error, 'RCON request failed'),
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
                        Remote console. Powers live player management (kick / ban / op) and Scheduled Tasks &ldquo;say&rdquo; jobs.
                        Enabling writes <code className="font-mono">enable-rcon=true</code> + this password into server.properties for you — restart the server to apply.
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

            {needsRestart && (
                <div className="flex items-center justify-between gap-3 px-3 py-2 rounded-md bg-(--warning-ghost) border border-(--warning)/30 text-(--base-08) text-xs">
                    <span className="flex items-center gap-1.5">
                        <AlertTriangle size={13} className="shrink-0 text-(--warning-light)" />
                        RCON enabled - restart the server to apply.
                    </span>
                    <button onClick={handleRestart} className="btn btn-secondary btn-sm shrink-0" disabled={restarting}>
                        <RotateCcw size={12} className={restarting ? 'animate-spin' : ''} />
                        {restarting ? 'Restarting…' : 'Restart now'}
                    </button>
                </div>
            )}

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
                        disabled={!enabled || !hasSecret || needsRestart}
                    />
                    <button
                        onClick={handleTest}
                        className="btn btn-secondary btn-sm"
                        disabled={testing || !enabled || !hasSecret || needsRestart}
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
