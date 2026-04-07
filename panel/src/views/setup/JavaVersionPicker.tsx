"use client";

import React from 'react';
import { Info } from 'lucide-react';

export const JAVA_IMAGES = [
    { id: 'ghcr.io/bartis-dev/dylaris-mc-java21:latest', label: 'Java 21', note: '1.20.5+', proxyNote: 'Recommended' },
    { id: 'ghcr.io/bartis-dev/dylaris-mc-java17:latest', label: 'Java 17', note: '1.18+', proxyNote: 'Minimum for Velocity' },
    { id: 'ghcr.io/bartis-dev/dylaris-mc-java8:latest',  label: 'Java 8',  note: '1.8–1.16', proxyNote: 'BungeeCord only' },
];

/** Returns the recommended Java image ID for a given MC major version string (e.g. "1.20.4"). */
export function recommendJavaForVersion(major: string): string | null {
    const parts = major.split('.').map(Number);
    const minor = parts[1] ?? 0;
    const patch = parts[2] ?? 0;
    if (minor >= 21 || (minor === 20 && patch >= 5)) return JAVA_IMAGES[0].id; // Java 21
    if (minor >= 18) return JAVA_IMAGES[1].id; // Java 17
    if (minor >= 8) return JAVA_IMAGES[2].id; // Java 8
    return null;
}

interface JavaVersionPickerProps {
    value: string;
    onChange: (id: string) => void;
    disabled?: boolean;
    serverType?: 'game' | 'proxy';
    recommended?: string;
}

export default function JavaVersionPicker({ value, onChange, disabled, serverType, recommended }: JavaVersionPickerProps) {
    const isProxy = serverType === 'proxy';

    return (
        <div className="flex flex-col gap-[5px]">
            <label className="input-label">Java Version</label>
            <div className="flex gap-2">
                {JAVA_IMAGES.map(j => {
                    const isRecommended = recommended === j.id;
                    return (
                        <button
                            key={j.id}
                            type="button"
                            onClick={() => !disabled && onChange(j.id)}
                            disabled={disabled}
                            className={`relative flex items-center gap-2 px-4 py-2.5 rounded-md border text-sm transition-all ${
                                value === j.id
                                    ? 'border-(--accent-border) bg-(--accent-ghost) text-(--accent-light)'
                                    : isRecommended
                                    ? 'border-(--success)/40 bg-(--success)/5 text-(--base-07) hover:border-(--success)/60'
                                    : 'border-(--base-03) text-(--base-07) hover:border-(--base-05)'
                            } ${disabled ? 'opacity-40 cursor-not-allowed' : 'cursor-pointer'}`}
                        >
                            <span className="font-medium">{j.label}</span>
                            <span className="text-xs text-(--base-06)">{isProxy ? j.proxyNote : j.note}</span>
                            {isRecommended && value !== j.id && (
                                <span className="absolute -top-2 -right-2 flex items-center gap-0.5 bg-(--success) text-white text-[9px] font-bold px-1.5 py-0.5 rounded-full">
                                    <Info size={9} />
                                </span>
                            )}
                        </button>
                    );
                })}
            </div>
        </div>
    );
}
