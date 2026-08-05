"use client";

import React from 'react';
import { Info, AlertTriangle } from 'lucide-react';

// The pure part lives in src/lib/javaVersion so the vitest suite can cover it.
// Re-exported here because SetupNewWizard/SetupEditMode/SetupViewMode import
// them from this module.
export { JAVA_IMAGES, recommendJavaForVersion, effectiveMcVersion } from '@/lib/javaVersion';
import { JAVA_IMAGES } from '@/lib/javaVersion';

interface JavaVersionPickerProps {
    value: string;
    onChange: (id: string) => void;
    disabled?: boolean;
    serverType?: 'game' | 'proxy';
    recommended?: string;
    /** The Minecraft major version string (e.g. "1.20.4") used in the mismatch warning. */
    mcVersion?: string;
}

export default function JavaVersionPicker({ value, onChange, disabled, serverType, recommended, mcVersion }: JavaVersionPickerProps) {
    const isProxy = serverType === 'proxy';

    const recommendedImage = recommended ? JAVA_IMAGES.find(j => j.id === recommended) : undefined;
    const showMismatchWarning = !!recommended && !!recommendedImage && value !== recommended;

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
            {showMismatchWarning && (
                <div className="alert alert-error flex items-start gap-2 mt-1 rounded-lg text-sm">
                    <AlertTriangle size={15} className="shrink-0 mt-0.5 text-(--error-light)" />
                    <span>
                        {mcVersion
                            ? <>Minecraft {mcVersion} needs {recommendedImage.label} — running a different Java version can prevent the server from starting.</>
                            : <>{recommendedImage.label} is recommended — running a different Java version can prevent the server from starting.</>
                        }
                    </span>
                </div>
            )}
        </div>
    );
}
