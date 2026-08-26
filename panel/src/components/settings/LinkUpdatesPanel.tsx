"use client";

import { useState, useEffect, useCallback, useRef } from 'react';
import {
    getLinkUpdateSettings, saveLinkUpdateSettings, getLinkUpdateStates, triggerLinkUpdate,
    getNodes, type LinkUpdatePolicy, type LinkUpdateSettings, type NodeLinkState, type Node,
} from '@/lib/api';
import Select from '@/components/ui/Select';
import { confirmDialog } from '@/components/ui/ConfirmDialog';
import { SkeletonCard } from '@/components/Skeleton';
import { RefreshCw, CircleCheck, AlertTriangle, Link2 } from 'lucide-react';
import { useUnsavedChanges } from '@/components/settings/UnsavedChanges';

const POLICY_OPTIONS: { value: LinkUpdatePolicy; label: string }[] = [
    { value: 'auto_idle', label: 'When idle — apply once no players are connected' },
    { value: 'auto', label: 'Immediately — apply as soon as an update is found' },
    { value: 'notify', label: 'Notify only — tell me, apply nothing' },
];

interface Props { showToast: (msg: string, ok: boolean) => void }

export default function LinkUpdatesPanel({ showToast }: Props) {
    const [settings, setSettings] = useState<LinkUpdateSettings | null>(null);
    const [states, setStates] = useState<Record<string, NodeLinkState>>({});
    const [nodes, setNodes] = useState<Node[]>([]);
    const [saving, setSaving] = useState(false);
    const [busy, setBusy] = useState<string | null>(null);

    const loadStates = useCallback(async () => {
        const [s, n] = await Promise.all([getLinkUpdateStates(), getNodes()]);
        setStates(s);
        setNodes(Array.isArray(n) ? n : []);
    }, []);

    // What was last loaded or saved. This panel is a two-field form with a Save
    // and no dirty tracking; the Apply buttons below it are actions and stay
    // immediate with their confirmation.
    const snapshotRef = useRef<LinkUpdateSettings | null>(null);

    useEffect(() => {
        getLinkUpdateSettings().then(s => {
            const stored = s ?? { policy: 'auto_idle' as const, intervalMinutes: 15 };
            setSettings(stored);
            snapshotRef.current = stored;
        });
        loadStates();
        // The node reports on its heartbeat, so a freshly applied update shows up
        // within a beat rather than instantly. Poll gently instead of pretending.
        const t = setInterval(loadStates, 15000);
        return () => clearInterval(t);
    }, [loadStates]);

    const save = async (): Promise<boolean> => {
        if (!settings) return false;
        setSaving(true);
        try {
            const res = await saveLinkUpdateSettings(settings);
            if (res) snapshotRef.current = settings;
            showToast(res ? 'Link update policy saved' : 'Could not save the Link update policy', !!res);
            return !!res;
        } finally {
            setSaving(false);
        }
    };

    const dirty =
        snapshotRef.current !== null &&
        JSON.stringify(settings) !== JSON.stringify(snapshotRef.current);

    useUnsavedChanges({
        dirty,
        saving,
        save,
        discard: () => { if (snapshotRef.current) setSettings(snapshotRef.current); },
    });

    const apply = async (nodeId?: string) => {
        const label = nodeId ? `node ${nodeId}` : 'every node with a pending update';
        const ok = await confirmDialog({
            title: 'Update the Link now?',
            message: `Replacing the Link on ${label} interrupts its player connections for roughly 10 to 30 seconds. Servers keep running and players reconnect on their own.`,
            confirmLabel: 'Update now',
            destructive: true,
        });
        if (!ok) return;
        setBusy(nodeId ?? '__all__');
        const res = await triggerLinkUpdate(nodeId);
        setBusy(null);
        if (!res) { showToast('Could not queue the Link update', false); return; }
        showToast(res.count > 0 ? `Link update queued for ${res.count} node(s)` : 'Nothing to update', res.count > 0);
        loadStates();
    };

    if (!settings) return <SkeletonCard />;

    // Only nodes that manage their own Link can be acted on here. A node running
    // an operator-deployed Link is listed with an explanation rather than a
    // button that would do nothing.
    const managed = nodes.filter(n => states[n.token]?.managed);
    const pending = managed.filter(n => states[n.token]?.updateAvailable);
    const unmanaged = nodes.filter(n => states[n.token] && !states[n.token].managed);

    return (
        <div className="space-y-6">
            <div className="rounded-xl border border-(--base-03) bg-(--base-01) p-5">
                <div className="flex items-center gap-2 mb-1">
                    <Link2 size={16} className="text-(--accent-light)" />
                    <h3 className="text-sm font-medium text-(--base-08)">Link sidecar updates</h3>
                </div>
                <p className="text-xs text-(--base-06) mb-4">
                    Each node runs a Link sidecar that carries gateway traffic. A Link older than the
                    edges it talks to does not fail outright, it misbehaves subtly, so keeping them in
                    step matters.{' '}
                    <span className="text-(--base-07)">
                        External and BYON nodes always update immediately and ignore this setting — there is
                        no operator on those machines to act on a notification.
                    </span>
                </p>

                <div className="grid gap-4 sm:grid-cols-2">
                    <label className="block">
                        <span className="text-xs text-(--base-07) mb-1.5 block">When to apply (datacenter nodes)</span>
                        <Select
                            value={settings.policy}
                            onChange={v => setSettings(s => s && ({ ...s, policy: v as LinkUpdatePolicy }))}
                            options={POLICY_OPTIONS}
                        />
                    </label>
                    <label className="block">
                        <span className="text-xs text-(--base-07) mb-1.5 block">Check every (minutes)</span>
                        <input
                            type="number"
                            min={1}
                            max={1440}
                            value={settings.intervalMinutes}
                            onChange={e => setSettings(s => s && ({ ...s, intervalMinutes: Number(e.target.value) }))}
                            className="w-full rounded-lg border border-(--base-03) bg-(--base-00) px-3 py-2 text-sm text-(--base-08) focus:outline-none focus:ring-2 focus:ring-(--focus-ring)"
                        />
                    </label>
                </div>

            </div>

            <div className="rounded-xl border border-(--base-03) bg-(--base-01) p-5">
                <div className="flex items-center justify-between mb-3">
                    <h3 className="text-sm font-medium text-(--base-08)">Node status</h3>
                    {pending.length > 0 && (
                        <button
                            onClick={() => apply()}
                            disabled={busy !== null}
                            className="inline-flex items-center gap-2 rounded-lg border border-(--base-03) px-3 py-1.5 text-xs text-(--base-08) transition hover:border-(--accent) disabled:opacity-50"
                        >
                            <RefreshCw size={13} className={busy === '__all__' ? 'animate-spin' : ''} />
                            Update all {pending.length}
                        </button>
                    )}
                </div>

                {managed.length === 0 && unmanaged.length === 0 && (
                    <p className="text-xs text-(--base-06)">
                        No node is reporting its Link status yet. Nodes report on their heartbeat, and an
                        older node image does not report at all.
                    </p>
                )}

                <ul className="divide-y divide-(--base-03)">
                    {managed.map(n => {
                        const st = states[n.token];
                        return (
                            <li key={n.token} className="flex items-center justify-between gap-3 py-2.5">
                                <div className="min-w-0">
                                    <p className="truncate text-sm text-(--base-08)">{n.name || n.token}</p>
                                    <p className="text-xs text-(--base-06)">
                                        {st.updateAvailable
                                            ? 'A newer Link image is available'
                                            : 'Link is running the current image'}
                                    </p>
                                </div>
                                {st.updateAvailable ? (
                                    <button
                                        onClick={() => apply(n.token)}
                                        disabled={busy !== null}
                                        className="inline-flex shrink-0 items-center gap-1.5 rounded-lg border border-(--base-03) px-2.5 py-1.5 text-xs text-(--base-08) transition hover:border-(--accent) disabled:opacity-50"
                                    >
                                        <AlertTriangle size={12} className="text-(--accent-light)" />
                                        {busy === n.token ? 'Queuing…' : 'Update'}
                                    </button>
                                ) : (
                                    <CircleCheck size={15} className="shrink-0 text-(--base-05)" />
                                )}
                            </li>
                        );
                    })}
                </ul>

                {unmanaged.length > 0 && (
                    <p className="mt-3 border-t border-(--base-03) pt-3 text-xs text-(--base-06)">
                        {unmanaged.map(n => n.name || n.token).join(', ')}:{' '}
                        the Link is deployed by an operator, not by the node, so it cannot be updated from here.
                    </p>
                )}
            </div>
        </div>
    );
}
