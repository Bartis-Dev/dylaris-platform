"use client";

import React, { useEffect, useState } from 'react';
import { listRegions, Region } from '@/lib/api/regions';
import { Check, Globe, Infinity as InfinityIcon } from 'lucide-react';

interface UserRegionPickerProps {
    /** Selected explicit region ids. Ignored when `allRegions` is true. */
    regions: string[];
    /** Whether the user has all-regions (incl. future) access. */
    allRegions: boolean;
    /** Called on every change with the new state. */
    onChange: (next: { allRegions: boolean; regions: string[] }) => void;
    /** Hide the entire picker when only the 'default' region exists.
     *  Most admins on single-region deployments never want to see this. */
    autoHideOnSingleRegion?: boolean;
    /** Disable the controls (e.g. during save). */
    disabled?: boolean;
    /** Optional className wrapping the picker. */
    className?: string;
}

export default function UserRegionPicker({
    regions: selected,
    allRegions,
    onChange,
    autoHideOnSingleRegion = true,
    disabled,
    className,
}: UserRegionPickerProps) {
    const [available, setAvailable] = useState<Region[]>([]);
    const [loaded, setLoaded] = useState(false);

    useEffect(() => {
        listRegions().then(res => {
            if (res.success) setAvailable(res.regions || []);
            setLoaded(true);
        });
    }, []);

    if (!loaded) {
        return (
            <div className={className}>
                <label className="input-label">Region access</label>
                <p className="text-xs text-(--base-06) font-mono">Loading…</p>
            </div>
        );
    }

    const isSingleRegionSetup = available.length === 1 && available[0].id === 'default';
    if (autoHideOnSingleRegion && isSingleRegionSetup) {
        // In single-region setups the picker is meaningless — the user always
        // gets 'default'. Caller still needs to send allRegions:true on submit
        // so existing access is preserved; the parent form handles that.
        return null;
    }

    const toggleAll = () => {
        onChange({ allRegions: !allRegions, regions: allRegions ? selected : [] });
    };

    const toggleRegion = (rid: string) => {
        if (allRegions) return;
        const next = selected.includes(rid)
            ? selected.filter(r => r !== rid)
            : [...selected, rid];
        onChange({ allRegions: false, regions: next });
    };

    return (
        <div className={className}>
            <label className="input-label flex items-center gap-1.5">
                <Globe size={12} /> Region access
            </label>

            <label className={`flex items-center gap-2 p-2 rounded-md border transition-colors cursor-pointer mt-1 ${
                allRegions
                    ? 'border-(--accent-border) bg-(--accent-ghost) text-(--accent-light)'
                    : 'border-(--base-04) hover:bg-(--base-03)'
            } ${disabled ? 'opacity-60 pointer-events-none' : ''}`}>
                <input
                    type="checkbox"
                    checked={allRegions}
                    onChange={toggleAll}
                    disabled={disabled}
                    className="accent-(--accent)"
                />
                <InfinityIcon size={14} />
                <span className="text-sm font-medium">All regions (current &amp; future)</span>
            </label>

            <div className={`mt-2 grid grid-cols-1 sm:grid-cols-2 gap-1.5 ${allRegions ? 'opacity-50' : ''}`}>
                {available.map(r => {
                    const isOn = allRegions || selected.includes(r.id);
                    return (
                        <button
                            key={r.id}
                            type="button"
                            onClick={() => toggleRegion(r.id)}
                            disabled={disabled || allRegions}
                            className={`flex items-center gap-2 p-2 rounded-md border text-left text-sm transition-colors ${
                                isOn
                                    ? 'border-(--success-border) bg-(--success-ghost) text-(--success-light)'
                                    : 'border-(--base-04) hover:bg-(--base-03)'
                            } disabled:cursor-not-allowed`}
                        >
                            <span
                                className="w-2 h-2 rounded-full shrink-0"
                                style={{ background: r.color || (isOn ? 'var(--success-light)' : 'var(--base-06)') }}
                            />
                            <span className="font-mono text-xs">{r.id}</span>
                            <span className="text-xs text-(--base-06) truncate">— {r.displayName}</span>
                            {isOn && <Check size={12} className="ml-auto" />}
                        </button>
                    );
                })}
            </div>

            <p className="text-xs text-(--base-06) mt-1.5">
                {allRegions
                    ? 'User can see and create resources in every region (now and any added later).'
                    : selected.length === 0
                        ? 'User has no region access — they will not see any servers.'
                        : `User is limited to ${selected.length} region${selected.length === 1 ? '' : 's'}.`}
            </p>
        </div>
    );
}
