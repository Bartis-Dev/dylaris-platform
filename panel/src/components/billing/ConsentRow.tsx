"use client";

import { AlertTriangle, Loader2 } from 'lucide-react';
import Switch from '@/components/ui/Switch';
import { consequenceOf, type SwitchState } from '@/lib/storeAccount';

/**
 * One metered-billing consent, with the CONSEQUENCE written under it rather
 * than the setting name. "Metered billing: off" tells a tenant nothing; what
 * they are choosing is whether running out costs money or stops the service.
 *
 * Its own file because the two switches ended up on two screens: the traffic
 * one belongs beside the traffic bars, where somebody watching an allowance
 * fill up is the person deciding, and the backup one stays on the store page
 * with the backup allowance. One component, so the two can never come to
 * describe the same choice differently.
 */
export default function ConsentRow({ title, kind, state, busy, onChange }: {
    title: string;
    kind: 'traffic' | 'backup';
    state: SwitchState;
    busy: boolean;
    onChange: (next: boolean) => void;
}) {
    if (state.kind === 'unavailable') {
        return (
            <div className="flex items-start gap-2.5">
                <AlertTriangle size={15} className="text-(--base-06) mt-0.5 shrink-0" />
                <div className="min-w-0">
                    <div className="text-sm text-(--base-08)">{title}</div>
                    <p className="text-xs text-(--base-06) mt-0.5 leading-relaxed">{state.reason}</p>
                </div>
            </div>
        );
    }
    const on = state.kind === 'on';
    return (
        <div className="flex items-start justify-between gap-4">
            <div className="min-w-0">
                <div className="text-sm text-(--base-08)">{title}</div>
                <p className="text-xs text-(--base-06) mt-0.5 leading-relaxed">{consequenceOf(kind, on)}</p>
            </div>
            <div className="shrink-0 pt-0.5">
                {busy
                    ? <Loader2 size={16} className="animate-spin text-(--base-06)" />
                    : <Switch checked={on} onChange={onChange} ariaLabel={title} />}
            </div>
        </div>
    );
}
