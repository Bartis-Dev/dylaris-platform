"use client";

import { useState, useEffect, useCallback } from 'react';
import {
    getLinkUpdateSettings, saveLinkUpdateSettings, getLinkUpdateStates, triggerLinkUpdate,
    getNodes, type LinkUpdatePolicy, type LinkUpdateSettings, type NodeLinkState, type Node,
} from '@/lib/api';
import Select from '@/components/ui/Select';
import { confirmDialog } from '@/components/ui/ConfirmDialog';
import { SkeletonCard } from '@/components/Skeleton';
import { RefreshCw, CircleCheck, AlertTriangle, Link2, Activity } from 'lucide-react';
import { useSettingsForm } from '@/lib/useSettingsForm';
import SettingsCard from '@/components/settings/SettingsCard';

const POLICY_OPTIONS: { value: LinkUpdatePolicy; label: string }[] = [
    { value: 'auto_idle', label: 'When idle - apply once no players are connected' },
    { value: 'auto', label: 'Immediately - apply as soon as an update is found' },
    { value: 'notify', label: 'Notify only - tell me, apply nothing' },
];

interface Props { showToast: (msg: string, ok: boolean) => void }

export default function LinkUpdatesPanel({ showToast }: Props) {
    const [states, setStates] = useState<Record<string, NodeLinkState>>({});
    const [nodes, setNodes] = useState<Node[]>([]);
    const [busy, setBusy] = useState<string | null>(null);

    const loadStates = useCallback(async () => {
        const [s, n] = await Promise.all([getLinkUpdateStates(), getNodes()]);
        setStates(s);
        setNodes(Array.isArray(n) ? n : []);
    }, []);

    const form = useSettingsForm<LinkUpdateSettings>({
        load: async () => (await getLinkUpdateSettings()) ?? { policy: 'auto_idle', intervalMinutes: 15 },
        save: async value => ({
            ok: !!(await saveLinkUpdateSettings(value)),
            message: 'Could not save the Link update policy.',
        }),
        successMessage: 'Link update policy saved.',
    });

    useEffect(() => {
        loadStates();
        // The node reports on its heartbeat, so a freshly applied update shows up
        // within a beat rather than instantly. Poll gently instead of pretending.
        const t = setInterval(loadStates, 15000);
        return () => clearInterval(t);
    }, [loadStates]);

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

    const settings = form.value;
    if (!settings) return <SkeletonCard />;

    // Only nodes that manage their own Link can be acted on here. A node running
    // an operator-deployed Link is listed with an explanation rather than a
    // button that would do nothing.
    const managed = nodes.filter(n => states[n.token]?.managed);
    const pending = managed.filter(n => states[n.token]?.updateAvailable);
    const unmanaged = nodes.filter(n => states[n.token] && !states[n.token].managed);

    return (
        <div className="space-y-5">
            <SettingsCard
                title="Link sidecar updates"
                icon={Link2}
                description="A Link older than the edges it talks to does not fail outright, it misbehaves subtly, so keeping them in step matters."
                form={form}
                footer="External and BYON nodes always update immediately and ignore this setting. There is no operator on those machines to act on a notification."
            >
                <div className="grid gap-4 sm:grid-cols-2">
                    <label className="block">
                        <span className="input-label">When to apply (datacenter nodes)</span>
                        <Select
                            value={settings.policy}
                            onChange={v => form.patch({ policy: v as LinkUpdatePolicy })}
                            options={POLICY_OPTIONS}
                        />
                    </label>
                    <label className="block">
                        <span className="input-label">Check every (minutes)</span>
                        <input
                            type="number"
                            min={1}
                            max={1440}
                            value={settings.intervalMinutes}
                            onChange={e => form.patch({ intervalMinutes: Number(e.target.value) })}
                            className="input-field w-full"
                        />
                    </label>
                </div>
            </SettingsCard>

            <SettingsCard
                title="Node status"
                icon={Activity}
                bodySpacing="none"
                actions={
                    pending.length > 0 && (
                        <button
                            onClick={() => apply()}
                            disabled={busy !== null}
                            className="btn btn-secondary btn-sm inline-flex items-center gap-1.5"
                        >
                            <RefreshCw size={13} className={busy === '__all__' ? 'animate-spin' : ''} />
                            Update all {pending.length}
                        </button>
                    )
                }
                footer={
                    unmanaged.length > 0 ? (
                        <>
                            {unmanaged.map(n => n.name || n.token).join(', ')}:{' '}
                            the Link is deployed by an operator, not by the node, so it cannot be
                            updated from here.
                        </>
                    ) : undefined
                }
            >
                {managed.length === 0 && unmanaged.length === 0 ? (
                    <p className="text-xs text-(--base-06)">
                        No node is reporting its Link status yet. Nodes report on their heartbeat, and an
                        older node image does not report at all.
                    </p>
                ) : (
                    <ul className="divide-y divide-(--base-03)">
                        {managed.map(n => {
                            const st = states[n.token];
                            return (
                                <li key={n.token} className="flex items-center justify-between gap-3 py-2.5 first:pt-0">
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
                                            className="btn btn-secondary btn-sm shrink-0 inline-flex items-center gap-1.5"
                                        >
                                            <AlertTriangle size={12} className="text-(--accent-light)" />
                                            {busy === n.token ? 'Queuing...' : 'Update'}
                                        </button>
                                    ) : (
                                        <CircleCheck size={15} className="shrink-0 text-(--base-05)" />
                                    )}
                                </li>
                            );
                        })}
                    </ul>
                )}
            </SettingsCard>
        </div>
    );
}
